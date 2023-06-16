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

import React from 'react';

import { useTheme } from 'styled-components';

import { Flex, Text } from 'design';

export type InputLabelProps = {
  label: string;
  aria: string;
  required: boolean;
  subLabel?: string;
};

export const InputLabel = ({
  label,
  aria,
  required,
  subLabel,
}: InputLabelProps) => {
  const theme = useTheme();

  return (
    <Flex gap={1} mb={1}>
      <label
        htmlFor={aria}
        aria-label={aria}
        aria-required={required}
        style={{
          fontWeight: 300,
          fontSize: '14px',
          lineHeight: '20px',
        }}
        data-testid="aria"
      >
        {label}
      </label>
      {subLabel && (
        <Text color={theme.colors.text.slightlyMuted}>
          <i>{subLabel}</i>
        </Text>
      )}
      {required && <Text color={theme.colors.error.main}>*</Text>}
    </Flex>
  );
};
