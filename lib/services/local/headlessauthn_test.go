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
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
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

// sshPubKey is a randomly-generated public key used for login tests.
//
// The corresponding private key is:
// -----BEGIN PRIVATE KEY-----
// MHcCAQEEIAKuZeB4WL4KAl5cnCrMYBy3kAX9qHt/g6OAbGGd7f3VoAoGCCqGSM49
// AwEHoUQDQgAEa/6A3YLbc/TyJ4lED2BT8iThuw6HcrDX3dRixwkPDjWYBOP4qrJ/
// jlGaPwXyuzeLuZgpFde7UiM1EHM2ClfGpw==
// -----END PRIVATE KEY-----
const sshPubKey = `ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBGv+gN2C23P08ieJRA9gU/Ik4bsOh3Kw193UYscJDw41mATj+Kqyf45Rmj8F8rs3i7mYKRXXu1IjNRBzNgpXxqc=`
