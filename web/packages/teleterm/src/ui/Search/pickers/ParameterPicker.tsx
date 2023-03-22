import React, { useCallback, useEffect } from 'react';

import { Highlight } from 'shared/components/Highlight';

import { useAsync } from 'shared/hooks/useAsync';

import { useSearchContext } from '../SearchContext';

import { ResultList } from './ResultList';
import { getActionPicker } from './pickers';
import { ParametrizedAction } from 'teleterm/ui/Search/types';

interface ParameterPickerProps {
  action: ParametrizedAction;
}

export function ParameterPicker(props: ParameterPickerProps) {
  const { inputValue, close, changeActivePicker } = useSearchContext();
  const [attempt, fetch] = useAsync(props.action.parameter.getSuggestions);
  let filtered: string[] = [];

  useEffect(() => {
    fetch();
  }, [props.action]);

  if (attempt.status === 'success') {
    filtered = attempt.data.filter(v =>
      v.toLocaleLowerCase().includes(inputValue.toLocaleLowerCase())
    );
  }

  if (inputValue) {
    filtered.unshift(inputValue);
  }

  const onPick = useCallback(
    (item: string) => {
      props.action.perform(item);
      close();
    },
    [close, props.action]
  );

  return (
    <ResultList<string>
      loading={attempt.status === 'processing'}
      items={filtered}
      onPick={onPick}
      onBack={() => changeActivePicker(getActionPicker())}
      render={item => (
        <Highlight text={item} keywords={[inputValue]}></Highlight>
      )}
    />
  );
}
