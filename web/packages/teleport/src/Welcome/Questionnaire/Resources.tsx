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

import { Flex, LabelInput, Text } from 'design';
import React from 'react';
import Image from 'design/Image';
import { CheckboxInput } from 'design/Checkbox';
import { useRule } from 'shared/components/Validation';

import { requiredArrayField } from 'teleport/Welcome/Questionnaire/constants';

import { ResourcesProps, ResourceType } from './types';
import { ResourceWrapper } from './ResourceWrapper';

export const Resources = ({
  resources,
  checked,
  updateFields,
}: ResourcesProps) => {
  const { valid, message } = useRule(requiredArrayField(checked));

  const updateResources = (label: string) => {
    let updated = checked;
    if (updated.includes(label)) {
      updated = updated.filter(r => r !== label);
    } else {
      updated.push(label);
    }

    updateFields({ resources: updated });
  };

  const renderCheck = (resource: ResourceType) => {
    const isSelected = checked.includes(resource.label);
    return (
      <label
        htmlFor={`box-${resource.label}`}
        data-testid={`box-${resource.label}`}
        key={resource.label}
        style={{
          width: '20%',
          height: '100%',
        }}
        onClick={() => updateResources(resource.label)}
      >
        <ResourceWrapper isSelected={isSelected} invalid={!valid}>
          <CheckboxInput
            aria-labelledby="resources"
            role="checkbox"
            type="checkbox"
            name={resource.label}
            readOnly
            checked={checked.includes(resource.label)}
            rule={requiredArrayField(checked)}
            style={{
              alignSelf: 'flex-end',
            }}
          />
          <Flex
            flexDirection="column"
            alignItems="center"
            justifyContent="center"
          >
            <Image src={resource.image} height="64px" width="64px" />
            <Text textAlign="center" typography="body3">
              {resource.label}
            </Text>
          </Flex>
        </ResourceWrapper>
      </label>
    );
  };

  return (
    <>
      <Flex gap={1} mb={1}>
        <LabelInput htmlFor={'resources'} hasError={!valid}>
          {valid
            ? `Which infrastructure resources do you need to access frequently?`
            : message}
          <i>&nbsp;Select all that apply.</i>
        </LabelInput>
      </Flex>
      <Flex gap={2} alignItems="flex-start" height="170px">
        {resources.map((r: ResourceType) => renderCheck(r))}
      </Flex>
    </>
  );
};
