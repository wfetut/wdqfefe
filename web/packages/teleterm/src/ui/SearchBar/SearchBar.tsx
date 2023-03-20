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
import { Spinner } from 'design/Icon';
import { space, width, color, height } from 'styled-system';

import { useSearchBar } from './useSearchBar';
import { SearchBarList } from './SearchBarList';

export function SearchBar() {
  const {
    visible,
    attempt,
    activeItemIndex,
    onPickItem,
    setActiveItemIndex,
    onFocus,
    inputValue,
    setInputValue,
    onHide,
    onShow,
    placeholder,
    onBack,
    keyboardShortcut,
  } = useSearchBar();
  const refInput = useRef<HTMLInputElement>();
  const refList = useRef<HTMLElement>();
  const refContainer = useRef<HTMLElement>();

  useEffect(() => {
    if (visible) {
      refInput.current.focus();
    }
  }, [visible]);

  function handleOnFocus(e: React.FocusEvent) {
    // trigger a callback when focus is coming from external element
    if (!refContainer.current.contains(e['relatedTarget'])) {
      onFocus(e);
    }
    // ensure that
    if (!visible) {
      onShow();
    }
  }

  function handleOnBlur(e: React.FocusEvent) {
    const inside =
      e?.relatedTarget?.contains(refInput.current) ||
      e?.relatedTarget?.contains(refList.current);

    if (inside) {
      refInput.current.focus();
      return;
    }

    onHide();
  }

  const handleArrowKey = (e: React.KeyboardEvent, nudge: number) => {
    if (attempt.status === 'success') {
      const next = getNext(activeItemIndex + nudge, attempt.data.length);
      setActiveItemIndex(next);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    switch (e.key) {
      case 'Enter':
        e.stopPropagation();
        e.preventDefault();
        onPickItem(activeItemIndex);
        break;
      case 'Escape':
        onBack();
        break;
      case 'ArrowUp':
        e.stopPropagation();
        e.preventDefault();
        handleArrowKey(e, -1);
        break;
      case 'ArrowDown':
        e.stopPropagation();
        e.preventDefault();
        handleArrowKey(e, 1);
        break;
    }
  };

  return (
    <Flex
      style={{
        position: 'relative',
        width: '600px',
        height: 'auto',
      }}
      justifyContent="center"
      ref={refContainer}
      onFocus={handleOnFocus}
      onBlur={handleOnBlur}
    >
      <Input
        ref={refInput}
        placeholder={placeholder}
        onChange={e => setInputValue(e.target.value)}
        value={inputValue}
        onKeyDown={handleKeyDown}
      />
      {attempt.status === 'processing' && (
        <Animate>
          <Spinner />
        </Animate>
      )}
      {!visible && <Shortcut>{keyboardShortcut}</Shortcut>}
      {visible && (
        <SearchBarList
          ref={refList}
          items={attempt.data}
          activeItem={activeItemIndex}
          onPick={i => onPickItem(i)}
        />
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

const Animate = styled(Box)`
  position: absolute;
  right: 12px;
  top: 8px;
  padding: 2px 2px;
  line-height: 12px;
  font-size: 12px;
  animation: spin 1s linear infinite;
  @keyframes spin {
    from {
      transform: rotate(0deg);
    }
    to {
      transform: rotate(360deg);
    }
  }
`;

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

function getNext(selectedIndex = 0, max = 0) {
  let index = selectedIndex % max;
  if (index < 0) {
    index += max;
  }
  return index;
}
