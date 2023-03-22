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

import React, { useRef, useEffect } from 'react';
import styled from 'styled-components';
import { Box, Flex } from 'design';
import { space, width, color, height } from 'styled-system';

import {
  SearchContextProvider,
  useSearchContext,
} from 'teleterm/ui/Search/SearchContext';
import { KeyboardShortcutAction } from 'teleterm/services/config';
import {
  useKeyboardShortcutFormatters,
  useKeyboardShortcuts,
} from 'teleterm/ui/services/keyboardShortcuts';

const OPEN_COMMAND_BAR_SHORTCUT_ACTION: KeyboardShortcutAction =
  'openCommandBar';

export function SearchBarConnected() {
  return (
    <SearchContextProvider>
      <SearchBar />
    </SearchContextProvider>
  );
}

export function SearchBar() {
  const listRef = useRef<HTMLElement>();
  const containerRef = useRef<HTMLElement>();
  const inputRef = useRef<HTMLInputElement>();
  const { getAccelerator } = useKeyboardShortcutFormatters();
  const { activePicker, inputValue, onInputValueChange, opened, open, close } =
    useSearchContext();

  useEffect(() => {
    if (opened) {
      inputRef.current.focus();
    }
  }, [inputRef, opened]);

  useKeyboardShortcuts({
    [OPEN_COMMAND_BAR_SHORTCUT_ACTION]: () => {
      open();
    },
  });

  // TODO: bring back onBlur
  useEffect(() => {
    const onClickOutside = e => {
      if (!e.composedPath().includes(containerRef.current)) {
        close();
      }
    };
    window.addEventListener('click', onClickOutside);
    return () => window.removeEventListener('click', onClickOutside);
  }, [close]);

  function handleOnFocus() {
    if (!opened) {
      open();
    }
  }

  return (
    <Flex
      style={{
        position: 'relative',
        width: '600px',
        height: 'auto',
      }}
      justifyContent="center"
      ref={containerRef}
      onFocus={handleOnFocus}
    >
      <Input
        ref={inputRef}
        placeholder={activePicker.placeholder}
        value={inputValue}
        onChange={e => {
          onInputValueChange(e.target.value);
        }}
      />
      {!opened && (
        <Shortcut>{getAccelerator(OPEN_COMMAND_BAR_SHORTCUT_ACTION)}</Shortcut>
      )}
      {opened && (
        <StyledGlobalSearchResults ref={listRef}>
          {activePicker.picker}
        </StyledGlobalSearchResults>
      )}
    </Flex>
  );
}

const Input = styled.input(props => {
  const { theme } = props;
  return {
    height: '32px',
    background: theme.colors.primary.lighter,
    boxSizing: 'border-box',
    color: theme.colors.text.primary,
    width: '100%',
    border: 'none',
    outline: 'none',
    padding: '2px 8px',
    '&:hover, &:focus': {
      color: theme.colors.primary.contrastText,
      background: theme.colors.primary.lighter,

      opacity: 1,
    },

    ...space(props),
    ...width(props),
    ...height(props),
    ...color(props),
  };
});

const Shortcut = styled(Box)`
  position: absolute;
  right: 12px;
  top: 8px;
  padding: 2px 3px;
  color: ${({ theme }) => theme.colors.text.secondary};
  background-color: ${({ theme }) => theme.colors.primary.light};
  line-height: 12px;
  font-size: 12px;
  border-radius: 2px;
`;

const StyledGlobalSearchResults = styled.div(({ theme }) => {
  return {
    boxShadow: '8px 8px 18px rgb(0 0 0)',
    color: theme.colors.primary.contrastText,
    background: theme.colors.primary.light,
    boxSizing: 'border-box',
    width: '600px',
    marginTop: '32px',
    display: 'block',
    position: 'absolute',
    border: '1px solid ' + theme.colors.action.hover,
    fontSize: '12px',
    listStyle: 'none outside none',
    textShadow: 'none',
    zIndex: '1000',
    maxHeight: '350px',
    overflow: 'auto',
    minHeight: '50px',
  };
});
