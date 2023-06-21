package pgbk

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gravitational/trace"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgtype"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/sirupsen/logrus"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/utils/retryutils"
	"github.com/gravitational/teleport/lib/backend"
)

const deleteBatchSize = 1000

func New(ctx context.Context, params backend.Params) (*Backend, error) {
	connString, _ := params["conn_string"].(string)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	log := logrus.WithField(trace.Component, teleport.Component("pgbk"))

	if azure, _ := params["azure"].(bool); azure {
		clientID, _ := params["azure_client_id"].(string)
		log.WithField("client_id", clientID).Warn("Using Azure authentication.")
		bc, err := azureBeforeConnect(clientID, log)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		poolConfig.BeforeConnect = bc
	}

	poolConfig.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET default_transaction_isolation TO serializable", pgx.QuerySimpleProtocol(true))
		return trace.Wrap(err)
	}

	log.Info("Setting up backend.")

	tryEnsureDatabase(ctx, poolConfig, log)

	pool, err := pgxpool.ConnectConfig(ctx, poolConfig)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	b := &Backend{
		log:  log,
		pool: pool,
	}

	if err := b.setupAndMigrate(ctx); err != nil {
		b.Close()
		return nil, trace.Wrap(err)
	}

	b.buf = backend.NewCircularBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	b.wg.Add(1)
	go b.backgroundExpiry(ctx)

	b.wg.Add(1)
	go b.backgroundChangeFeed(ctx)

	return b, nil
}

type Backend struct {
	log  logrus.FieldLogger
	pool *pgxpool.Pool
	buf  *backend.CircularBuffer

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (b *Backend) Close() error {
	b.cancel()
	b.wg.Wait()
	b.pool.Close()
	return nil
}

func (b *Backend) retry(ctx context.Context, f func(*pgxpool.Pool) error) error {
	retry, err := retryutils.NewLinear(retryutils.LinearConfig{
		First:  0,
		Step:   100 * time.Millisecond,
		Max:    750 * time.Millisecond,
		Jitter: retryutils.NewHalfJitter(),
	})
	if err != nil {
		return trace.Wrap(err)
	}

	for i := 1; i < 20; i++ {
		if err := f(b.pool); err == nil {
			return nil
		}

		if isCode(err, pgerrcode.SerializationFailure) || isCode(err, pgerrcode.DeadlockDetected) {
			b.log.WithError(err).WithField("attempt", i).Debug("Operation failed due to conflicts, retrying quickly.")
			retry.Reset()
		} else {
			b.log.WithError(err).WithField("attempt", i).Debug("Operation failed, retrying.")
			retry.Inc()
		}

		select {
		case <-retry.After():
		case <-ctx.Done():
			return ctx.Err()
		}

	}

	return trace.LimitExceeded("too many retries, last error: %v", err)
}

func (b *Backend) beginTxFunc(ctx context.Context, txOptions pgx.TxOptions, f func(pgx.Tx) error) error {
	return b.retry(ctx, func(p *pgxpool.Pool) error {
		return p.BeginTxFunc(ctx, txOptions, f)
	})
}

func (b *Backend) setupAndMigrate(ctx context.Context) error {
	const (
		latestVersion = 2
	)
	var version int
	var migrateErr error
	if err := b.beginTxFunc(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS migrate (
				version int PRIMARY KEY,
				created timestamptz NOT NULL DEFAULT now()
			)`, pgx.QuerySimpleProtocol(true),
		); err != nil && !isCode(err, pgerrcode.InsufficientPrivilege) {
			return trace.Wrap(err)
		}

		if err := tx.QueryRow(ctx,
			"SELECT COALESCE(max(version), 0) FROM migrate",
			pgx.QuerySimpleProtocol(true),
		).Scan(&version); err != nil {
			return trace.Wrap(err)
		}

		switch version {
		case 0:
			if _, err := tx.Exec(ctx, `
				CREATE TABLE kv (
					key bytea PRIMARY KEY,
					value bytea NOT NULL,
					expires timestamp
				);
				CREATE INDEX kv_expires ON kv (expires) WHERE expires IS NOT NULL;
				INSERT INTO migrate (version) VALUES (2);`,
				pgx.QuerySimpleProtocol(true),
			); err != nil {
				return trace.Wrap(err)
			}
		case latestVersion:
			// nothing to do
		default:
			migrateErr = trace.BadParameter("unsupported schema version %v", version)
		}

		return nil
	}); err != nil {
		return trace.Wrap(err)
	}

	if migrateErr != nil {
		return migrateErr
	}

	if version != latestVersion {
		b.log.WithFields(logrus.Fields{
			"prev_version": version,
			"cur_version":  latestVersion,
		}).Info("Migrated database schema.")
	}

	return nil
}

var _ backend.Backend = (*Backend)(nil)

// Create writes item if key doesn't exist
func (b *Backend) Create(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	var r int64
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		tag, err := p.Exec(ctx, `
			INSERT INTO kv (key, value, expires) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires = excluded.expires
			WHERE kv.expires IS NOT NULL AND kv.expires <= now()`,
			i.Key, i.Value, toPgTime(i.Expires))
		if err != nil {
			return trace.Wrap(err)
		}
		r = tag.RowsAffected()
		return nil
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	if r < 1 {
		return nil, trace.AlreadyExists("key %q already exists", i.Key)
	}
	return newLease(i), nil
}

// Put writes item
func (b *Backend) Put(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		_, err := p.Exec(ctx, `
			INSERT INTO kv (key, value, expires) VALUES ($1, $2, $3)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires = excluded.expires`,
			i.Key, i.Value, toPgTime(i.Expires))
		return trace.Wrap(err)
	}); err != nil {
		return nil, trace.Wrap(err)
	}
	return newLease(i), nil
}

// CompareAndSwap
func (b *Backend) CompareAndSwap(ctx context.Context, expected backend.Item, replaceWith backend.Item) (*backend.Lease, error) {
	if !bytes.Equal(expected.Key, replaceWith.Key) {
		return nil, trace.BadParameter("expected and replaceWith keys should match")
	}
	var r int64
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		tag, err := p.Exec(ctx,
			"UPDATE kv SET value = $2, expires = $3 WHERE key = $1 AND value = $4 AND (expires IS NULL OR expires > now())",
			replaceWith.Key, replaceWith.Value, toPgTime(replaceWith.Expires), expected.Value)
		if err != nil {
			return trace.Wrap(err)
		}
		r = tag.RowsAffected()
		return nil
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	if r < 1 {
		return nil, trace.CompareFailed("key %q does not exist or does not match expected", replaceWith.Key)
	}
	return newLease(replaceWith), nil
}

// Update writes item if key exists
func (b *Backend) Update(ctx context.Context, i backend.Item) (*backend.Lease, error) {
	var r int64
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		tag, err := p.Exec(ctx,
			"UPDATE kv SET value = $2, expires = $3 WHERE key = $1 AND (expires IS NULL OR expires > now())",
			i.Key, i.Value, toPgTime(i.Expires))
		if err != nil {
			return trace.Wrap(err)
		}
		r = tag.RowsAffected()
		return nil
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	if r < 1 {
		return nil, trace.NotFound("key %q does not exist", i.Key)
	}
	return newLease(i), nil
}

// Get implements backend.Backend
func (b *Backend) Get(ctx context.Context, key []byte) (*backend.Item, error) {
	found := false
	var value []byte
	var expires pgtype.Timestamp
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		found, value, expires.Time = false, nil, time.Time{}
		err := p.QueryRow(ctx, `
			SELECT value, expires FROM kv
			WHERE key = $1 AND (expires IS NULL OR expires > now())`,
			key).Scan(&value, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return trace.Wrap(err)
		}
		found = true
		return nil
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	if !found {
		return nil, trace.NotFound("key %q does not exist", key)
	}

	return &backend.Item{
		Key:     key,
		Value:   value,
		Expires: expires.Time,
	}, nil
}

// GetRange implements backend.Backend
func (b *Backend) GetRange(ctx context.Context, startKey []byte, endKey []byte, limit int) (*backend.GetResult, error) {
	if limit <= 0 {
		limit = backend.DefaultRangeLimit
	}
	r := backend.GetResult{}
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		r.Items = nil
		var k, v []byte
		var e pgtype.Timestamp
		_, err := p.QueryFunc(ctx, `
			SELECT key, value, expires FROM kv
			WHERE key BETWEEN $1 AND $2 AND (expires IS NULL OR expires > now())
			LIMIT $3`,
			[]any{startKey, endKey, limit}, []any{&k, &v, &e},
			func(pgx.QueryFuncRow) error {
				r.Items = append(r.Items, backend.Item{
					Key:     k,
					Value:   v,
					Expires: e.Time,
				})
				k, v = nil, nil
				return nil
			})
		return trace.Wrap(err)
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	return &r, nil
}

// Delete implements backend.Backend
func (b *Backend) Delete(ctx context.Context, key []byte) error {
	var r int64
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		tag, err := p.Exec(ctx,
			"DELETE FROM kv WHERE key = $1 AND (expires IS NULL OR expires > now())",
			key)
		if err != nil {
			return trace.Wrap(err)
		}
		r = tag.RowsAffected()
		return nil
	}); err != nil {
		return trace.Wrap(err)
	}

	if r < 1 {
		return trace.NotFound("key %q does not exist", key)
	}
	return nil
}

// DeleteRange implements backend.Backend
func (b *Backend) DeleteRange(ctx context.Context, startKey []byte, endKey []byte) error {
	// logical decoding (before Postgres 13?) can become esponentially slow the
	// bigger the transaction; thankfully, we can just limit our transactions to
	// a small-ish number of affected rows (1000 seems to work ok) as we don't
	// need atomicity here or in backgroundExpiry, which are the only two places
	// in which we do transactions that affect more than one row
	for i := 0; i < backend.DefaultRangeLimit/deleteBatchSize; i++ {
		var r int64
		if err := b.retry(ctx, func(p *pgxpool.Pool) error {
			tag, err := p.Exec(ctx,
				"DELETE FROM kv WHERE key = ANY(ARRAY(SELECT key FROM kv WHERE key BETWEEN $1 AND $2 LIMIT $3))",
				startKey, endKey, deleteBatchSize)
			if err != nil {
				return trace.Wrap(err)
			}
			r = tag.RowsAffected()
			return nil
		}); err != nil {
			return trace.Wrap(err)
		}

		if r < deleteBatchSize {
			return nil
		}
	}

	return trace.LimitExceeded("too many iterations")
}

// KeepAlive implements backend.Backend
func (b *Backend) KeepAlive(ctx context.Context, lease backend.Lease, expires time.Time) error {
	var r int64
	if err := b.retry(ctx, func(p *pgxpool.Pool) error {
		tag, err := p.Exec(ctx,
			"UPDATE kv SET expires = $2 WHERE key = $1 AND (expires IS NULL OR expires > now())",
			lease.Key, toPgTime(expires))
		if err != nil {
			return trace.Wrap(err)
		}
		r = tag.RowsAffected()
		return trace.Wrap(err)
	}); err != nil {
		return trace.Wrap(err)
	}

	if r < 1 {
		return trace.NotFound("key %q does not exist", lease.Key)
	}
	return nil
}

// NewWatcher implements backend.Backend
func (b *Backend) NewWatcher(ctx context.Context, watch backend.Watch) (backend.Watcher, error) {
	return b.buf.NewWatcher(ctx, watch)
}

// CloseWatchers implements backend.Backend
func (b *Backend) CloseWatchers() { b.buf.Clear() }

// Clock implements backend.Backend
func (*Backend) Clock() clockwork.Clock { return clockwork.NewRealClock() }
