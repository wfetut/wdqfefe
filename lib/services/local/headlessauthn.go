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
	"time"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/utils"
)

// CreateHeadlessAuthentication validates and creates a headless authentication in the backend if one with
// the same name does not already exist, else it returns an error.
func (s *IdentityService) CreateHeadlessAuthentication(ctx context.Context, headlessAuthn *types.HeadlessAuthentication) (*types.HeadlessAuthentication, error) {
	if err := headlessAuthn.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	item, err := marshalToItem(headlessAuthn)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	_, err = s.Create(ctx, *item)
	return headlessAuthn, trace.Wrap(err)
}

// UpsertHeadlessAuthentication validates and upserts a headless authentication in the backend.
func (s *IdentityService) UpsertHeadlessAuthentication(ctx context.Context, headlessAuthn *types.HeadlessAuthentication) (*types.HeadlessAuthentication, error) {
	if err := headlessAuthn.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	item, err := marshalToItem(headlessAuthn)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	_, err = s.Put(ctx, *item)
	return headlessAuthn, trace.Wrap(err)
}

// GetHeadlessAuthentication returns a headless authentication from the backend by name.
func (s *IdentityService) GetHeadlessAuthentication(ctx context.Context, name string) (*types.HeadlessAuthentication, error) {
	item, err := s.Get(ctx, headlessAuthenticationKey(name))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	headlessAuthn, err := unmarshalFromItem(item)
	return headlessAuthn, trace.Wrap(err)
}

func marshalToItem(headlessAuthn *types.HeadlessAuthentication) (*backend.Item, error) {
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

func unmarshalFromItem(item *backend.Item) (*types.HeadlessAuthentication, error) {
	var headlessAuthn types.HeadlessAuthentication
	if err := utils.FastUnmarshal(item.Value, &headlessAuthn); err != nil {
		return nil, trace.Wrap(err, "error unmarshalling headless authentication from storage")
	}

	expires := item.Expires
	if !expires.IsZero() {
		headlessAuthn.Metadata.Expires = &expires
	}

	return &headlessAuthn, nil
}

func headlessAuthenticationKey(name string) []byte {
	return backend.Key("headless_authentication", name)
}
