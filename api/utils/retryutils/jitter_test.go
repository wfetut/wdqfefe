/*
Copyright 2021-2022 Gravitational, Inc.

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

package retryutils

import (
	"fmt"
	"testing"
	"time"

	"github.com/gravitational/trace"
	"github.com/stretchr/testify/require"
)

func TestNewJitterBadParameter(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		n         time.Duration
		assertErr require.ErrorAssertionFunc
	}{
		{
			n: -1,
			assertErr: func(t require.TestingT, err error, i ...interface{}) {
				require.True(t, trace.IsBadParameter(err), err)
			},
		},
		{
			n: 0,
			assertErr: func(t require.TestingT, err error, i ...interface{}) {
				require.True(t, trace.IsBadParameter(err), err)
			},
		},
		{
			n:         1,
			assertErr: require.NoError,
		},
		{
			n:         7,
			assertErr: require.NoError,
		},
	} {
		t.Run(fmt.Sprintf("n=%v", tc.n), func(t *testing.T) {
			_, err := newJitter(tc.n, nil)
			tc.assertErr(t, err)
		})
	}
}

func TestNewJitter(t *testing.T) {
	t.Parallel()

	baseDuration := time.Second
	mockInt63nFloor := mockInt63n(func(n int64) int64 { return 0 })
	mockInt63nCeiling := mockInt63n(func(n int64) int64 { return n - 1 })

	for _, tc := range []struct {
		desc          string
		n             time.Duration
		expectFloor   time.Duration
		expectCeiling time.Duration
	}{
		{
			desc:          "FullJitter",
			n:             1,
			expectFloor:   0,
			expectCeiling: baseDuration - 1,
		},
		{
			desc:          "HalfJitter",
			n:             2,
			expectFloor:   baseDuration / 2,
			expectCeiling: baseDuration - 1,
		},
		{
			desc:          "SeventhJitter",
			n:             7,
			expectFloor:   baseDuration * 6 / 7,
			expectCeiling: baseDuration - 1,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			testFloorJitter, err := newJitter(tc.n, mockInt63nFloor)
			require.NoError(t, err)
			require.Equal(t, tc.expectFloor, testFloorJitter(baseDuration))

			testCeilingJitter, err := newJitter(tc.n, mockInt63nCeiling)
			require.NoError(t, err)
			require.Equal(t, tc.expectCeiling, testCeilingJitter(baseDuration))
		})
	}
}

type mockInt63n func(n int64) int64

func (m mockInt63n) Int63n(n int64) int64 {
	return m(n)
}
