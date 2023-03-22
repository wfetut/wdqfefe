import React, { useCallback, useEffect, useState } from 'react';
import styled from 'styled-components';

import { Box, Flex, Label as DesignLabel, Text } from 'design';
import * as icons from 'design/Icon';

import { makeEmptyAttempt, useAsync } from 'shared/hooks/useAsync';
import { Highlight } from 'shared/components/Highlight';

import { useAppContext } from 'teleterm/ui/appContextProvider';

import {
  ResourceMatch,
  SearchResult,
  SearchResultDatabase,
  SearchResultKube,
  SearchResultServer,
} from 'teleterm/ui/Search/searchResult';
import * as tsh from 'teleterm/services/tshd/types';
import { useSearch } from 'teleterm/ui/Search/useSearch';

import { mapToActions } from '../actions';
import { useSearchContext } from '../SearchContext';
import { SearchAction } from '../types';

import { getParameterPicker } from './pickers';
import { ResultList } from './ResultList';

export function ActionPicker() {
  const ctx = useAppContext();
  const [attempt, fetch, setAttempt] = useAsync(useSearch());
  const { inputValue, changeActivePicker, close } = useSearchContext();
  const debouncedInputValue = useDebounce(inputValue, 200);

  useEffect(() => {
    if (debouncedInputValue) {
      fetch(debouncedInputValue);
    } else {
      setAttempt(makeEmptyAttempt());
    }
  }, [debouncedInputValue]);

  const onPick = useCallback(
    (action: SearchAction) => {
      if (action.type === 'simple-action') {
        action.perform();
        close();
      }
      if (action.type === 'parametrized-action') {
        changeActivePicker(getParameterPicker(action));
      }
    },
    [changeActivePicker, close]
  );

  if (!inputValue) {
    return <div>Search for something</div>;
  }

  return (
    <ResultList<SearchAction>
      loading={attempt.status === 'processing'}
      items={mapToActions(ctx, attempt.data || [])}
      onPick={onPick}
      onBack={close}
      render={item => {
        const Component = ComponentMap[item.searchResult.kind];
        return <Component item={item.searchResult} />;
      }}
    />
  );
}

function useDebounce<T>(value: T, delay: number): T {
  // State and setters for debounced value
  const [debouncedValue, setDebouncedValue] = useState(value);
  useEffect(
    () => {
      // Update debounced value after delay
      const handler = setTimeout(() => {
        setDebouncedValue(value);
      }, delay);
      // Cancel the timeout if value changes (also on delay change or unmount)
      // This is how we prevent debounced value from updating if value is changed ...
      // .. within the delay period. Timeout gets cleared and restarted.
      return () => {
        clearTimeout(handler);
      };
    },
    [value, delay] // Only re-call effect if value or delay changes
  );
  return debouncedValue;
}

const ComponentMap: Record<
  SearchResult['kind'],
  React.FC<{ item: SearchResult }>
> = {
  server: ServerItem,
  kube: KubeItem,
  database: DatabaseItem,
};

function ServerItem(props: { item: SearchResultServer }) {
  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Server fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Flex justifyContent="space-between" alignItems="center">
          <Box mr={2}>
            <HighlightField field="hostname" searchResult={props.item} />
          </Box>
          <Box>
            <Text typography="body2" fontSize={0}>
              {props.item.score}
            </Text>
          </Box>
        </Flex>
        <Labels item={props.item} />
      </Flex>
    </Flex>
  );
}

function DatabaseItem(props: { item: SearchResultDatabase }) {
  const db = props.item.resource;

  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Database fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Flex justifyContent="space-between" alignItems="center">
          <Box mr={2}>
            <HighlightField field="name" searchResult={props.item} />
          </Box>
          <Box>
            <Text typography="body2" fontSize={0}>
              {db.type}/{db.protocol} {props.item.score}
            </Text>
          </Box>
        </Flex>
        <Labels item={props.item} />
      </Flex>
    </Flex>
  );
}

function KubeItem(props: { item: SearchResultKube }) {
  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Kubernetes fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Box mr={2}>
          <HighlightField field="name" searchResult={props.item} />
        </Box>
        <Labels item={props.item} />
      </Flex>
    </Flex>
  );
}

function Labels(props: { item: SearchResult }) {
  return (
    <Flex gap={1} flexWrap="wrap">
      {props.item.resource.labelsList.map(label => (
        <Label key={label.name + label.value} item={props.item} label={label} />
      ))}
    </Flex>
  );
}

function Label(props: { item: SearchResult; label: tsh.Label }) {
  const { item, label } = props;
  const labelMatches = item.labelMatches.filter(
    match => match.labelName == label.name
  );
  const nameMatches = labelMatches
    .filter(match => match.kind === 'label-name')
    .map(match => match.searchTerm);
  const valueMatches = labelMatches
    .filter(match => match.kind === 'label-value')
    .map(match => match.searchTerm);

  return (
    <DesignLabel key={label.name} kind="secondary">
      <Highlight text={label.name} keywords={nameMatches} />:{' '}
      <Highlight text={label.value} keywords={valueMatches} />
    </DesignLabel>
  );
}

function HighlightField(props: {
  searchResult: SearchResult;
  field: ResourceMatch<SearchResult['kind']>['field'];
}) {
  // `as` used as a workaround for a TypeScript issue.
  // https://github.com/microsoft/TypeScript/issues/33591
  const keywords = (
    props.searchResult.resourceMatches as ResourceMatch<SearchResult['kind']>[]
  )
    .filter(match => match.field === props.field)
    .map(match => match.searchTerm);

  return (
    <Highlight
      text={props.searchResult.resource[props.field]}
      keywords={keywords}
    />
  );
}

const SquareIconBackground = styled(Box)`
  background: ${props => props.color};
  display: flex;
  align-items: center;
  justify-content: center;
  height: 26px;
  width: 26px;
  margin-right: 8px;
  border-radius: 2px;
  padding: 4px;
`;
