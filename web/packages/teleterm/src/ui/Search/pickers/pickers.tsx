import React from 'react';

import { ParametrizedAction, SearchPicker } from '../types';

import { ActionPicker } from './ActionPicker';
import { ParameterPicker } from './ParameterPicker';

export const getActionPicker = (): SearchPicker => {
  return {
    picker: <ActionPicker />,
    placeholder: 'Search for something',
  };
};
export const getParameterPicker = (
  parametrizedAction: ParametrizedAction
): SearchPicker => {
  return {
    picker: <ParameterPicker action={parametrizedAction} />,
    placeholder: parametrizedAction.parameter.placeholder,
  };
};
