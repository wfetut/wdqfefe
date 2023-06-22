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

package userpreferences

import (
	"github.com/gravitational/teleport/api/gen/proto/go/assist/v1"
)

type AssistUserPreferencesResponse struct {
	PreferredLogins []string                 `json:"preferred_logins"`
	ViewMode        assist.AssistantViewMode `json:"view_mode"`
}

// DefaultUserPreferences is the default assist user preferences.
var DefaultUserPreferences = &assist.AssistantUserPreferences{
	PreferredLogins: []string{},
	ViewMode:        assist.AssistantViewMode_ASSISTANT_VIEW_MODE_DOCKED,
}

// MergeUserPreferences merges the given assist user preferences.
func MergeUserPreferences(a, b *assist.AssistantUserPreferences) *assist.AssistantUserPreferences {
	if b.PreferredLogins != nil {
		a.PreferredLogins = b.PreferredLogins
	}

	if b.ViewMode != assist.AssistantViewMode_ASSISTANT_VIEW_MODE_UNSPECIFIED {
		a.ViewMode = b.ViewMode
	}

	return a
}

// UserPreferencesResponse creates a JSON response from the given assist user preferences.
func UserPreferencesResponse(resp *assist.AssistantUserPreferences) AssistUserPreferencesResponse {
	jsonResp := AssistUserPreferencesResponse{
		PreferredLogins: make([]string, 0, len(resp.PreferredLogins)),
		ViewMode:        resp.ViewMode,
	}

	for _, login := range resp.PreferredLogins {
		jsonResp.PreferredLogins = append(jsonResp.PreferredLogins, login)
	}

	return jsonResp
}
