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
import { screen } from '@testing-library/react';

import { render } from 'design/utils/testing';
import { InputLabel, InputLabelProps } from 'design/Input/InputLabel';

describe('inputLabel', () => {
  let props: InputLabelProps;

  beforeEach(() => {
    props = {
      label: 'some-label',
      aria: 'some-aria-string',
      required: false,
    };
  });

  test('renders label', () => {
    props.label = 'Company Name';
    render(
      <>
        <InputLabel {...props} />
        <input id={props.aria} />
      </>
    );

    expect(screen.getByLabelText('Company Name')).toBeInTheDocument();
  });

  test('renders required', () => {
    props.required = true;
    render(<InputLabel {...props} />);

    expect(screen.getByText('some-label')).toBeInTheDocument();

    screen.getByText((content, node) => {
      const hasText = node => node.textContent === 'some-label*';
      const nodeHasText = hasText(node);
      // eslint-disable-next-line testing-library/no-node-access
      const childrenDontHaveText = Array.from(node.children).every(
        child => !hasText(child)
      );
      return nodeHasText && childrenDontHaveText;
    });
  });

  test('renders optional sub-label', () => {
    props.subLabel = 'Last, First.';
    render(<InputLabel {...props} />);

    expect(screen.getByText('Last, First.')).toBeInTheDocument();
  });
});
