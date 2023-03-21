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

import { useEffect, useRef, useState } from 'react';

import { useAsync } from 'shared/hooks/useAsync';

import { useAppContext } from 'teleterm/ui/appContextProvider';
import {
  useKeyboardShortcutFormatters,
  useKeyboardShortcuts,
} from 'teleterm/ui/services/keyboardShortcuts';
import { KeyboardShortcutAction } from 'teleterm/services/config';
import { SearchBarAction } from 'teleterm/ui/services/searchBar';

const OPEN_COMMAND_BAR_SHORTCUT_ACTION: KeyboardShortcutAction =
  'openCommandBar';

export function useSearchBar() {
  const { searchBarService } = useAppContext();
  const { picker, visible } = searchBarService.useState();
  const inputRef = useRef<HTMLInputElement>();
  const [activeItemIndex, setActiveItemIndex] = useState(0);
  const [attempt, fetch] = useAsync(async () => {
    const items = await picker.onFilter(inputRef.current?.value || '');
    setActiveItemIndex(0);
    return items;
  });
  const { getAccelerator } = useKeyboardShortcutFormatters();

  // const debouncedFetch = useMemo(() => debounce(fetch, 150), [picker]);

  const onInputValueChange = () => {
    //TODO: add debounce
    fetch();
  };

  useEffect(() => {
    setActiveItemIndex(0);
    clearInputValue();
    fetch();
  }, [picker]);

  useKeyboardShortcuts({
    [OPEN_COMMAND_BAR_SHORTCUT_ACTION]: () => {
      searchBarService.show();
    },
  });

  const onFocus = (e: any) => {
    if (e.relatedTarget) {
      searchBarService.lastFocused = new WeakRef(e.relatedTarget);
    }
  };

  const onPickItem = (item: SearchBarAction) => {
    searchBarService.revertDefaultAndHide();
    picker.onPick(item);
  };

  const onBack = () => {
    searchBarService.goBack();
  };

  const clearInputValue = () => {
    if (inputRef.current) {
      inputRef.current.value = '';
    }
  };

  return {
    visible,
    activeItemIndex,
    attempt,
    onFocus,
    onBack,
    inputRef,
    onPickItem,
    setActiveItemIndex,
    onInputValueChange,
    placeholder: picker.getPlaceholder(),
    onHide: searchBarService.revertDefaultAndHide,
    onShow: searchBarService.show,
    keyboardShortcut: getAccelerator(OPEN_COMMAND_BAR_SHORTCUT_ACTION),
  };
}
