// Copyright 2023 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package common

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func Test_methodCallMetrics(t *testing.T) {
	methodCallCount.Reset()
	methodCallLatency.Reset()

	iter := 13
	for i := 0; i < iter; i++ {
		methodCallMetrics("test", "dummy", 1)()
	}

	t.Run("verify methodCallCount", func(t *testing.T) {
		ch := make(chan prometheus.Metric, 100)
		methodCallCount.Collect(ch)
		obs := <-ch

		var m = &dto.Metric{}
		require.NoError(t, obs.Write(m))

		require.Equal(t, "component", m.Label[0].GetName())
		require.Equal(t, "test", m.Label[0].GetValue())

		require.Equal(t, "engine", m.Label[1].GetName())
		require.Equal(t, "dummy", m.Label[1].GetValue())

		require.Equal(t, "method", m.Label[2].GetName())
		require.Equal(t, "Test_methodCallMetrics", m.Label[2].GetValue())

		require.Equal(t, float64(iter), m.GetCounter().GetValue())
	})

	t.Run("verify methodCallLatency", func(t *testing.T) {
		ch := make(chan prometheus.Metric, 100)
		methodCallLatency.Collect(ch)
		obs := <-ch

		var m = &dto.Metric{}
		require.NoError(t, obs.Write(m))

		require.Equal(t, "component", m.Label[0].GetName())
		require.Equal(t, "test", m.Label[0].GetValue())

		require.Equal(t, "engine", m.Label[1].GetName())
		require.Equal(t, "dummy", m.Label[1].GetValue())

		require.Equal(t, "method", m.Label[2].GetName())
		require.Equal(t, "Test_methodCallMetrics", m.Label[2].GetValue())

		buckets := m.GetHistogram().Bucket
		require.Equal(t, iter, int(*buckets[len(buckets)-1].CumulativeCount))
	})
}
