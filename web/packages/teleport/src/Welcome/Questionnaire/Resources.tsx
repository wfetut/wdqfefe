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

import { Flex, Text } from 'design';

import React from 'react';

import { InputLabel } from 'design/Input/InputLabel';

import Image from 'design/Image';

import { useTheme } from 'styled-components';

import { CheckboxInput } from 'design/Checkbox';

import {
  ResourcesProps,
  ResourceType,
} from 'teleport/Welcome/Questionnaire/types';

export const Resources = ({
  resources,
  checked,
  updateFields,
}: ResourcesProps) => {
  const theme = useTheme();

  const updateResources = (label: string) => {
    let updated = checked;
    if (updated.includes(label)) {
      updated = updated.filter(r => r !== label);
    } else {
      updated.push(label);
    }

    updateFields({ resources: updated });
  };

  const renderCheck = (resource: ResourceType, index: number) => {
    const isSelected = checked.includes(resource.label);
    return (
      <label
        htmlFor={`box-${resource.label}`}
        data-testid={`box-${resource.label}`}
        key={`${index}-${resource.label}`}
        style={{
          width: '20%',
          height: '100%',
        }}
        onClick={() => updateResources(resource.label)}
      >
        <Flex
          id={`box-${resource.label}`}
          flexDirection="column"
          height="100%"
          bg={theme.colors.spotBackground[0]}
          p="12px 0"
          gap={1}
          borderRadius="4px"
          style={
            isSelected
              ? {
                  border: `1px solid ${theme.colors.brand}`,
                }
              : {}
          }
        >
          <CheckboxInput
            aria-labelledby="resources"
            role="checkbox"
            type="checkbox"
            name={resource.label}
            readOnly
            checked={checked.includes(resource.label)}
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
        </Flex>
      </label>
    );
  };

  return (
    <>
      <InputLabel
        label="Which infrastructure resources do you need to access frequently?"
        subLabel="Select all that apply."
        aria="resources"
        required
      />
      <Flex gap={2} alignItems="flex-start" height="170px">
        {resources.map((r: ResourceType, i: number) => renderCheck(r, i))}
      </Flex>
    </>
  );
};
