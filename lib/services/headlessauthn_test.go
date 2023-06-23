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

package services_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
)

// TestValidateHeadlessAuthentication tests headless authentication validation logic.
func TestValidateHeadlessAuthentication(t *testing.T) {
	t.Parallel()

	pubUUID := services.NewHeadlessAuthenticationID([]byte(sshPubKey))
	expires := time.Now().Add(time.Minute)

	expectBadParameter := func(tt require.TestingT, err error, i ...interface{}) {
		require.True(t, trace.IsBadParameter(err), "expected bad parameter error but got: %v", err)
	}

	tests := []struct {
		name      string
		ha        *types.HeadlessAuthentication
		assertErr require.ErrorAssertionFunc
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
				State:     types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
			assertErr: require.NoError,
		}, {
			name: "NOK name missing",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Expires: &expires,
					},
				},
				State:     types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
			assertErr: expectBadParameter,
		}, {
			name: "NOK expires missing",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: pubUUID,
					},
				},
				State:     types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
			assertErr: expectBadParameter,
		}, {
			name: "NOK username missing",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name:    pubUUID,
						Expires: &expires,
					},
				},
				State:     types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				PublicKey: []byte(sshPubKey),
			},
			assertErr: expectBadParameter,
		}, {
			name: "NOK public key missing",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name:    uuid.NewString(),
						Expires: &expires,
					},
				},
				State: types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				User:  "user",
			},
			assertErr: expectBadParameter,
		}, {
			name: "NOK name not derived from public key",
			ha: &types.HeadlessAuthentication{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name:    uuid.NewString(),
						Expires: &expires,
					},
				},
				State:     types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_PENDING,
				User:      "user",
				PublicKey: []byte(sshPubKey),
			},
			assertErr: expectBadParameter,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := services.ValidateHeadlessAuthentication(test.ha)
			test.assertErr(t, err)
		})
	}
}

// sshPubKey is a randomly-generated public key used for login tests.
const sshPubKey = `ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBGv+gN2C23P08ieJRA9gU/Ik4bsOh3Kw193UYscJDw41mATj+Kqyf45Rmj8F8rs3i7mYKRXXu1IjNRBzNgpXxqc=`
