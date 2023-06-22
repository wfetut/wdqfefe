/**
 * Copyright 2023 Gravitational, Inc
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import React, { useMemo } from 'react';

import { Option } from 'shared/components/Select';

import FieldSelect from 'shared/components/FieldSelect';

import { requiredField } from 'shared/components/Validation/rules';

import {
  RoleProps,
  TeamOptionsStrings,
  TitleOptionsStrings,
} from 'teleport/Welcome/Questionnaire/types';
import {
  TeamOptions,
  TitleOptions,
} from 'teleport/Welcome/Questionnaire/constants';

export const Role = ({ team, role, updateFields }: RoleProps) => {
  const teamOptions: Option<TeamOptionsStrings, TeamOptionsStrings>[] =
    useMemo(() => {
      return Object.values(TeamOptions)
        .filter(v => !isNaN(Number(v)))
        .map(key => ({
          value: TeamOptions[key],
          label: TeamOptions[key],
        }));
    }, []);

  const titleOptions: Option<TitleOptionsStrings, TitleOptionsStrings>[] =
    useMemo(() => {
      return Object.values(TitleOptions)
        .filter(v => !isNaN(Number(v)))
        .map(key => ({
          value: TitleOptions[key],
          label: TitleOptions[key],
        }));
    }, []);

  return (
    <>
      <FieldSelect
        label="Which Team are you on?"
        rule={requiredField('Team is required')}
        placeholder="Select Team"
        onChange={(e: Option<TeamOptionsStrings, string>) =>
          updateFields({ team: e.value })
        }
        options={teamOptions}
        value={team ? { label: team, value: team } : null}
      />
      <FieldSelect
        label="Job Title"
        rule={requiredField('Job Title is required')}
        placeholder="Select Job Title"
        onChange={(e: Option<TitleOptionsStrings, string>) =>
          updateFields({ role: e.value })
        }
        options={titleOptions}
        value={role ? { label: role, value: role } : null}
      />
    </>
  );
};
