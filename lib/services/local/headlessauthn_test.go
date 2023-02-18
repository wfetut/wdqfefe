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

package local_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/services/local"
)

func TestIdentityService_CompareAndSwapHeadlessAuthentication(t *testing.T) {
	t.Parallel()
	identity := newIdentityService(t, clockwork.NewFakeClock())

	pubUUID := services.NewHeadlessAuthenticationID([]byte(sshPubKey))
	expires := time.Now().Add(time.Minute)

	tests := []struct {
		name            string
		ha              *types.HeadlessAuthentication
		assertUpsertErr require.ErrorAssertionFunc
	}{
		{
			name: "OK headless authentication",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name:    pubUUID,
						Expires: &expires,
					},
				},
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
		}, {
			name: "NOK name not derived from public key",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name:    uuid.NewString(),
						Expires: &expires,
					},
				},
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
			assertUpsertErr: func(tt require.TestingT, err error, i ...interface{}) {
				require.True(t, trace.IsBadParameter(err))
			},
		},
	}

	ctx := context.Background()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub, err := identity.CreateHeadlessAuthenticationStub(ctx, test.ha.Metadata.Name)
			require.NoError(t, err)

			swapped, err := identity.CompareAndSwapHeadlessAuthentication(ctx, stub, test.ha)
			if test.assertUpsertErr != nil {
				test.assertUpsertErr(t, err)
				return
			}
			require.NoError(t, err)

			retrieved, err := identity.GetHeadlessAuthentication(ctx, test.ha.Metadata.Name)
			require.NoError(t, err)
			require.Equal(t, swapped, retrieved)
		})
	}
}

func TestIdentityService_HeadlessAuthenticationWatcher(t *testing.T) {
	t.Parallel()
	identity := newIdentityService(t, clockwork.NewFakeClock())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := local.NewHeadlessAuthenticationWatcher(ctx, identity.Backend)
	require.NoError(t, err)

	pubUUID := services.NewHeadlessAuthenticationID([]byte(sshPubKey))

	// Test context cancellation
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Millisecond*100)
	defer waitCancel()

	_, err = w.Wait(waitCtx, pubUUID, func(ha *types.HeadlessAuthentication) (bool, error) { return true, nil })
	require.Error(t, err)
	require.Equal(t, waitCtx.Err(), err)

	// Test waiting for stub creation.
	waitCtx, waitCancel = context.WithTimeout(ctx, time.Millisecond*100)
	defer waitCancel()
	headlessAuthnCh := make(chan *types.HeadlessAuthentication)
	go func() {
		headlessAuthn, err := w.Wait(waitCtx, pubUUID, func(ha *types.HeadlessAuthentication) (bool, error) { return true, nil })
		assert.NoError(t, err)
		headlessAuthnCh <- headlessAuthn
	}()
	require.Eventually(t, func() bool { return w.CheckWaiter(pubUUID) }, time.Millisecond*100, time.Millisecond*10)

	stub, err := identity.CreateHeadlessAuthenticationStub(ctx, pubUUID)
	require.NoError(t, err)
	require.Equal(t, stub, <-headlessAuthnCh)

	// Test waiting for compare and swap.
	waitCtx, waitCancel = context.WithTimeout(ctx, time.Millisecond*100)
	defer waitCancel()
	go func() {
		headlessAuthn, err := w.Wait(waitCtx, pubUUID, func(ha *types.HeadlessAuthentication) (bool, error) {
			return ha.State == types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED, nil
		})
		assert.NoError(t, err)
		headlessAuthnCh <- headlessAuthn
	}()
	require.Eventually(t, func() bool { return w.CheckWaiter(pubUUID) }, time.Millisecond*100, time.Millisecond*10)

	replace := *stub
	replace.State = types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED
	replace.PublicKey = []byte(sshPubKey)
	replace.User = "user"

	swapped, err := identity.CompareAndSwapHeadlessAuthentication(ctx, stub, &replace)
	require.NoError(t, err)
	require.Equal(t, swapped, <-headlessAuthnCh)

	// Test watcher close via ctx. waiters should be notified to close.
	newUUID := uuid.NewString()
	waitCtx, waitCancel = context.WithTimeout(ctx, time.Millisecond*100)
	defer waitCancel()
	go func() {
		_, err := w.Wait(ctx, newUUID, func(ha *types.HeadlessAuthentication) (bool, error) { return true, nil })
		assert.Error(t, err)
	}()
	require.Eventually(t, func() bool { return w.CheckWaiter(newUUID) }, time.Millisecond*100, time.Millisecond*10)

	cancel()

	// New waiters should be prevented.
	_, err = w.Wait(ctx, newUUID, func(ha *types.HeadlessAuthentication) (bool, error) { return true, nil })
	require.Error(t, err)
}

// sshPubKey is a randomly-generated public key used for login tests.
//
// The corresponding private key is:
// -----BEGIN PRIVATE KEY-----
// MHcCAQEEIAKuZeB4WL4KAl5cnCrMYBy3kAX9qHt/g6OAbGGd7f3VoAoGCCqGSM49
// AwEHoUQDQgAEa/6A3YLbc/TyJ4lED2BT8iThuw6HcrDX3dRixwkPDjWYBOP4qrJ/
// jlGaPwXyuzeLuZgpFde7UiM1EHM2ClfGpw==
// -----END PRIVATE KEY-----
const sshPubKey = `ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBGv+gN2C23P08ieJRA9gU/Ik4bsOh3Kw193UYscJDw41mATj+Kqyf45Rmj8F8rs3i7mYKRXXu1IjNRBzNgpXxqc=`
