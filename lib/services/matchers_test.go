/*
Copyright 2021 Gravitational, Inc.

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
	"testing"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/defaults"
)

// TestMatchResourceLabels tests matching a resource against a selector.
func TestMatchResourceLabels(t *testing.T) {
	tests := []struct {
		description    string
		selectors      []ResourceMatcher
		databaseLabels map[string]string
		match          bool
	}{
		{
			description: "wildcard selector matches empty labels",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{types.Wildcard: []string{types.Wildcard}}},
			},
			databaseLabels: nil,
			match:          true,
		},
		{
			description: "wildcard selector matches any label",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{types.Wildcard: []string{types.Wildcard}}},
			},
			databaseLabels: map[string]string{
				uuid.New().String(): uuid.New().String(),
				uuid.New().String(): uuid.New().String(),
			},
			match: true,
		},
		{
			description: "selector doesn't match empty labels",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{"env": []string{"dev"}}},
			},
			databaseLabels: nil,
			match:          false,
		},
		{
			description: "selector matches specific label",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{"env": []string{"dev"}}},
			},
			databaseLabels: map[string]string{"env": "dev"},
			match:          true,
		},
		{
			description: "selector doesn't match label",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{"env": []string{"dev"}}},
			},
			databaseLabels: map[string]string{"env": "prod"},
			match:          false,
		},
		{
			description: "selector matches label",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{"env": []string{"dev", "prod"}}},
			},
			databaseLabels: map[string]string{"env": "prod"},
			match:          true,
		},
		{
			description: "selector doesn't match multiple labels",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{
					"env":     []string{"dev"},
					"cluster": []string{"root"},
				}},
			},
			databaseLabels: map[string]string{"cluster": "root"},
			match:          false,
		},
		{
			description: "selector matches multiple labels",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{
					"env":     []string{"dev"},
					"cluster": []string{"root"},
				}},
			},
			databaseLabels: map[string]string{"cluster": "root", "env": "dev"},
			match:          true,
		},
		{
			description: "one of multiple selectors matches",
			selectors: []ResourceMatcher{
				{Labels: types.Labels{"env": []string{"dev"}}},
				{Labels: types.Labels{"cluster": []string{"root"}}},
			},
			databaseLabels: map[string]string{"cluster": "root"},
			match:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			database, err := types.NewDatabaseV3(types.Metadata{
				Name:   "test",
				Labels: test.databaseLabels,
			}, types.DatabaseSpecV3{
				Protocol: defaults.ProtocolPostgres,
				URI:      "localhost:5432",
			})
			require.NoError(t, err)

			require.Equal(t, test.match, MatchResourceLabels(test.selectors, database))
		})
	}
}

func TestMatchResourceByFilters_Helper(t *testing.T) {
	t.Parallel()

	server, err := types.NewServerWithLabels("banana", types.KindNode, types.ServerSpecV2{
		Hostname:    "foo",
		Addr:        "bar",
		PublicAddrs: []string{"foo.example.com:3080"},
	}, map[string]string{"env": "prod", "os": "mac"})
	require.NoError(t, err)

	resource := types.ResourceWithLabels(server)

	testcases := []struct {
		name        string
		filters     MatchResourceFilter
		assertErr   require.ErrorAssertionFunc
		assertMatch require.BoolAssertionFunc
	}{
		{
			name:        "empty filters",
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "all match",
			filters: MatchResourceFilter{
				PredicateExpression: `resource.spec.hostname == "foo"`,
				SearchKeywords:      []string{"banana"},
				Labels:              map[string]string{"os": "mac"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "no match",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "no-match"`,
				SearchKeywords:      []string{"no", "match"},
				Labels:              map[string]string{"no": "match"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "search keywords hostname match",
			filters: MatchResourceFilter{
				SearchKeywords: []string{"foo"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "search keywords addr match",
			filters: MatchResourceFilter{
				SearchKeywords: []string{"bar"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "search keywords public addr match",
			filters: MatchResourceFilter{
				SearchKeywords: []string{"foo.example.com"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "expression match",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "prod" && exists(labels.os)`,
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "no expression match",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "no-match"`,
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "error in expr",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == prod`,
			},
			assertErr:   require.Error,
			assertMatch: require.False,
		},
		{
			name: "label match",
			filters: MatchResourceFilter{
				Labels: map[string]string{"os": "mac"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "no label match",
			filters: MatchResourceFilter{
				Labels: map[string]string{"no": "match"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "search match",
			filters: MatchResourceFilter{
				SearchKeywords: []string{"mac", "env"},
			},
			assertErr:   require.NoError,
			assertMatch: require.True,
		},
		{
			name: "no search match",
			filters: MatchResourceFilter{
				SearchKeywords: []string{"no", "match"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "partial match is no match: search",
			filters: MatchResourceFilter{
				PredicateExpression: `resource.spec.hostname == "foo"`,
				Labels:              map[string]string{"os": "mac"},
				SearchKeywords:      []string{"no", "match"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "partial match is no match: labels",
			filters: MatchResourceFilter{
				PredicateExpression: `resource.spec.hostname == "foo"`,
				Labels:              map[string]string{"no": "match"},
				SearchKeywords:      []string{"mac", "env"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
		{
			name: "partial match is no match: expression",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "no-match"`,
				Labels:              map[string]string{"os": "mac"},
				SearchKeywords:      []string{"mac", "env"},
			},
			assertErr:   require.NoError,
			assertMatch: require.False,
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			match, err := matchResourceByFilters(resource, tc.filters)
			tc.assertErr(t, err)
			tc.assertMatch(t, match)
		})
	}
}

func TestMatchAndFilterKubeClusters(t *testing.T) {
	t.Parallel()

	getKubeServers := func() []types.KubeServer {
		cluster1, err := types.NewKubernetesClusterV3(
			types.Metadata{
				Name:   "cluster-1",
				Labels: map[string]string{"env": "prod", "os": "mac"},
			},
			types.KubernetesClusterSpecV3{},
		)
		require.NoError(t, err)

		cluster2, err := types.NewKubernetesClusterV3(
			types.Metadata{
				Name:   "cluster-2",
				Labels: map[string]string{"env": "staging", "os": "mac"},
			},
			types.KubernetesClusterSpecV3{},
		)
		require.NoError(t, err)
		cluster3, err := types.NewKubernetesClusterV3(
			types.Metadata{
				Name:   "cluster-3",
				Labels: map[string]string{"env": "prod", "os": "mac"},
			},
			types.KubernetesClusterSpecV3{},
		)

		require.NoError(t, err)
		var servers []types.KubeServer
		for _, cluster := range []*types.KubernetesClusterV3{cluster1, cluster2, cluster3} {
			server, err := types.NewKubernetesServerV3FromCluster(cluster, "_", "_")
			require.NoError(t, err)
			servers = append(servers, server)
		}
		return servers
	}

	testcases := []struct {
		name        string
		filters     MatchResourceFilter
		expectedLen int
		assertMatch require.BoolAssertionFunc
	}{
		{
			name:        "empty values",
			expectedLen: 3,
			assertMatch: require.True,
		},
		{
			name:        "all match",
			expectedLen: 3,
			filters: MatchResourceFilter{
				PredicateExpression: `labels.os == "mac"`,
			},
			assertMatch: require.True,
		},
		{
			name:        "some match",
			expectedLen: 2,
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "prod"`,
			},
			assertMatch: require.True,
		},
		{
			name:        "single match",
			expectedLen: 1,
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "staging"`,
			},
			assertMatch: require.True,
		},
		{
			name: "no match",
			filters: MatchResourceFilter{
				PredicateExpression: `labels.env == "no-match"`,
			},
			assertMatch: require.False,
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kubeServers := getKubeServers()
			atLeastOneMatch := false
			matchedServers := make([]types.KubeServer, 0, len(kubeServers))
			for _, kubeServer := range kubeServers {
				match, err := matchAndFilterKubeClusters(types.ResourceWithLabels(kubeServer), tc.filters)
				require.NoError(t, err)
				if match {
					atLeastOneMatch = true
					matchedServers = append(matchedServers, kubeServer)
				}
			}
			tc.assertMatch(t, atLeastOneMatch)

			require.Len(t, matchedServers, tc.expectedLen)
		})
	}
}

// TestMatchResourceByFilters tests supported resource kinds and
// if a resource has contained resources, those contained resources
// are filtered instead.
func TestMatchResourceByFilters(t *testing.T) {
	t.Parallel()

	filterExpression := `resource.metadata.name == "foo"`

	testcases := []struct {
		name           string
		wantNotImplErr bool
		filters        MatchResourceFilter
		resource       func() types.ResourceWithLabels
	}{
		{
			name: "no filter should return true",
			resource: func() types.ResourceWithLabels {
				server, err := types.NewServer("foo", types.KindNode, types.ServerSpecV2{})
				require.NoError(t, err)
				return server
			},
			filters: MatchResourceFilter{ResourceKind: types.KindNode},
		},
		{
			name:     "unsupported resource kind",
			resource: func() types.ResourceWithLabels { return nil },
			filters: MatchResourceFilter{
				ResourceKind:   "unsupported",
				SearchKeywords: []string{"nothing"},
			},
			wantNotImplErr: true,
		},
		{
			name: "app server",
			resource: func() types.ResourceWithLabels {
				appServer, err := types.NewAppServerV3(types.Metadata{
					Name: "_",
				}, types.AppServerSpecV3{
					HostID: "_",
					App: &types.AppV3{
						Metadata: types.Metadata{Name: "foo"},
						Spec:     types.AppSpecV3{URI: "_"},
					},
				})
				require.NoError(t, err)
				return appServer
			},
			filters: MatchResourceFilter{
				ResourceKind:        types.KindAppServer,
				PredicateExpression: filterExpression,
			},
		},
		{
			name: "db server",
			resource: func() types.ResourceWithLabels {
				dbServer, err := types.NewDatabaseServerV3(types.Metadata{
					Name: "_",
				}, types.DatabaseServerSpecV3{
					HostID:   "_",
					Hostname: "_",
					Database: &types.DatabaseV3{
						Metadata: types.Metadata{Name: "foo"},
						Spec: types.DatabaseSpecV3{
							URI:      "_",
							Protocol: "_",
						},
					},
				})
				require.NoError(t, err)
				return dbServer
			},
			filters: MatchResourceFilter{
				ResourceKind:        types.KindDatabaseServer,
				PredicateExpression: filterExpression,
			},
		},

		{
			name: "kube cluster",
			resource: func() types.ResourceWithLabels {
				cluster, err := types.NewKubernetesClusterV3FromLegacyCluster("_", &types.KubernetesCluster{
					Name: "foo",
				})
				require.NoError(t, err)
				return cluster
			},
			filters: MatchResourceFilter{
				ResourceKind:        types.KindKubernetesCluster,
				PredicateExpression: filterExpression,
			},
		},
		{
			name: "node",
			resource: func() types.ResourceWithLabels {
				server, err := types.NewServer("foo", types.KindNode, types.ServerSpecV2{})
				require.NoError(t, err)
				return server
			},
			filters: MatchResourceFilter{
				ResourceKind:        types.KindNode,
				PredicateExpression: filterExpression,
			},
		},
		{
			name: "windows desktop",
			resource: func() types.ResourceWithLabels {
				desktop, err := types.NewWindowsDesktopV3("foo", nil, types.WindowsDesktopSpecV3{Addr: "_"})
				require.NoError(t, err)
				return desktop
			},
			filters: MatchResourceFilter{
				ResourceKind:        types.KindWindowsDesktop,
				PredicateExpression: filterExpression,
			},
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resource := tc.resource()
			match, err := MatchResourceByFilters(resource, tc.filters, nil)

			switch tc.wantNotImplErr {
			case true:
				require.True(t, trace.IsNotImplemented(err))
				require.False(t, match)
			default:
				require.NoError(t, err)
				require.True(t, match)
			}
		})
	}
}

func TestResourceMatchersToTypes(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []ResourceMatcher
		out  []*types.DatabaseResourceMatcher
	}{
		{
			name: "empty",
			in:   []ResourceMatcher{},
			out:  []*types.DatabaseResourceMatcher{},
		},
		{
			name: "single element with single label",
			in: []ResourceMatcher{
				{Labels: types.Labels{"elem1": []string{"elem1"}}},
			},
			out: []*types.DatabaseResourceMatcher{
				{Labels: &types.Labels{"elem1": []string{"elem1"}}},
			},
		},
		{
			name: "single element with multiple labels",
			in: []ResourceMatcher{
				{Labels: types.Labels{"elem2": []string{"elem1", "elem2"}}},
			},
			out: []*types.DatabaseResourceMatcher{
				{Labels: &types.Labels{"elem2": []string{"elem1", "elem2"}}},
			},
		},
		{
			name: "multiple elements",
			in: []ResourceMatcher{
				{Labels: types.Labels{"elem1": []string{"elem1"}}},
				{Labels: types.Labels{"elem2": []string{"elem1", "elem2"}}},
				{Labels: types.Labels{"elem3": []string{"elem1", "elem2", "elem3"}}},
			},
			out: []*types.DatabaseResourceMatcher{
				{Labels: &types.Labels{"elem1": []string{"elem1"}}},
				{Labels: &types.Labels{"elem2": []string{"elem1", "elem2"}}},
				{Labels: &types.Labels{"elem3": []string{"elem1", "elem2", "elem3"}}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.out, ResourceMatchersToTypes(tt.in))
		})
	}
}

func TestServerMatcher(t *testing.T) {
	t.Parallel()
	newServer := func(meta *types.CloudMetadata, labels map[string]string) types.Server {
		s, err := types.NewServer("test-server", types.KindNode, types.ServerSpecV2{
			CloudMetadata: meta,
		})
		require.NoError(t, err)
		if labels != nil {
			s.SetStaticLabels(labels)
		}
		return s
	}
	wildcardLabels := types.Labels{types.Wildcard: []string{types.Wildcard}}

	passingCases := []struct {
		name    string
		matcher ServerMatcher
		server  types.Server
	}{
		{
			name: "labels match",
			matcher: ServerMatcher{
				ResourceMatcher: ResourceMatcher{
					Labels: types.Labels{"foo": []string{"bar"}},
				},
			},
			server: newServer(nil, map[string]string{"foo": "bar"}),
		},
		{
			name: "aws matches",
			matcher: ServerMatcher{
				ResourceMatcher: ResourceMatcher{
					Labels: wildcardLabels,
				},
				AWS: &AWSAttributeMatcher{
					AccountIDs:  []string{"abcd"},
					InstanceIDs: []string{"1234"},
				},
			},
			server: newServer(&types.CloudMetadata{
				AWS: &types.CloudMetadata_AWSInfo{
					AccountID:  "abcd",
					InstanceID: "1234",
				},
			}, nil),
		},
	}
	for _, tc := range passingCases {
		t.Run("accept "+tc.name, func(t *testing.T) {
			require.True(t, MatchServer([]ServerMatcher{tc.matcher}, tc.server))
		})
	}

	failingCases := []struct {
		name    string
		matcher ServerMatcher
		server  types.Server
	}{
		{
			name: "labels don't match",
			matcher: ServerMatcher{
				ResourceMatcher: ResourceMatcher{
					Labels: types.Labels{"foo": []string{"bar"}},
				},
			},
			server: newServer(nil, map[string]string{"foo": "baz"}),
		},
		{
			name: "aws doesn't match",
			matcher: ServerMatcher{
				ResourceMatcher: ResourceMatcher{
					Labels: wildcardLabels,
				},
				AWS: &AWSAttributeMatcher{
					AccountIDs:  []string{"abcd"},
					InstanceIDs: []string{"1234"},
				},
			},
			server: newServer(&types.CloudMetadata{
				AWS: &types.CloudMetadata_AWSInfo{
					AccountID:  "abcd",
					InstanceID: "5678",
				},
			}, nil),
		},
	}
	for _, tc := range failingCases {
		t.Run("reject "+tc.name, func(t *testing.T) {
			require.False(t, MatchServer([]ServerMatcher{tc.matcher}, tc.server))
		})
	}
}
