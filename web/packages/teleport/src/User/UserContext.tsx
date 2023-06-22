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
import React, { createContext, PropsWithChildren, useContext, useEffect, useState } from 'react';

import useAttempt from 'shared/hooks/useAttemptNext';

import { Indicator } from 'design';

import { StyledIndicator } from 'teleport/Main';

import * as service from 'teleport/services/userPreferences';

import type { UserPreferences } from 'teleport/services/userPreferences/types';
import { Failed } from 'design/CardError';

interface UserContextValue {
  preferences: UserPreferences;
  updatePreferences: (preferences: UserPreferences) => void;
}

const UserContext = createContext<UserContextValue>(null);

export function useUser() {
  return useContext(UserContext);
}

export function UserContextProvider(props: PropsWithChildren<unknown>) {
  const { attempt, setAttempt, run } = useAttempt('processing');

  const [preferences, setPreferences] = useState<UserPreferences | null>(null);

  async function loadUserPreferences() {
    const preferences = await service.getUserPreferences();

    setPreferences(preferences);
  }

  function updatePreferences(preferences: UserPreferences) {
    setPreferences(preferences);

    return service.updateUserPreferences(preferences);
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
