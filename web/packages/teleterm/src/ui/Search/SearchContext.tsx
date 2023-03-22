/**
 * Copyright 2022 Gravitational, Inc.
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
  useContext,
  useState,
  FC,
  useEffect,
  useCallback,
  createContext,
} from 'react';

import { getActionPicker } from './pickers/pickers';

const SearchContext = createContext<{
  inputValue: string;
  onInputValueChange(val: string): void;
  changeActivePicker(any): void;
  activePicker: any;
  close(): void;
  open(): void;
  opened: boolean;
}>(null);

export const SearchContextProvider: FC = props => {
  const [opened, setOpened] = useState(false);
  const [inputValue, setInputValue] = useState('');
  const [activePicker, setActivePicker] = useState(getActionPicker());

  useEffect(() => {
    setInputValue('');
  }, [activePicker]);

  const close = useCallback(() => {
    setOpened(false);
    setActivePicker(getActionPicker());
  }, []);

  function open(): void {
    setOpened(true);
  }

  return (
    <SearchContext.Provider
      value={{
        inputValue,
        onInputValueChange: setInputValue,
        changeActivePicker: setActivePicker,
        activePicker,
        close,
        opened,
        open,
      }}
      children={props.children}
    />
  );
};

export const useSearchContext = () => {
  const context = useContext(SearchContext);

  if (!context) {
    throw new Error('SearchContext requires SearchContextProvider context.');
  }

  return context;
};
