package services

import (
	"bytes"
	"context"
	"fmt"
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
func NewUnifiedResourceCache(cfg Config) (*UnifiedResourceCache, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	ctx, cancel := context.WithCancel(cfg.Context)
	buf := backend.NewCircularBuffer(
		backend.BufferCapacity(cfg.BufferSize),
	)
	buf.SetInit()
	m := &UnifiedResourceCache{
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
		cfg.Component = teleport.ComponentUnifiedResource
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
	Value string
	// ID is a record ID, newer records have newer ids
	ID int64
}

type UnifiedResourceCache struct {
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
func (c *UnifiedResourceCache) Close() error {
	c.cancel()
	c.Lock()
	defer c.Unlock()
	c.buf.Close()
	return nil
}

// Clock returns clock used by this backend
func (c *UnifiedResourceCache) Clock() clockwork.Clock {
	return c.Config.Clock
}

// Create creates item if it does not exist
func (c *UnifiedResourceCache) Create(ctx context.Context, i Item) error {
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
func (c *UnifiedResourceCache) Get(ctx context.Context, key []byte) (*Item, error) {
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
func (c *UnifiedResourceCache) Update(ctx context.Context, i Item) error {
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
func (c *UnifiedResourceCache) Put(ctx context.Context, i Item) error {
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
func (c *UnifiedResourceCache) PutRange(ctx context.Context, items []Item) error {
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
func (c *UnifiedResourceCache) Delete(ctx context.Context, key []byte) error {
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
func (c *UnifiedResourceCache) DeleteRange(ctx context.Context, startKey, endKey []byte) error {
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
func (c *UnifiedResourceCache) GetRange(ctx context.Context, startKey []byte, endKey []byte, limit int) (*GetResult, error) {
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
func (c *UnifiedResourceCache) CompareAndSwap(ctx context.Context, expected Item, replaceWith Item) error {
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

func (c *UnifiedResourceCache) getRange(ctx context.Context, startKey, endKey []byte, limit int) GetResult {
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

func (c *UnifiedResourceCache) processEvent(event Event) {
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

type UnifiedResourceWatcherConfig struct {
	ResourceWatcherConfig
	NodesGetter
	DatabaseGetter
}

type UnifiedResourceWatcher struct {
	*resourceWatcher
	*unifiedResourceCollector
}

type unifiedResourceCollector struct {
	UnifiedResourceWatcherConfig
	current         *UnifiedResourceCache
	lock            sync.RWMutex
	initializationC chan struct{}
	once            sync.Once
}

func NewUnifiedResourceWatcher(ctx context.Context, cfg UnifiedResourceWatcherConfig) (*UnifiedResourceWatcher, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	mem, err := NewUnifiedResourceCache(Config{
		Mirror: true,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	collector := &unifiedResourceCollector{
		UnifiedResourceWatcherConfig: cfg,
		current:                      mem,
		initializationC:              make(chan struct{}),
	}

	watcher, err := newResourceWatcher(ctx, collector, cfg.ResourceWatcherConfig)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &UnifiedResourceWatcher{
		resourceWatcher:          watcher,
		unifiedResourceCollector: collector,
	}, nil
}

func (u *unifiedResourceCollector) getResourcesAndUpdateCurrent(ctx context.Context) error {
	newNodes, err := u.NodesGetter.GetNodes(ctx, apidefaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}

	nodes := make([]Item, 0)
	for _, node := range newNodes {
		nodes = append(nodes, Item{
			Key:   []byte(node.GetName() + "/" + node.GetKind()),
			Value: node.GetName(),
			ID:    u.current.generateID(),
		})
	}
	return u.current.PutRange(ctx, nodes)
}

func (c *UnifiedResourceCache) generateID() int64 {
	return atomic.AddInt64(&c.nextID, 1)
}

func (u *unifiedResourceCollector) notifyStale() {}

func (u *unifiedResourceCollector) initializationChan() <-chan struct{} {
	return u.initializationC
}

func (u *unifiedResourceCollector) processEventAndUpdateCurrent(ctx context.Context, event types.Event) {
	// TESTING VALUE DOESN'T
	fmt.Println("---------")
	item, err := u.current.Get(ctx, []byte("zidane/"+types.KindNode))
	if err != nil {
		fmt.Printf("err: %+v\n", err)
	}
	fmt.Printf("%+v\n", item)
	fmt.Println("---------")
}

func (u *unifiedResourceCollector) resourceKind() string {
	return types.KindNode
}
