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

import React, { useEffect, useRef } from 'react';
import styled from 'styled-components';
import { Box, Text, Flex, Label as DesignLabel } from 'design';
import * as icons from 'design/Icon';
import { Highlight } from 'shared/components/Highlight';

import * as types from 'teleterm/ui/services/searchBar/types';
import {
  ActionDbConnect,
  ActionKubeConnect,
  ActionSshConnect,
  SearchBarAction,
} from 'teleterm/ui/services/searchBar/types';
import { SearchResult, ResourceMatch } from 'teleterm/ui/Search/searchResult';

import type * as tsh from 'teleterm/services/tshd/types';

export const SearchBarList = React.forwardRef<HTMLElement, Props>(
  (props, ref) => {
    const { items, activeItem } = props;
    const activeItemRef = useRef<HTMLDivElement>();

    useEffect(() => {
      // `false` - bottom of the element will be aligned to the bottom of the visible area of the scrollable ancestor
      activeItemRef.current?.scrollIntoView(false);
    }, [activeItem]);

    const $items = items.map((r, index) => {
      const Cmpt = ComponentMap[r.kind] || UnknownItem;
      const isActive = index === activeItem;

      return (
        <StyledItem
          data-attr={index}
          ref={isActive ? activeItemRef : null}
          $active={isActive}
          key={`${index}`}
        >
          <Cmpt item={r} />
        </StyledItem>
      );
    });

    function handleClick(e: React.SyntheticEvent) {
      const el = e.target;
      if (el instanceof Element) {
        const itemEl = el.closest('[data-attr]');
        const index = parseInt(itemEl.getAttribute('data-attr'));
        props.onPick(items[index]);
      }
    }

    return (
      <StyledGlobalSearchResults
        ref={ref}
        tabIndex={-1}
        data-attr="quickpicker.list"
        onClick={handleClick}
      >
        {items.length === 0 ? 'Search for something' : $items}
      </StyledGlobalSearchResults>
    );
  }
);

function UnknownItem(props: { item: types.SearchBarAction }) {
  const { kind } = props.item;
  return <div>unknown kind: {kind} </div>;
}

function SshLoginItem(props: { item: types.ActionSshLogin }) {
  return <div>{props.item.searchResult.login}</div>;
}

function DbUsernameItem(props: { item: types.ActionDbUsername }) {
  return <div>{props.item.searchResult.username}</div>;
}

const StyledItem = styled.div(({ theme, $active }) => {
  return {
    '&:hover, &:focus': {
      cursor: 'pointer',
      background: theme.colors.primary.lighter,
    },

    borderBottom: `2px solid ${theme.colors.primary.main}`,
    padding: '2px 8px',
    color: theme.colors.primary.contrastText,
    background: $active
      ? theme.colors.primary.lighter
      : theme.colors.primary.light,
  };
});

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

type Props = {
  items: types.SearchBarAction[];
  activeItem: number;
  onPick(item: types.SearchBarAction): void;
};

const ComponentMap: Record<
  SearchBarAction['kind'],
  React.FC<{ item: SearchBarAction }>
> = {
  ['action.ssh-connect']: ServerItem,
  ['action.kube-connect']: KubeItem,
  ['action.db-connect']: DatabaseItem,
  ['action.ssh-login']: SshLoginItem,
  ['action.db-username']: DbUsernameItem,
};

function ServerItem(props: { item: ActionSshConnect }) {
  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Server fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Flex justifyContent="space-between" alignItems="center">
          <Box mr={2}>
            <HighlightField
              field="hostname"
              searchResult={props.item.searchResult}
            />
          </Box>
          <Box>
            <Text typography="body2" fontSize={0}>
              {props.item.searchResult.score}
            </Text>
          </Box>
        </Flex>
        <Labels item={props.item.searchResult} />
      </Flex>
    </Flex>
  );
}

function DatabaseItem(props: { item: ActionDbConnect }) {
  const db = props.item.searchResult.resource;

  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Database fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Flex justifyContent="space-between" alignItems="center">
          <Box mr={2}>
            <HighlightField
              field="name"
              searchResult={props.item.searchResult}
            />
          </Box>
          <Box>
            <Text typography="body2" fontSize={0}>
              {db.type}/{db.protocol} {props.item.searchResult.score}
            </Text>
          </Box>
        </Flex>
        <Labels item={props.item.searchResult} />
      </Flex>
    </Flex>
  );
}

function KubeItem(props: { item: ActionKubeConnect }) {
  return (
    <Flex alignItems="flex-start" p={1} minWidth="300px">
      <SquareIconBackground color="#4DB2F0">
        <icons.Kubernetes fontSize="20px" />
      </SquareIconBackground>
      <Flex flexDirection="column" ml={1} flex={1}>
        <Box mr={2}>
          <HighlightField field="name" searchResult={props.item.searchResult} />
        </Box>
        <Labels item={props.item.searchResult} />
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
