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
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/gen/proto/go/assist/v1"
)

type AssistUserPreferencesResponse struct {
	PreferredLogins []string `json:"preferred_logins"`
	ViewMode assist.AssistantViewMode `json:"view_mode"`
}

// DefaultUserPreferences is the default assist user preferences.
var DefaultUserPreferences = &assist.AssistantUserPreferences{
	PreferredLogins: []string{},
	ViewMode:        assist.AssistantViewMode_ASSISTANT_VIEW_MODE_DOCKED,
}

// ValidateUserPreferences validates the given assist user preferences.
func ValidateUserPreferences(preferences *assist.AssistantUserPreferences) error {
	if preferences == nil {
		return trace.BadParameter("missing assist preferences")
	}
	if preferences.PreferredLogins == nil {
		return trace.BadParameter("missing assist preferred logins")
	}
	if preferences.ViewMode == assist.AssistantViewMode_ASSISTANT_VIEW_MODE_UNSPECIFIED {
		return trace.BadParameter("missing assist view mode")
	}

	return nil
}

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
