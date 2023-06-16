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

import { Box, Flex, Input } from 'design';

import React, { useMemo } from 'react';

import Select, { Option } from 'shared/components/Select';

import { InputLabel } from 'design/Input/InputLabel';

import {
  CompanyProps,
  EmployeeOptionsStrings,
} from 'teleport/Welcome/Questionnaire/types';
import { EmployeeOptions } from 'teleport/Welcome/Questionnaire/constants';

export const Company = ({
  updateFields,
  companyName,
  numberOfEmployees,
}: CompanyProps) => {
  const options: Option<EmployeeOptionsStrings, EmployeeOptionsStrings>[] =
    useMemo(() => {
      return Object.values(EmployeeOptions)
        .filter(v => !isNaN(Number(v)))
        .map(key => ({
          value: EmployeeOptions[key],
          label: EmployeeOptions[key],
        }));
    }, []);

  return (
    <Flex flexDirection="column">
      <InputLabel label="Company Name" aria="company-name" required />
      <Input
        mb={3}
        id="company-name"
        type="text"
        value={companyName}
        placeholder="ex. github"
        onChange={e => {
          updateFields({
            companyName: e.target.value,
          });
        }}
      />
      <InputLabel label="Number of Employees" aria="employees" required />
      <Box mb={3}>
        <Select
          inputId="employees"
          label="employees"
          hasError={false}
          elevated={false}
          placeholder="Select Team Size"
          onChange={(e: Option<EmployeeOptionsStrings, string>) =>
            updateFields({
              employeeCount: e.value,
            })
          }
          options={options}
          value={
            numberOfEmployees
              ? { label: numberOfEmployees, value: numberOfEmployees }
              : null
          }
        />
      </Box>
    </Flex>
  );
};
