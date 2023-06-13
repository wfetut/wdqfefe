/*
Copyright 2015 Gravitational, Inc.

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

package ui

import (
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
)

// Unified Resource describes a unified resource for webapp
type UnifiedResouce struct {
	// Name is this server name
	Name string `json:"id"`
	// ClusterName is this server cluster name
	ClusterName string `json:"siteId"`
	// Labels is this server list of labels
	Labels []Label `json:"tags"`
}

// MakeUnifiedResource creates server objects for webapp
func MakeUnifiedResource(clusterName string, resources []types.ResourceWithLabels, accessChecker services.AccessChecker) ([]UnifiedResouce, error) {
	unifiedResources := []UnifiedResouce{}
	for _, resource := range resources {
		labels := resource.GetAllLabels()
		uiLabels := makeLabels(labels)

		// serverLogins, err := accessChecker.GetAllowedLoginsForResource(resource)
		// if err != nil {
		// 	return nil, trace.Wrap(err)
		// }

		unifiedResources = append(unifiedResources, UnifiedResouce{
			ClusterName: clusterName,
			Labels:      uiLabels,
			Name:        resource.GetName(),
		})
	}

	return unifiedResources, nil
}
