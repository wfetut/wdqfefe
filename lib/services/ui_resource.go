package services

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/btree"
	"github.com/gravitational/teleport"
	apidefaults "github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	log "github.com/sirupsen/logrus"
)

type Config struct {
	// Context is a context for opening the
	// database
	Context context.Context
	// BTreeDegree is a degree of B-Tree, 2 for example, will create a
	// 2-3-4 tree (each node contains 1-3 items and 2-4 children).
	BTreeDegree int
	// Clock is a clock for time-related operations
	Clock clockwork.Clock
	// Component is a logging component
	Component string
	// EventsOff turns off events generation
	EventsOff bool
	// BufferSize sets up event buffer size
	BufferSize int
	// Mirror mode is used when the memory backend is used for caching. In mirror
	// mode, record IDs for Put and PutRange requests are re-used (instead of
	// generating fresh ones) and expiration is turned off.
	Mirror bool
}

// New creates a new memory cache that holds the unified resources
func NewUIResourceCache(cfg Config) (*UIResourceCache, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	ctx, cancel := context.WithCancel(cfg.Context)
	buf := backend.NewCircularBuffer(
		backend.BufferCapacity(cfg.BufferSize),
	)
	buf.SetInit()
	m := &UIResourceCache{
		Mutex: &sync.Mutex{},
		Entry: log.WithFields(log.Fields{
			trace.Component: teleport.ComponentMemory,
		}),
		Config: cfg,
		tree: btree.NewG(cfg.BTreeDegree, func(a, b *btreeItem) bool {
			return a.Less(b)
		}),
		cancel: cancel,
		ctx:    ctx,
		buf:    buf,
	}
	return m, nil
}

// CheckAndSetDefaults checks and sets default values
func (cfg *Config) CheckAndSetDefaults() error {
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = backend.DefaultBufferCapacity
	}
	if cfg.BTreeDegree <= 0 {
		cfg.BTreeDegree = 8
	}
	if cfg.Clock == nil {
		cfg.Clock = clockwork.NewRealClock()
	}
	if cfg.Component == "" {
		cfg.Component = teleport.ComponentUIResource
	}
	return nil
}

type btreeItem struct {
	Item
	index int
}

// Less is used for Btree operations,
// returns true if item is less than the other one
func (i *btreeItem) Less(iother btree.Item) bool {
	switch other := iother.(type) {
	case *btreeItem:
		return bytes.Compare(i.Key, other.Key) < 0
	case *prefixItem:
		return !iother.Less(i)
	default:
		return false
	}
}

// prefixItem is used for prefix matches on a B-Tree
type prefixItem struct {
	// prefix is a prefix to match
	prefix []byte
}

// Less is used for Btree operations
func (p *prefixItem) Less(iother btree.Item) bool {
	other := iother.(*btreeItem)
	return !bytes.HasPrefix(other.Key, p.prefix)
}

type Item struct {
	// Key is a key of the key value item
	Key []byte
	// Value is a value of the key value item
	Value types.ResourceWithLabels
	// ID is a record ID, newer records have newer ids
	ID int64
}

type UIResourceCache struct {
	// nextID is a next record ID
	// intentionally placed first to ensure 64-bit alignment
	nextID int64

	*sync.Mutex
	*log.Entry
	Config
	// tree is a BTree with items
	tree *btree.BTreeG[*btreeItem]
	// cancel is a function that cancels
	// all operations
	cancel context.CancelFunc
	// ctx is a context signaling close
	ctx context.Context
	buf *backend.CircularBuffer
}

type Event struct {
	// Type is operation type
	Type types.OpType
	// Item is event Item
	Item Item
}

// Close closes memory backend
func (c *UIResourceCache) Close() error {
	c.cancel()
	c.Lock()
	defer c.Unlock()
	c.buf.Close()
	return nil
}

// Clock returns clock used by this backend
func (c *UIResourceCache) Clock() clockwork.Clock {
	return c.Config.Clock
}

// Create creates item if it does not exist
func (c *UIResourceCache) Create(ctx context.Context, i Item) error {
	if len(i.Key) == 0 {
		return trace.BadParameter("missing parameter key")
	}
	c.Lock()
	defer c.Unlock()
	if c.tree.Has(&btreeItem{Item: i}) {
		return trace.AlreadyExists("key %q already exists", string(i.Key))
	}
	event := Event{
		Type: types.OpPut,
		Item: i,
	}
	event.Item.ID = c.generateID()
	c.processEvent(event)
	// if !c.EventsOff {
	// 	c.buf.Emit(event)
	// }
	return nil
}

// Get returns a single item or not found error
func (c *UIResourceCache) Get(ctx context.Context, key []byte) (*Item, error) {
	if len(key) == 0 {
		return nil, trace.BadParameter("missing parameter key")
	}
	c.Lock()
	defer c.Unlock()
	i, found := c.tree.Get(&btreeItem{Item: Item{Key: key}})
	if !found {
		return nil, trace.NotFound("key %q is not found", string(key))
	}
	return &i.Item, nil
}

// Update updates item if it exists, or returns NotFound error
func (c *UIResourceCache) Update(ctx context.Context, i Item) error {
	if len(i.Key) == 0 {
		return trace.BadParameter("missing parameter key")
	}
	c.Lock()
	defer c.Unlock()
	if !c.tree.Has(&btreeItem{Item: i}) {
		return trace.NotFound("key %q is not found", string(i.Key))
	}
	event := Event{
		Type: types.OpPut,
		Item: i,
	}
	event.Item.ID = c.generateID()
	c.processEvent(event)
	// if !m.EventsOff {
	// 	m.buf.Emit(event)
	// }
	return nil
}

// Put puts value into backend (creates if it does not
// exist, updates it otherwise)
func (c *UIResourceCache) Put(ctx context.Context, i Item) error {
	if len(i.Key) == 0 {
		return trace.BadParameter("missing parameter key")
	}
	c.Lock()
	defer c.Unlock()
	event := Event{
		Type: types.OpPut,
		Item: i,
	}
	event.Item.ID = c.generateID()
	c.processEvent(event)
	return nil
}

// PutRange puts range of items into backend (creates if items do not
// exist, updates it otherwise)
func (c *UIResourceCache) PutRange(ctx context.Context, items []Item) error {
	for i := range items {
		if items[i].Key == nil {
			return trace.BadParameter("missing parameter key in item %v", i)
		}
	}
	c.Lock()
	defer c.Unlock()
	for _, item := range items {
		event := Event{
			Type: types.OpPut,
			Item: item,
		}
		event.Item.ID = c.generateID()
		c.processEvent(event)
	}
	return nil
}

// Delete deletes item by key, returns NotFound error
// if item does not exist
func (c *UIResourceCache) Delete(ctx context.Context, key []byte) error {
	if len(key) == 0 {
		return trace.BadParameter("missing parameter key")
	}
	c.Lock()
	defer c.Unlock()
	if !c.tree.Has(&btreeItem{Item: Item{Key: key}}) {
		return trace.NotFound("key %q is not found", string(key))
	}
	event := Event{
		Type: types.OpDelete,
		Item: Item{
			Key: key,
		},
	}
	c.processEvent(event)
	// if !m.EventsOff {
	// 	m.buf.Emit(event)
	// }
	return nil
}

// DeleteRange deletes range of items with keys between startKey and endKey
// Note that elements deleted by range do not produce any events
func (c *UIResourceCache) DeleteRange(ctx context.Context, startKey, endKey []byte) error {
	if len(startKey) == 0 {
		return trace.BadParameter("missing parameter startKey")
	}
	if len(endKey) == 0 {
		return trace.BadParameter("missing parameter endKey")
	}
	c.Lock()
	defer c.Unlock()
	re := c.getRange(ctx, startKey, endKey, backend.NoLimit)
	for _, item := range re.Items {
		event := Event{
			Type: types.OpDelete,
			Item: item,
		}
		c.processEvent(event)
		// if !m.EventsOff {
		// 	m.buf.Emit(event)
		// }
	}
	return nil
}

// GetRange returns query range
func (c *UIResourceCache) GetRange(ctx context.Context, startKey []byte, endKey []byte, limit int) (*GetResult, error) {
	if len(startKey) == 0 {
		return nil, trace.BadParameter("missing parameter startKey")
	}
	if len(endKey) == 0 {
		return nil, trace.BadParameter("missing parameter endKey")
	}
	if limit <= 0 {
		limit = backend.DefaultRangeLimit
	}
	c.Lock()
	defer c.Unlock()
	re := c.getRange(ctx, startKey, endKey, limit)
	if len(re.Items) == backend.DefaultRangeLimit {
		c.Warnf("Range query hit backend limit. (this is a bug!) startKey=%q,limit=%d", startKey, backend.DefaultRangeLimit)
	}
	return &re, nil
}

// CompareAndSwap compares item with existing item and replaces it with replaceWith item
func (c *UIResourceCache) CompareAndSwap(ctx context.Context, expected Item, replaceWith Item) error {
	if len(expected.Key) == 0 {
		return trace.BadParameter("missing parameter Key")
	}
	if len(replaceWith.Key) == 0 {
		return trace.BadParameter("missing parameter Key")
	}
	if !bytes.Equal(expected.Key, replaceWith.Key) {
		return trace.BadParameter("expected and replaceWith keys should match")
	}
	c.Lock()
	defer c.Unlock()
	i, found := c.tree.Get(&btreeItem{Item: expected})
	if !found {
		return trace.CompareFailed("key %q is not found", string(expected.Key))
	}
	existingItem := i.Item
	// we wont have strings forever
	if existingItem.Value != expected.Value {
		return trace.CompareFailed("current value does not match expected for %v", string(expected.Key))
	}
	event := Event{
		Type: types.OpPut,
		Item: replaceWith,
	}
	c.processEvent(event)
	// if !m.EventsOff {
	// 	m.buf.Emit(event)
	// }
	return nil
}

type GetResult struct {
	Items []Item
}

func (c *UIResourceCache) getRange(ctx context.Context, startKey, endKey []byte, limit int) GetResult {
	var res GetResult
	c.tree.AscendRange(&btreeItem{Item: Item{Key: startKey}}, &btreeItem{Item: Item{Key: endKey}}, func(item *btreeItem) bool {
		res.Items = append(res.Items, item.Item)
		if limit > 0 && len(res.Items) >= limit {
			return false
		}
		return true
	})
	return res
}

func (c *UIResourceCache) processEvent(event Event) {
	switch event.Type {
	case types.OpPut:
		item := &btreeItem{Item: event.Item, index: -1}
		c.tree.ReplaceOrInsert(item)
	case types.OpDelete:
		item, found := c.tree.Get(&btreeItem{Item: event.Item})
		if !found {
			return
		}
		c.tree.Delete(item)
	default:
		// skip unsupported record
	}
}

type DatabaseServersGetter interface {
	GetDatabaseServers(context.Context, string, ...MarshalOption) ([]types.DatabaseServer, error)
}
type UIResourceWatcherConfig struct {
	ResourceWatcherConfig
	NodesGetter
	DatabaseServersGetter
}

type UIResourceWatcher struct {
	*resourceWatcher
	*uiResourceCollector
}

func (u *UIResourceWatcher) GetUIResources(ctx context.Context, namespace string) ([]types.ResourceWithLabels, error) {
	result, err := u.current.GetRange(ctx, backend.Key(prefix), backend.RangeEnd(backend.Key(prefix)), backend.NoLimit)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	var resources []types.ResourceWithLabels

	for _, item := range result.Items {
		resources = append(resources, item.Value)
	}

	return resources, nil
}

type uiResourceCollector struct {
	UIResourceWatcherConfig
	current         *UIResourceCache
	lock            sync.RWMutex
	initializationC chan struct{}
	once            sync.Once
}

func NewUIResourceWatcher(ctx context.Context, cfg UIResourceWatcherConfig) (*UIResourceWatcher, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	mem, err := NewUIResourceCache(Config{
		Mirror: true,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	collector := &uiResourceCollector{
		UIResourceWatcherConfig: cfg,
		current:                 mem,
		initializationC:         make(chan struct{}),
	}

	watcher, err := newResourceWatcher(ctx, collector, cfg.ResourceWatcherConfig)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &UIResourceWatcher{
		resourceWatcher:     watcher,
		uiResourceCollector: collector,
	}, nil
}

func (u *uiResourceCollector) getResourcesAndUpdateCurrent(ctx context.Context) error {
	err := u.getAndUpdateNodes(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	err = u.getAndUpdateDatabases(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (u *uiResourceCollector) getAndUpdateNodes(ctx context.Context) error {
	newNodes, err := u.NodesGetter.GetNodes(ctx, apidefaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}
	nodes := make([]Item, 0)
	for _, node := range newNodes {
		nodes = append(nodes, Item{
			Key:   backend.Key(prefix, node.GetNamespace(), node.GetName(), types.KindNode),
			Value: node,
			ID:    u.current.generateID(),
		})
	}
	return u.current.PutRange(ctx, nodes)
}

func (u *uiResourceCollector) getAndUpdateDatabases(ctx context.Context) error {
	newDbs, err := u.DatabaseServersGetter.GetDatabaseServers(ctx, apidefaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}
	dbs := make([]Item, 0)
	for _, db := range newDbs {
		dbs = append(dbs, Item{
			Key:   backend.Key(prefix, db.GetNamespace(), db.GetName(), types.KindDatabaseServer),
			Value: db,
			ID:    u.current.generateID(),
		})
	}
	return u.current.PutRange(ctx, dbs)
}

func (c *UIResourceCache) generateID() int64 {
	return atomic.AddInt64(&c.nextID, 1)
}

func (u *uiResourceCollector) notifyStale() {}

func (u *uiResourceCollector) initializationChan() <-chan struct{} {
	return u.initializationC
}

func (u *uiResourceCollector) processEventAndUpdateCurrent(ctx context.Context, event types.Event) {
	if event.Resource == nil {
		u.Log.Warnf("Unexpected event: %v.", event)
		return
	}

	u.lock.Lock()
	defer u.lock.Unlock()
	switch event.Type {
	case types.OpDelete:
		u.current.Delete(ctx, backend.Key(prefix, event.Resource.GetMetadata().Namespace, event.Resource.GetName(), event.Resource.GetKind()))
	case types.OpPut:
		u.current.Put(ctx, Item{
			Key:   backend.Key(prefix, event.Resource.GetMetadata().Namespace, event.Resource.GetName(), event.Resource.GetKind()),
			Value: event.Resource.(types.ResourceWithLabels),
			ID:    u.current.generateID(),
		})
	default:
		u.Log.Warnf("unsupported event type %s.", event.Type)
		return
	}
}

func (u *uiResourceCollector) resourceKind() string {
	return types.KindNode
}

const (
	separator = "/"
	prefix    = "ui_resource"
)
