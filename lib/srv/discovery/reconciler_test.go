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

package discovery

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
)

func TestGetUpsertBatchSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		queueLen          int
		lastBatchSize     int
		expectedBatchSize int
	}{
		{
			name:              "small batches",
			queueLen:          100,
			lastBatchSize:     0,
			expectedBatchSize: defaults.MinBatchSize,
		},
		{
			name:              "continue previous batch size",
			queueLen:          100,
			lastBatchSize:     20,
			expectedBatchSize: 20,
		},
		{
			name:              "large batches",
			queueLen:          10000,
			lastBatchSize:     0,
			expectedBatchSize: 12,
		},
		{
			name:              "larger batch than previous",
			queueLen:          10000,
			lastBatchSize:     10,
			expectedBatchSize: 12,
		},
		{
			name:              "last batch larger than queue size",
			queueLen:          10,
			lastBatchSize:     15,
			expectedBatchSize: 10,
		},
		{
			name:              "short queue",
			queueLen:          3,
			lastBatchSize:     0,
			expectedBatchSize: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expectedBatchSize, getUpsertBatchSize(tc.queueLen, tc.lastBatchSize))
		})
	}
}

func generateServerInfos(t *testing.T, n int) []types.ServerInfo {
	serverInfos := make([]types.ServerInfo, 0, n)
	for i := 0; i < n; i++ {
		si, err := types.NewServerInfo(types.Metadata{
			Name: fmt.Sprintf("instance-%d", i),
		}, types.ServerInfoSpecV1{})
		require.NoError(t, err)
		serverInfos = append(serverInfos, si)
	}
	return serverInfos
}

func initLabelReconcilerForTests(t *testing.T) (*labelReconciler, clockwork.FakeClock, *fakeAccessPoint) {
	clock := clockwork.NewFakeClock()
	ap := &fakeAccessPoint{}
	lr, err := newLabelReconciler(&labelReconcilerConfig{
		clock:       clock,
		accessPoint: ap,
	})
	require.NoError(t, err)

	return lr, clock, ap
}

func TestLabelReconciler(t *testing.T) {
	t.Parallel()
	lr, clock, ap := initLabelReconcilerForTests(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	go lr.run(ctx)

	serverInfos := generateServerInfos(t, 100)
	lr.queueServerInfos(serverInfos)
	b := defaults.MinBatchSize

	for i := 0; i < 20; i++ {
		clock.Advance(time.Second)
		// TODO(atburke): figure out scope(?) issue that prevents using require.Eventually
		for j := 0; j < 10; j++ {
			if len(ap.upsertedServerInfos) == b {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		require.Len(t, ap.upsertedServerInfos, b)

		require.Equal(t, serverInfos[b*i:b*(i+1)], ap.upsertedServerInfos)
		ap.upsertedServerInfos = []types.ServerInfo{}
	}
}
