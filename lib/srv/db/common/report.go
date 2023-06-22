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
	"context"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/lib/observability/metrics"
	"github.com/gravitational/teleport/lib/utils"
)

type reporterConfig struct {
	engine     Engine
	engineName string
	clock      clockwork.Clock
	component  string
}

func (r *reporterConfig) CheckAndSetDefaults() error {
	if r.engine == nil {
		return trace.BadParameter("missing parameter Engine")
	}
	if r.engineName == "" {
		return trace.BadParameter("missing parameter EngineName")
	}
	if r.clock == nil {
		r.clock = clockwork.NewRealClock()
	}
	if r.component == "" {
		r.component = teleport.ComponentDatabase
	}
	return nil
}

type reportingEngine struct {
	reporterConfig
}

func init() {
	_ = metrics.RegisterPrometheusCollectors(prometheusCollectorsEngine...)
}

func NewReportingEngine(cfg reporterConfig) (Engine, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}

	return &reportingEngine{cfg}, nil
}

func (r *reportingEngine) InitializeConnection(clientConn net.Conn, sessionCtx *Session) error {
	closer := utils.NewCloserConn(clientConn)
	trackingClientConn := utils.NewTrackingConn(closer)

	go func() {
		var txPrev, rxPrev uint64

		update := func() {
			tx, rx := trackingClientConn.Stat()
			clientWrittenBytes.WithLabelValues(r.component, r.engineName).Add(float64(tx - txPrev))
			clientReadBytes.WithLabelValues(r.component, r.engineName).Add(float64(rx - rxPrev))
			txPrev = tx
			rxPrev = rx
		}

		for {
			select {
			case <-closer.Context().Done():
				update()
				return
			case <-r.clock.After(time.Second):
				update()
			}
		}
	}()

	connections.WithLabelValues(r.component, r.engineName).Inc()
	return r.engine.InitializeConnection(trackingClientConn, sessionCtx)
}

func (r *reportingEngine) SendError(err error) {
	engineErrors.WithLabelValues(r.component, r.engineName).Inc()
	r.engine.SendError(err)
}

func (r *reportingEngine) HandleConnection(ctx context.Context, session *Session) error {
	activeConnections.WithLabelValues(r.component, r.engineName).Inc()
	defer activeConnections.WithLabelValues(r.component, r.engineName).Dec()

	start := r.clock.Now()
	defer func() {
		connectionDurations.WithLabelValues(r.component, r.engineName).Observe(r.clock.Since(start).Seconds())
	}()

	return trace.Wrap(r.engine.HandleConnection(ctx, session))
}

var _ Engine = &reportingEngine{}

var commonLabels = []string{teleport.ComponentLabel, "engine"}

var (
	clientReadBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_client_read_bytes",
			Help: "Total number bytes read from the DB clients",
		},
		commonLabels,
	)

	clientWrittenBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_client_written_bytes",
			Help: "Total number bytes written to the DB clients",
		},
		commonLabels,
	)

	messagesFromClient = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_messages_from_client",
			Help: "Number of messages (packets) received from the DB client",
		},
		commonLabels,
	)

	messagesFromServer = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_messages_from_server",
			Help: "Number of messages (packets) received from the DB server",
		},
		commonLabels,
	)

	methodCallCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_method_call_count",
		},
		append([]string{"method"}, commonLabels...),
	)

	methodCallLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "db_method_call_latency",
			// lowest bucket start of upper bound 0.001 sec (1 ms) with factor 2
			// highest bucket start of 0.001 sec * 2^15 == 32.768 sec
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
		},
		append([]string{"method"}, commonLabels...),
	)

	connections = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_connections_total",
			Help: "Number of initialized DB connections",
		},
		commonLabels,
	)

	connectionDurations = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "db_connection_durations",
			Help: "Duration of connection",
			// 1ms ... 14.5h
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 20),
		},
		commonLabels,
	)

	activeConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_active_connections",
			Help: "Number of active DB connections",
		},
		commonLabels,
	)

	connectionSetupTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "db_connection_setup_time",
			Help: "Initial time to setup DB connection, before any requests are handled.",
			// 1ms ... 14.5h
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 20),
		},
		commonLabels,
	)

	engineErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "db_errors_total",
			Help: "Number of synthetic DB errors sent to the client",
		},
		commonLabels,
	)

	prometheusCollectorsEngine = []prometheus.Collector{
		clientReadBytes, clientWrittenBytes,
		messagesFromClient, messagesFromServer,
		methodCallCount, methodCallLatency,

		connections, activeConnections, connectionDurations, connectionSetupTime, engineErrors,
	}
)

func methodCallMetrics(component, engine string, skip int) func() {
	start := time.Now()

	name := "<unknown>"
	pc, _, _, ok := runtime.Caller(skip)
	if ok {
		info := runtime.FuncForPC(pc)
		name = info.Name()
	}

	// find the last dot in the method name.
	if last := strings.LastIndexByte(name, '.'); last != -1 {
		// take everything after the last dot.
		name = name[last+1 : len(name)]
	}

	methodCallCount.WithLabelValues(name, component, engine).Inc()
	return func() {
		methodCallLatency.WithLabelValues(name, component, engine).Observe(time.Since(start).Seconds())
	}
}
