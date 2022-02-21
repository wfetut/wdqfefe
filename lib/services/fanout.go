/*
Copyright 2020 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package services

import (
	"context"
	"sync"
	"unsafe"

	"sync/atomic"

	"github.com/gravitational/teleport/api/types"

	"github.com/gravitational/trace"
)

const defaultQueueSize = 64

type fanoutEntry struct {
	kind    types.WatchKind
	watcher *fanoutWatcher
	closed  bool
}

// Fanout is a helper which allows a stream of events to be fanned-out to many
// watchers. Used by the cache layer to forward events.
type Fanout struct {
	mu           sync.RWMutex
	init, closed bool
	watchers     map[string]*watcherList
	// eventsCh is used in tests
	eventsCh chan FanoutEvent
}

// NewFanout creates a new Fanout instance in an uninitialized
// state.  Until initialized, watchers will be queued but no
// events will be sent.
func NewFanout(eventsCh ...chan FanoutEvent) *Fanout {
	f := &Fanout{
		watchers: make(map[string]*watcherList),
	}
	if len(eventsCh) != 0 {
		f.eventsCh = eventsCh[0]
	}
	return f
}

const (
	// EventWatcherRemoved is emitted when event watcher has been removed
	EventWatcherRemoved = iota
)

// FanoutEvent is used in tests
type FanoutEvent struct {
	// Kind is event kind
	Kind int
}

// NewWatcher attaches a new watcher to this fanout instance.
func (f *Fanout) NewWatcher(ctx context.Context, watch types.Watch) (types.Watcher, error) {
	f.mu.RLock()

	if f.closed {
		f.mu.RUnlock()
		return nil, trace.Errorf("cannot register watcher, fanout system closed")
	}

	w, err := newFanoutWatcher(ctx, f, watch)
	if err != nil {
		f.mu.RUnlock()
		return nil, trace.Wrap(err)
	}

	if f.init {
		// fanout is already initialized; emit init event immediately.
		if !w.init() {
			w.cancel()
			f.mu.RUnlock()
			return nil, trace.BadParameter("failed to send init event")
		}
	}

	f.addWatcher(w)
	return w, nil
}

// SetInit sets Fanout into an initialized state, sending OpInit events
// to any watchers which were added prior to initialization.
func (f *Fanout) SetInit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.init {
		return
	}
	for _, list := range f.watchers {
		var remove []*fanoutWatcher
		list.iter(func(entry *fanoutEntry) {
			if !entry.watcher.init() {
				remove = append(remove, entry.watcher)
			}
		})
		for _, w := range remove {
			f.removeWatcher(w)
			w.cancel()
		}
	}
	f.init = true
}

func filterEventSecrets(event types.Event) types.Event {
	r, ok := event.Resource.(types.ResourceWithSecrets)
	if !ok {
		return event
	}
	event.Resource = r.WithoutSecrets()
	return event
}

// Len returns a total count of watchers
func (f *Fanout) Len() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var count int
	for key := range f.watchers {
		count += f.watchers[key].len()
	}
	return count
}

func (f *Fanout) trySendEvent(e FanoutEvent) {
	if f.eventsCh == nil {
		return
	}
	select {
	case f.eventsCh <- e:
	default:
	}
}

// Emit broadcasts events to all matching watchers that have been attached
// to this fanout instance.
func (f *Fanout) Emit(events ...types.Event) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if !f.init {
		panic("Emit called on uninitialized fanout instance")
	}
	for _, fullEvent := range events {
		// by default, we operate on a version of the event which
		// has had secrets filtered out.
		event := filterEventSecrets(fullEvent)
		var remove []*fanoutWatcher
		// If the event has no associated resource, emit it to all watchers.
		if event.Resource == nil {
			for _, list := range f.watchers {
				list.iter(func(entry *fanoutEntry) {
					if err := entry.watcher.emit(event); err != nil {
						entry.watcher.setError(err)
						remove = append(remove, entry.watcher)
					}
				})
			}
		} else {
			list := f.watchers[event.Resource.GetKind()]
			if list == nil {
				continue
			}

			list.iter(func(entry *fanoutEntry) {
				match, err := entry.kind.Matches(event)
				if err != nil {
					entry.watcher.setError(err)
					remove = append(remove, entry.watcher)
					return
				}
				if !match {
					return
				}
				emitEvent := event
				// if this entry loads secrets, emit the
				// full unfiltered event.
				if entry.kind.LoadSecrets {
					emitEvent = fullEvent
				}
				if err := entry.watcher.emit(emitEvent); err != nil {
					entry.watcher.setError(err)
					remove = append(remove, entry.watcher)
				}
			})
		}

		go func() {
			f.mu.Lock()
			defer f.mu.Unlock()

			for _, w := range remove {
				f.removeWatcher(w)
				w.cancel()
			}
		}()
	}
}

// Reset closes all attached watchers and places the fanout instance
// into an uninitialized state.  Reset may be called on an uninitialized
// fanout instance to remove "queued" watchers.
func (f *Fanout) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeWatchersAsync()
	f.init = false
}

// Close permanently closes the fanout.  Existing watchers will be
// closed and no new watchers will be added.
func (f *Fanout) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeWatchersAsync()
	f.closed = true
}

// closeWatchersAsync moves ownership of the watcher mapping to a background goroutine
// for asynchronous cancellation and sets up a new empty mapping.
func (f *Fanout) closeWatchersAsync() {
	watchersToClose := f.watchers
	f.watchers = make(map[string]*watcherList)
	// goroutines run with a "happens after" relationship to the
	// expressions that create them.  since we move ownership of the
	// old watcher mapping prior to spawning this goroutine, we are
	// "safe" to modify it without worrying about locking.  because
	// we don't continue to hold the lock in the foreground goroutine,
	// this fanout instance may permit new events/registrations/inits/resets
	// while the old watchers are still being closed.  this is fine, since
	// the aformentioned move guarantees that these old watchers aren't
	// going to observe any of the new state transitions.
	go func() {
		for _, list := range watchersToClose {
			list.iter(func(entry *fanoutEntry) {
				entry.watcher.cancel()
			})
		}
	}()
}

func (f *Fanout) addWatcher(w *fanoutWatcher) {
	upgraded := false
	defer func() {
		if upgraded {
			f.mu.Unlock()
		} else {
			f.mu.RUnlock()
		}
	}()

	for _, kind := range w.watch.Kinds {
		list := f.watchers[kind.Kind]
		if list == nil {
			if !upgraded {
				f.mu.RUnlock()
				f.mu.Lock()
				upgraded = true
			}

			list = newWatcherList()
			f.watchers[kind.Kind] = list
		}

		list.add(fanoutEntry{
			kind:    kind,
			watcher: w,
		})
	}
}

func (f *Fanout) removeWatcherWithLock(w *fanoutWatcher) {
	if w == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeWatcher(w)
}

func (f *Fanout) removeWatcher(w *fanoutWatcher) {
	for _, kind := range w.watch.Kinds {
		list := f.watchers[kind.Kind]

		if list != nil {
			if list.remove(w) {
				f.trySendEvent(FanoutEvent{Kind: EventWatcherRemoved})
			}

			if list.len() == 0 {
				delete(f.watchers, kind.Kind)
			}
		}
	}
}

func newFanoutWatcher(ctx context.Context, f *Fanout, watch types.Watch) (*fanoutWatcher, error) {
	if len(watch.Kinds) < 1 {
		return nil, trace.BadParameter("must specify at least one resource kind to watch")
	}
	ctx, cancel := context.WithCancel(ctx)
	if watch.QueueSize < 1 {
		watch.QueueSize = defaultQueueSize
	}
	return &fanoutWatcher{
		fanout: f,
		watch:  watch,
		eventC: make(chan types.Event, watch.QueueSize),
		cancel: cancel,
		ctx:    ctx,
	}, nil
}

type fanoutWatcher struct {
	emux     sync.Mutex
	fanout   *Fanout
	err      error
	watch    types.Watch
	eventC   chan types.Event
	cancel   context.CancelFunc
	ctx      context.Context
	initOnce sync.Once
	initOk   bool
}

// init transmits the OpInit event.  safe to double-call.
func (w *fanoutWatcher) init() (ok bool) {
	w.initOnce.Do(func() {
		select {
		case w.eventC <- types.Event{Type: types.OpInit}:
			w.initOk = true
		default:
			w.initOk = false
		}
	})
	return w.initOk
}

func (w *fanoutWatcher) emit(event types.Event) error {
	select {
	case <-w.ctx.Done():
		return trace.Wrap(w.ctx.Err(), "watcher closed")
	case w.eventC <- event:
		return nil
	default:
		return trace.BadParameter("buffer overflow")
	}
}

func (w *fanoutWatcher) Events() <-chan types.Event {
	return w.eventC
}

func (w *fanoutWatcher) Done() <-chan struct{} {
	return w.ctx.Done()
}

func (w *fanoutWatcher) Close() error {
	w.cancel()
	// goroutine is to prevent accidental
	// deadlock, if watcher.Close is called
	// under Fanout mutex
	go w.fanout.removeWatcherWithLock(w)
	return nil
}

func (w *fanoutWatcher) setError(err error) {
	w.emux.Lock()
	defer w.emux.Unlock()
	w.err = err
}

func (w *fanoutWatcher) Error() error {
	w.emux.Lock()
	defer w.emux.Unlock()
	if w.err != nil {
		return w.err
	}
	select {
	case <-w.Done():
		return trace.Errorf("watcher closed")
	default:
		return nil
	}
}

const watcherListNodeSize = 16
const watcherSentinel = watcherListNodeSize + 1

type watcherList struct {
	head   unsafe.Pointer
	length int32
}

func newWatcherList() *watcherList {
	return &watcherList{
		head: unsafe.Pointer(newWatcherListNode()),
	}
}

func (l *watcherList) iter(f func(*fanoutEntry)) {
	n := (*watcherListNode)(atomic.LoadPointer(&l.head))

	for {
		if n == nil {
			return
		}

		top := atomic.LoadInt32(&n.init)
		for i := int32(0); i < top; i++ {
			entry := &n.entries[i]
			if !entry.closed {
				f(entry)
			}
		}

		n = n.next
	}
}

func (l *watcherList) len() int {
	return int(atomic.LoadInt32(&l.length))
}

func (l *watcherList) add(w fanoutEntry) {
	for {
		nodePtr := atomic.LoadPointer(&l.head)
		node := (*watcherListNode)(nodePtr)

		if !node.add(w) {
			head := newWatcherListNode()
			head.next = node
			head.setFirst(w)

			if atomic.CompareAndSwapPointer(&l.head, unsafe.Pointer(node), unsafe.Pointer(head)) {
				break
			}

			continue
		}

		break
	}

	atomic.AddInt32(&l.length, 1)
}

func (l *watcherList) remove(w *fanoutWatcher) bool {
	found := false
	l.iter(func(entry *fanoutEntry) {
		if entry.watcher == w {
			found = true
			entry.closed = true
		}
	})

	atomic.AddInt32(&l.length, -1)
	return found
}

type watcherListNode struct {
	entries []fanoutEntry
	slot    int32
	init    int32
	next    *watcherListNode
}

func newWatcherListNode() *watcherListNode {
	return &watcherListNode{
		entries: make([]fanoutEntry, watcherListNodeSize),
	}
}

func (n *watcherListNode) setFirst(w fanoutEntry) {
	n.entries[0] = w
	n.slot = 1
	n.init = 1
}

func (n *watcherListNode) add(w fanoutEntry) bool {
	slot := n.getSlot()
	if slot == watcherSentinel {
		return false
	}

	n.entries[slot] = w
	atomic.AddInt32(&n.init, 1)
	return true
}

func (n *watcherListNode) getSlot() int32 {
	for {
		currentSlot := atomic.LoadInt32(&n.slot)
		if currentSlot == watcherListNodeSize {
			return watcherSentinel
		}

		newSlot := currentSlot + 1
		if atomic.CompareAndSwapInt32(&n.slot, currentSlot, newSlot) {
			return currentSlot
		}
	}
}
