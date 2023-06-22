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

import type {
  AssistUserPreferences,
  AssistUserPreferencesPayload,
} from 'teleport/Assist/types';

enum UserTheme {
  Light = 1,
  Dark = 2,
}

export interface UserPreferences {
  theme: UserTheme;
  assist: AssistUserPreferences;
}

export interface UserPreferencesPayload {
  theme: UserTheme;
  assist: AssistUserPreferencesPayload;
}

export type GetUserPreferencesResponse = UserPreferencesPayload;
export type UpdateUserPreferencesRequest = UserPreferencesPayload;
