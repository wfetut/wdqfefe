/**
 * Copyright 2023 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
import cfg from 'teleport/config';
import api from 'teleport/services/api';

import { makeAssistUserPreferences, makeAssistUserPreferencesPayload } from 'teleport/Assist/service';

import type { GetUserPreferencesResponse, UserPreferences, UserPreferencesPayload } from 'teleport/services/userPreferences/types';

export async function getUserPreferences() {
  const res: GetUserPreferencesResponse = await api.get(cfg.api.userPreferencesPath);

  return makeUserPreferences(res);
}

export function updateUserPreferences(preferences: UserPreferences) {
  const payload = makeUserPreferencesPayload(preferences);

  return api.put(cfg.api.userPreferencesPath, payload);
}

function makeUserPreferences(payload: UserPreferencesPayload): UserPreferences {
  return {
    theme: payload.theme,
    assist: makeAssistUserPreferences(payload.assist),
  }
}

function makeUserPreferencesPayload(preferences: UserPreferences): UserPreferencesPayload {
  return {
    theme: preferences.theme,
    assist: makeAssistUserPreferencesPayload(preferences.assist),
  }
}
