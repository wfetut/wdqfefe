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
package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerInfoMatches(t *testing.T) {
	t.Parallel()
	newServerInfo := func(name string, spec ServerInfoSpecV1) ServerInfo {
		return &ServerInfoV1{
			Metadata: Metadata{
				Name: name,
			},
			Spec: spec,
		}
	}

	passingCases := []struct {
		name string
		a    ServerInfo
		b    ServerInfo
	}{
		{
			name: "name matches",
			a:    newServerInfo("si", ServerInfoSpecV1{}),
			b:    newServerInfo("si", ServerInfoSpecV1{}),
		},
		{
			name: "AWS matches",
			a: newServerInfo("", ServerInfoSpecV1{
				AWS: &ServerInfoSpecV1_AWSInfo{
					AccountID:  "abcd",
					InstanceID: "1234",
				},
			}),
			b: newServerInfo("", ServerInfoSpecV1{
				AWS: &ServerInfoSpecV1_AWSInfo{
					AccountID:  "abcd",
					InstanceID: "1234",
				},
			}),
		},
	}

	for _, tc := range passingCases {
		t.Run("accept "+tc.name, func(t *testing.T) {
			require.True(t, tc.a.Matches(tc.b))
		})
	}

	failingCases := []struct {
		name string
		a    ServerInfo
		b    ServerInfo
	}{
		{
			name: "name doesn't match",
			a:    newServerInfo("a", ServerInfoSpecV1{}),
			b:    newServerInfo("b", ServerInfoSpecV1{}),
		},
		{
			name: "AWS doesn't match",
			a: newServerInfo("a", ServerInfoSpecV1{
				AWS: &ServerInfoSpecV1_AWSInfo{
					AccountID:  "abcd",
					InstanceID: "1234",
				},
			}),
			b: newServerInfo("b", ServerInfoSpecV1{
				AWS: &ServerInfoSpecV1_AWSInfo{
					AccountID:  "efgh",
					InstanceID: "5678",
				},
			}),
		},
	}

	for _, tc := range failingCases {
		t.Run("reject "+tc.name, func(t *testing.T) {
			require.False(t, tc.a.Matches(tc.b))
		})
	}
}
