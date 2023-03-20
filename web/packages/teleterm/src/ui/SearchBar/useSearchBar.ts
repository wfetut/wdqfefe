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

import { useEffect, useState } from 'react';

import { useAsync } from 'shared/hooks/useAsync';

import { useAppContext } from 'teleterm/ui/appContextProvider';
import {
  useKeyboardShortcutFormatters,
  useKeyboardShortcuts,
} from 'teleterm/ui/services/keyboardShortcuts';
import { KeyboardShortcutAction } from 'teleterm/services/config';

const OPEN_COMMAND_BAR_SHORTCUT_ACTION: KeyboardShortcutAction =
  'openCommandBar';

export function useSearchBar() {
  const { searchBarService } = useAppContext();
  const { picker, visible } = searchBarService.useState();
  const [inputValue, setInputValue] = useState('');
  const [activeItemIndex, setActiveItemIndex] = useState(0);
  const [attempt, fetch] = useAsync(
    async () => await picker.onFilter(inputValue)
  );
  const { getAccelerator } = useKeyboardShortcutFormatters();

  useEffect(() => {
    //TODO: add debounce
    fetch();
  }, [inputValue, picker]);

  useEffect(() => {
    setActiveItemIndex(0);
    setInputValue('');
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

  const onPickItem = (index: number) => {
    searchBarService.revertDefaultAndHide();
    const item = attempt.status === 'success' && attempt.data[index];
    if (item) {
      picker.onPick(item);
    }
  };

  const onBack = () => {
    searchBarService.goBack();
  };

  return {
    visible,
    activeItemIndex,
    attempt,
    onFocus,
    onBack,
    onPickItem,
    setActiveItemIndex,
    inputValue,
    setInputValue,
    placeholder: picker.getPlaceholder(),
    onHide: searchBarService.hide,
    onShow: searchBarService.show,
    keyboardShortcut: getAccelerator(OPEN_COMMAND_BAR_SHORTCUT_ACTION),
  };
}
