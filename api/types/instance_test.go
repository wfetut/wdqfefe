/*
Copyright 2022 Gravitational, Inc.

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

package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInstanceFilter(t *testing.T) {
	iis := []struct {
		id       string
		version  string
		services []SystemRole
		upgrader string
	}{
		{
			id:       "a1",
			version:  "v1.2.3",
			services: []SystemRole{RoleAuth},
		},
		{
			id:       "a2",
			version:  "v2.3.4",
			services: []SystemRole{RoleAuth, RoleNode},
			upgrader: "kube",
		},
		{
			id:       "p1",
			version:  "v1.2.1",
			services: []SystemRole{RoleProxy},
		},
		{
			id:       "p2",
			version:  "v2.3.1",
			services: []SystemRole{RoleProxy, RoleNode},
			upgrader: "unit",
		},
	}

	// set up group of test instances
	var instances []Instance
	for _, ii := range iis {
		ins, err := NewInstance(ii.id, InstanceSpecV1{
			Version:          ii.version,
			Services:         ii.services,
			ExternalUpgrader: ii.upgrader,
		})

		require.NoError(t, err)
		instances = append(instances, ins)
	}

	// set up test scenarios
	tts := []struct {
		desc    string
		filter  InstanceFilter
		matches []string
	}{
		{
			desc:   "match-all",
			filter: InstanceFilter{},
			matches: []string{
				"a1",
				"a2",
				"p1",
				"p2",
			},
		},
		{
			desc: "match-proxies",
			filter: InstanceFilter{
				Services: []SystemRole{
					RoleProxy,
				},
			},
			matches: []string{
				"p1",
				"p2",
			},
		},
		{
			desc: "match-old",
			filter: InstanceFilter{
				OlderThanVersion: "v2",
			},
			matches: []string{
				"a1",
				"p1",
			},
		},
		{
			desc: "match-new",
			filter: InstanceFilter{
				NewerThanVersion: "v2",
			},
			matches: []string{
				"a2",
				"p2",
			},
		},
		{
			desc: "match-version-range",
			filter: InstanceFilter{
				NewerThanVersion: "v1.2.2",
				OlderThanVersion: "v2.3.3",
			},
			matches: []string{
				"a1",
				"p2",
			},
		},
		{
			desc: "match-kube-upgrader",
			filter: InstanceFilter{
				ExternalUpgrader: "kube",
			},
			matches: []string{
				"a2",
			},
		},
		{
			desc: "match-no-upgrader",
			filter: InstanceFilter{
				NoExtUpgrader: true,
			},
			matches: []string{
				"a1",
				"p1",
			},
		},
	}

	for _, tt := range tts {
		var matches []string
		for _, ins := range instances {
			if tt.filter.Match(ins) {
				matches = append(matches, ins.GetName())
			}
		}

		require.Equal(t, tt.matches, matches)
	}
}

func TestInstanceControlLogExpiry(t *testing.T) {
	const ttl = time.Minute
	now := time.Now()
	instance, err := NewInstance("test-instance", InstanceSpecV1{
		LastSeen: now,
	})
	require.NoError(t, err)

	instance.AppendControlLog(
		InstanceControlLogEntry{
			Type: "foo",
			Time: now,
			TTL:  ttl,
		},
		InstanceControlLogEntry{
			Type: "bar",
			Time: now.Add(-ttl / 2),
			TTL:  ttl,
		},
		InstanceControlLogEntry{
			Type: "bin",
			Time: now.Add(-ttl * 2),
			TTL:  ttl,
		},
		InstanceControlLogEntry{
			Type: "baz",
			Time: now,
			TTL:  time.Hour,
		},
	)

	require.Len(t, instance.GetControlLog(), 4)

	instance.SyncLogAndResourceExpiry(ttl)

	require.Len(t, instance.GetControlLog(), 3)
	require.Equal(t, now.Add(time.Hour).UTC(), instance.Expiry())

	instance.SetLastSeen(now.Add(ttl))

	instance.SyncLogAndResourceExpiry(ttl)

	require.Len(t, instance.GetControlLog(), 2)
	require.Equal(t, now.Add(time.Hour).UTC(), instance.Expiry())

	instance.AppendControlLog(
		InstanceControlLogEntry{
			Type: "long-lived",
			Time: now,
			TTL:  time.Hour * 2,
		},
	)

	instance.SyncLogAndResourceExpiry(ttl)

	require.Len(t, instance.GetControlLog(), 3)
	require.Equal(t, now.Add(time.Hour*2).UTC(), instance.Expiry())
}
