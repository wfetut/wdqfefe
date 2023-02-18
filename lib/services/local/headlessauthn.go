/*
Copyright 2023 Gravitational, Inc.

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

package local

import (
	"context"
	"sync"
	"time"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/utils"
)

const maxWaiters = 1024

// HeadlessAuthenticationWatcher is a custom backend watcher for the headless authentication resource.
type HeadlessAuthenticationWatcher struct {
	log         logrus.FieldLogger
	b           backend.Backend
	watchersMux sync.Mutex
	waiters     [maxWaiters]headlessAuthenticationWaiter
	closed      chan struct{}
}

type headlessAuthenticationWaiter struct {
	name string
	ch   chan *types.HeadlessAuthentication
}

// NewHeadlessAuthenticationWatcher creates a new headless login watcher.
func NewHeadlessAuthenticationWatcher(ctx context.Context, b backend.Backend) (*HeadlessAuthenticationWatcher, error) {
	if b == nil {
		return nil, trace.BadParameter("missing required field backend")
	}
	watcher := &HeadlessAuthenticationWatcher{
		log:    logrus.StandardLogger(),
		b:      b,
		closed: make(chan struct{}),
	}

	if err := watcher.start(ctx); err != nil {
		return nil, trace.Wrap(err)
	}

	return watcher, nil
}

func (h *HeadlessAuthenticationWatcher) start(ctx context.Context) error {
	w, err := h.b.NewWatcher(ctx, backend.Watch{
		Prefixes: [][]byte{[]byte(headlessAuthenticationKey(""))},
	})
	if err != nil {
		return trace.Wrap(err)
	}

	go h.processEvents(ctx, w)

	return nil
}

func (h *HeadlessAuthenticationWatcher) close() {
	h.watchersMux.Lock()
	defer h.watchersMux.Unlock()
	close(h.closed)
}

func (h *HeadlessAuthenticationWatcher) processEvents(ctx context.Context, w backend.Watcher) {
	for {
		select {
		case event := <-w.Events():
			headlessAuthn, err := unmarshalHeadlessAuthenticationFromItem(&event.Item)
			if err != nil {
				h.log.WithError(err).Debug("failed to unmarshal headless authentication from event")
			} else {
				h.notify(headlessAuthn)
			}
		case <-ctx.Done():
			h.close()
			return
		}
	}
}

func (h *HeadlessAuthenticationWatcher) notify(headlessAuthn *types.HeadlessAuthentication) {
	h.watchersMux.Lock()
	defer h.watchersMux.Unlock()
	for i := range h.waiters {
		if h.waiters[i].name == headlessAuthn.Metadata.Name {
			select {
			case h.waiters[i].ch <- headlessAuthn:
			default:
			}
		}
	}
}

// Wait watchers for the headless authentication with the given id to be added/updated
// in the backend, and waits for the given condition to be met, to result in an error,
// or for the given context to close.
func (h *HeadlessAuthenticationWatcher) Wait(ctx context.Context, name string, cond func(*types.HeadlessAuthentication) (bool, error)) (*types.HeadlessAuthentication, error) {
	waiter, err := h.assignWaiter(ctx, name)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer h.unassignWaiter(waiter)

	// With the waiter allocated, check if there is an existing entry in the backend.
	currentItem, err := h.b.Get(ctx, headlessAuthenticationKey(name))
	if err == nil {
		headlessAuthn, err := unmarshalHeadlessAuthenticationFromItem(currentItem)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		select {
		case waiter.ch <- headlessAuthn:
		default:
		}
	}

	for {
		select {
		case headlessAuthn := <-waiter.ch:
			if ok, err := cond(headlessAuthn); err != nil {
				return nil, trace.Wrap(err)
			} else if ok {
				return headlessAuthn, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.closed:
			return nil, trace.Errorf("headless authentication watcher closed")
		}
	}
}

// CheckWaiter checks if there is an active waiter matching the given
// headless authentication ID. Used in tests.
func (h *HeadlessAuthenticationWatcher) CheckWaiter(name string) bool {
	h.watchersMux.Lock()
	defer h.watchersMux.Unlock()
	for i := range h.waiters {
		if h.waiters[i].name == name {
			return true
		}
	}
	return false
}

func (h *HeadlessAuthenticationWatcher) assignWaiter(ctx context.Context, name string) (*headlessAuthenticationWaiter, error) {
	h.watchersMux.Lock()
	defer h.watchersMux.Unlock()

	select {
	case <-h.closed:
		return nil, trace.Errorf("headless authentication watcher closed")
	default:
	}

	for i := range h.waiters {
		if h.waiters[i].ch != nil {
			continue
		}
		h.waiters[i].ch = make(chan *types.HeadlessAuthentication, 1)
		h.waiters[i].name = name
		return &h.waiters[i], nil
	}

	return nil, trace.LimitExceeded("too many in-flight headless login requests")
}

func (h *HeadlessAuthenticationWatcher) unassignWaiter(waiter *headlessAuthenticationWaiter) {
	h.watchersMux.Lock()
	defer h.watchersMux.Unlock()
	close(waiter.ch)
	waiter.ch = nil
	waiter.name = ""
}

// CreateHeadlessAuthenticationStub creates a headless authentication stub in the backend.
func (s *IdentityService) CreateHeadlessAuthenticationStub(ctx context.Context, name string) (*types.HeadlessAuthentication, error) {
	expires := s.Clock().Now().Add(time.Minute)
	headlessAuthn := &types.HeadlessAuthentication{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name:    name,
				Expires: &expires,
			},
		},
	}

	item, err := marshalHeadlessAuthenticationToItem(headlessAuthn)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	if _, err = s.Create(ctx, *item); err != nil {
		return nil, trace.Wrap(err)
	}
	return headlessAuthn, nil
}

// CompareAndSwapHeadlessAuthentication validates the new headless authentication and
// performs a compare and swap replacement on a headless authentication resource.
func (s *IdentityService) CompareAndSwapHeadlessAuthentication(ctx context.Context, old, new *types.HeadlessAuthentication) (*types.HeadlessAuthentication, error) {
	if err := services.ValidateHeadlessAuthentication(new); err != nil {
		return nil, trace.Wrap(err)
	}

	oldItem, err := marshalHeadlessAuthenticationToItem(old)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	newItem, err := marshalHeadlessAuthenticationToItem(new)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	_, err = s.CompareAndSwap(ctx, *oldItem, *newItem)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return new, nil
}

// GetHeadlessAuthentication returns a headless authentication from the backend by name.
func (s *IdentityService) GetHeadlessAuthentication(ctx context.Context, name string) (*types.HeadlessAuthentication, error) {
	item, err := s.Get(ctx, headlessAuthenticationKey(name))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	headlessAuthn, err := unmarshalHeadlessAuthenticationFromItem(item)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return headlessAuthn, nil
}

func marshalHeadlessAuthenticationToItem(headlessAuthn *types.HeadlessAuthentication) (*backend.Item, error) {
	if err := headlessAuthn.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	value, err := utils.FastMarshal(headlessAuthn)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	var expires time.Time
	if headlessAuthn.Metadata.Expires != nil {
		expires = *headlessAuthn.Metadata.Expires
	}

	return &backend.Item{
		Key:     headlessAuthenticationKey(headlessAuthn.Metadata.Name),
		Value:   value,
		Expires: expires,
	}, nil
}

func unmarshalHeadlessAuthenticationFromItem(item *backend.Item) (*types.HeadlessAuthentication, error) {
	var headlessAuthn types.HeadlessAuthentication
	if err := utils.FastUnmarshal(item.Value, &headlessAuthn); err != nil {
		return nil, trace.Wrap(err, "error unmarshalling headless authentication from storage")
	}

	expires := item.Expires
	if !expires.IsZero() {
		headlessAuthn.Metadata.Expires = &expires
	}

	if err := headlessAuthn.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	return &headlessAuthn, nil
}

func headlessAuthenticationKey(name string) []byte {
	return backend.Key("headless_authentication", name)
}
