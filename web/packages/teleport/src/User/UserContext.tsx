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
import React, {
  createContext,
  PropsWithChildren,
  useContext,
  useEffect,
  useState,
} from 'react';

import useAttempt from 'shared/hooks/useAttemptNext';

import { Indicator } from 'design';

import { Failed } from 'design/CardError';

import { StyledIndicator } from 'teleport/Main';

import * as service from 'teleport/services/userPreferences';

import { ThemePreference } from 'teleport/services/userPreferences/types';
import storage from 'teleport/services/localStorage';

import type {
  UserPreferences,
  UserPreferencesSubset,
} from 'teleport/services/userPreferences/types';

interface UserContextValue {
  preferences: UserPreferences;
  updatePreferences: (preferences: Partial<UserPreferences>) => void;
}

const UserContext = createContext<UserContextValue>(null);

export function useUser() {
  return useContext(UserContext);
}

function preferenceToThemeOption(userTheme: ThemePreference) {
  switch (userTheme) {
    case ThemePreference.Light:
      return 'light';
    case ThemePreference.Dark:
      return 'dark';
  }
}

function themeOptionToPreference(themeOption: 'light' | 'dark') {
  switch (themeOption) {
    case 'light':
      return ThemePreference.Light;
    case 'dark':
      return ThemePreference.Dark;
  }
}

export function UserContextProvider(props: PropsWithChildren<unknown>) {
  const { attempt, run } = useAttempt('processing');

  const [preferences, setPreferences] = useState<UserPreferences | null>(null);

  async function loadUserPreferences() {
    const preferences = await service.getUserPreferences();

    storage.setThemeOption(preferenceToThemeOption(preferences.theme));

    setPreferences(preferences);
  }

  function updatePreferences(newPreferences: UserPreferencesSubset) {
    if (newPreferences.theme) {
      const currentTheme = themeOptionToPreference(storage.getThemeOption());
      if (currentTheme !== newPreferences.theme) {
        storage.setThemeOption(preferenceToThemeOption(newPreferences.theme));
      }
    }

    setPreferences({ ...preferences, ...newPreferences } as UserPreferences);

    return service.updateUserPreferences(newPreferences);
  }

  useEffect(() => {
    run(loadUserPreferences);
  }, []);

  if (attempt.status === 'failed') {
    return <Failed message={attempt.statusText} />;
  }

  if (attempt.status !== 'success') {
    return (
      <StyledIndicator>
        <Indicator />
      </StyledIndicator>
    );
  }

  return (
    <UserContext.Provider value={{ preferences, updatePreferences }}>
      {props.children}
    </UserContext.Provider>
  );
}
