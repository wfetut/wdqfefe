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

import React, { useState, useRef } from 'react';
import styled from 'styled-components';
import { Flex, Button, Popover } from 'design';
import * as icons from 'design/Icon';

import { ListItem } from 'teleterm/ui/components/ListItem';

import { SearchBar } from '../Search';

import { Connections } from './Connections';
import { Clusters } from './Clusters';
import { Identity } from './Identity';
import { AdditionalActions } from './AdditionalActions';
import { ConnectionsIconStatusIndicator } from './Connections/ConnectionsIcon/ConnectionsIconStatusIndicator';

export function TopBar() {
  const [isOpen, setIsOpen] = useState(false);
  const iconRef = useRef();

  return (
    <Grid>
      <JustifyLeft>
        <Connections />
        <Container ref={iconRef} onClick={() => setIsOpen(true)}>
          <ConnectionsIconStatusIndicator connected={false} />
          <StyledButton
            kind="secondary"
            size="small"
            m="auto"
            title="Connect My Computer"
          >
            <icons.Wand fontSize={16} />
          </StyledButton>
        </Container>
        <Popover
          open={isOpen}
          anchorEl={iconRef.current}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
          onClose={() => setIsOpen(false)}
        >
          <Menu>
            <StyledListItem>
              <icons.Link fontSize={2} />
              Share computer
            </StyledListItem>
            <StyledListItem>
              <icons.Cog fontSize={2} />
              Manage agent
            </StyledListItem>
          </Menu>
        </Popover>
      </JustifyLeft>
      <CentralContainer>
        <Clusters />
        <SearchBar />
      </CentralContainer>
      <JustifyRight>
        <AdditionalActions />
        <Identity />
      </JustifyRight>
    </Grid>
  );
}

const Grid = styled(Flex).attrs({ gap: 3, py: 2, px: 3 })`
  background: ${props => props.theme.colors.levels.surface};
  width: 100%;
  height: 56px;
  align-items: center;
  justify-content: space-between;
  z-index: 2; // minimally higher z-index than the one defined in StyledTabs, so that its drop-shadow doesn't cover the TopBar
`;

const CentralContainer = styled(Flex).attrs({ gap: 3 })`
  flex: 1;
  align-items: center;
  justify-content: center;
  height: 100%;
  max-width: calc(${props => props.theme.space[10]}px * 9);
`;

const JustifyLeft = styled.div`
  display: flex;
  justify-self: start;
  align-items: center;
  height: 100%;
  gap: ${props => props.theme.space[3]}px;
`;

const JustifyRight = styled.div`
  display: flex;
  justify-self: end;
  align-items: center;
  height: 100%;
`;

const Container = styled.div`
  position: relative;
  display: inline-block;
`;

const StyledButton = styled(Button)`
  background: ${props => props.theme.colors.spotBackground[0]};
  padding: ${props => props.theme.space[2]}px;
  width: 30px;
  height: 30px;
`;

const Menu = styled.menu`
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  background: ${props => props.theme.colors.levels.elevated};
`;

const StyledListItem = styled(ListItem)`
  height: 38px;
  gap: ${props => props.theme.space[3]}px;
  padding: 0 ${props => props.theme.space[3]}px;
  border-radius: 0;

  &:disabled {
    cursor: default;
    color: ${props => props.theme.colors.text.disabled};

    &:hover {
      background-color: inherit;
    }
  }
`;
