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
	"github.com/gravitational/trace"
)

// Unified Resource describes a unified resource for webapp
type UIResource struct {
	// Kind is the resource kind
	Kind string `json:"kind"`
	// Name is this server name
	Name string `json:"name"`
	// Labels is this server list of labels
	Labels []Label `json:"tags"`
}

// i need to make this an actual interface

// MakeUIResource creates server objects for webapp
func MakeUIResource(clusterName string, resources []types.ResourceWithLabels, accessChecker services.AccessChecker) ([]UIResource, error) {
	uiResources := []UIResource{}
	for _, resource := range resources {
		switch r := resource.(type) {
		case types.Server:
			serverLabels := r.GetStaticLabels()
			serverCmdLabels := r.GetCmdLabels()
			uiLabels := makeLabels(serverLabels, transformCommandLabels(serverCmdLabels))
			uiResources = append(uiResources, UIResource{
				Kind:   r.GetKind(),
				Name:   r.GetHostname(),
				Labels: uiLabels,
			})
		case types.DatabaseServer:
			// dbNames, dbUsers, err := getDatabaseUsersAndNames(accessChecker)
			// if err != nil {
			// 	return nil, trace.Wrap(err)
			// }
			uiLabels := makeLabels(r.GetAllLabels())
			uiResources = append(uiResources, UIResource{
				Kind:   r.GetKind(),
				Name:   r.GetName(),
				Labels: uiLabels,
			})
		default:
			return nil, trace.Errorf("UI Resource has unknown type: %T", resource)
		}
	}

	return uiResources, nil
}

// func getDatabaseUsersAndNames(accessChecker services.AccessChecker) (dbNames []string, dbUsers []string, err error) {
// 	dbNames, dbUsers, err = accessChecker.CheckDatabaseNamesAndUsers(0, true /* force ttl override*/)
// 	if err != nil {
// 		// if NotFound error:
// 		// This user cannot request database access, has no assigned database names or users
// 		//
// 		// Every other error should be reported upstream.
// 		if !trace.IsNotFound(err) {
// 			return nil, nil, trace.Wrap(err)
// 		}

// 		// We proceed with an empty list of DBUsers and DBNames
// 		dbUsers = []string{}
// 		dbNames = []string{}
// 	}

// 	return dbNames, dbUsers, nil
// }
