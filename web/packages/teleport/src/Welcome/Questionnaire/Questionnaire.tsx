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

import React, { useEffect, useState } from 'react';
import { ButtonPrimary, Card, Text } from 'design';

import { QuestionnaireFormFields } from 'teleport/Welcome/Questionnaire/types';
import { Company } from 'teleport/Welcome/Questionnaire/Company';
import { Role } from 'teleport/Welcome/Questionnaire/Role';
import { Resources } from 'teleport/Welcome/Questionnaire/Resources';
import { supportedResources } from 'teleport/Welcome/Questionnaire/constants';

export const Questionnaire = () => {
  const [valid, setValid] = useState<boolean>(false);

  const [formFields, setFormFields] = useState<QuestionnaireFormFields>({
    companyName: '',
    employeeCount: undefined,
    team: undefined,
    role: undefined,
    resources: [],
  });

  const updateForm = (fields: Partial<QuestionnaireFormFields>) => {
    setFormFields({
      role: fields.role ?? formFields.role,
      team: fields.team ?? formFields.team,
      resources: fields.resources ?? formFields.resources,
      companyName: fields.companyName ?? formFields.companyName,
      employeeCount: fields.employeeCount ?? formFields.employeeCount,
    });

    console.log('***: ', fields);
  };

  useEffect(() => {
    setValid(
      formFields.resources.length != 0 &&
        formFields.companyName != '' &&
        formFields.employeeCount != undefined &&
        formFields.team != undefined &&
        formFields.role != undefined
    );
  }, [formFields]);

  const submitForm = () => {
    console.info(formFields);
    // todo (michellescripts) submit all Qs to Sales Center
    // todo (michellescripts) set resource Q on user state
  };

  // todo (michellescripts) only display <Company .../> if the survey is unanswered for the account
  return (
    <Card mx="auto" maxWidth="600px" p="4">
      <Text typography="h2" mb={4}>
        Tell us about yourself
      </Text>
      <Company
        companyName={formFields.companyName}
        numberOfEmployees={formFields.employeeCount}
        updateFields={updateForm}
      />
      <Role
        role={formFields.role}
        team={formFields.team}
        updateFields={updateForm}
      />
      <Resources
        resources={supportedResources}
        checked={formFields.resources}
        updateFields={updateForm}
      />
      <ButtonPrimary
        mt={3}
        width="100%"
        size="large"
        disabled={!valid}
        onClick={submitForm}
      >
        Submit
      </ButtonPrimary>
    </Card>
  );
};
