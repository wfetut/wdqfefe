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

import React, { useMemo, useRef } from 'react';

import styled from 'styled-components';
import { debounce } from 'shared/utils/highbar';
import {
  Indicator,
  Box,
  ButtonPrimary,
  ButtonSecondary,
  Flex,
  Link,
  Text,
} from 'design';
import Validation from 'shared/components/Validation';
import * as Alerts from 'design/Alert';
import * as icons from 'design/Icon';

import { CliCommand } from './CliCommand';
import { ConfigFieldInput, PortFieldInput } from './common';
import { DocumentGatewayProps } from './DocumentGateway';

type OnlineDocumentGatewayProps = Pick<
  DocumentGatewayProps,
  | 'changeDbNameAttempt'
  | 'changePortAttempt'
  | 'disconnect'
  | 'changeDbName'
  | 'changePort'
  | 'gateway'
  | 'runCliCommand'
>;

export function OnlineDocumentGateway(props: OnlineDocumentGatewayProps) {
  const isPortOrDbNameProcessing =
    props.changeDbNameAttempt.status === 'processing' ||
    props.changePortAttempt.status === 'processing';
  const hasError =
    props.changeDbNameAttempt.status === 'error' ||
    props.changePortAttempt.status === 'error';
  const formRef = useRef<HTMLFormElement>();
  const { gateway } = props;

  const handleChangeDbName = useMemo(() => {
    return debounce((value: string) => {
      props.changeDbName(value);
    }, 150);
  }, [props.changeDbName]);

  const handleChangePort = useMemo(() => {
    return debounce((value: string) => {
      if (formRef.current.reportValidity()) {
        props.changePort(value);
      }
    }, 1000);
  }, [props.changePort]);

  const $errors = hasError && (
    <Flex flexDirection="column" gap={2} mb={3}>
      {props.changeDbNameAttempt.status === 'error' && (
        <Alerts.Danger mb={0}>
          Could not change the database name:{' '}
          {props.changeDbNameAttempt.statusText}
        </Alerts.Danger>
      )}
      {props.changePortAttempt.status === 'error' && (
        <Alerts.Danger mb={0}>
          Could not change the port number: {props.changePortAttempt.statusText}
        </Alerts.Danger>
      )}
    </Flex>
  );

  const $landing = (
    <Flex flexDirection="column" gap={4}>
      <div>
        <Text>
          Lorem ipsum dolor sit amet, consectetur adipiscing elit. Maecenas
          gravida viverra accumsan. Ut risus magna, sollicitudin ac purus ac,
          congue posuere lorem.
        </Text>
        <Text>
          Integer a rutrum nulla, et porttitor nisi. Vivamus pharetra bibendum
          pretium. Nunc molestie mi dapibus turpis accumsan, nec dignissim sem
          egestas.
        </Text>
      </div>

      <ButtonPrimary mx="auto">Setup the agent</ButtonPrimary>
    </Flex>
  );

  const phasesInProgress: { status: PhaseStatus; text: string }[] = [
    { text: 'Setting up roles', status: 'done' },
    { text: 'Downloading the agent', status: 'processing' },
    { text: 'Generating the config file', status: 'waiting' },
    { text: 'Joining the cluster', status: 'waiting' },
  ];

  const phasesError: { status: PhaseStatus; text: string }[] =
    phasesInProgress.map((phase, index) => ({
      ...phase,
      status: index === 1 ? 'error' : phase.status,
    }));

  const phasesDone: { status: PhaseStatus; text: string }[] =
    phasesInProgress.map(phase => ({
      ...phase,
      status: 'done',
    }));

  const $setupInProgress = (
    <Flex flexDirection="column" gap={4}>
      <ProgressBar phases={phasesInProgress} />
      <ButtonPrimary
        disabled="disabled"
        css={`
          align-self: flex-start;
        `}
      >
        Connect to computer
      </ButtonPrimary>
    </Flex>
  );

  const $setupError = (
    <Flex flexDirection="column" gap={4}>
      <ProgressBar phases={phasesError} />
      <Flex gap={2}>
        <ButtonPrimary
          disabled="disabled"
          css={`
            align-self: flex-start;
          `}
        >
          Connect to computer
        </ButtonPrimary>
        <ButtonPrimary>Retry</ButtonPrimary>
      </Flex>
    </Flex>
  );

  const $setupDone = (
    <Flex flexDirection="column" gap={4}>
      <ProgressBar phases={phasesDone} />
      <ButtonPrimary
        css={`
          align-self: flex-start;
        `}
      >
        Connect to computer
      </ButtonPrimary>
    </Flex>
  );

  const $buttonsRunning = (
    <>
      <ButtonSecondary size="small">Remove agent</ButtonSecondary>
      <ButtonSecondary size="small">Stop sharing</ButtonSecondary>
    </>
  );

  const $running = (
    <Flex flexDirection="column" gap={4}>
      <Text>
        <icons.CircleCheck color="success" /> Agent running
      </Text>
      <Text>
        Cluster users with the role{' '}
        <strong>connect-my-computer-robert@acme.com</strong> can now connect to
        your computer as <strong>bob</strong>.
      </Text>
      <ButtonPrimary
        css={`
          align-self: flex-start;
        `}
      >
        Start ssh session
      </ButtonPrimary>
    </Flex>
  );

  return (
    <Box maxWidth="590px" width="100%" mx="auto" mt="4" px="5">
      <Flex justifyContent="space-between" mb="4" flexWrap="wrap" gap={2}>
        <Text typography="h3">Connect My Computer</Text>
        {/* <Flex gap="2">{$buttonsRunning}</Flex> */}
      </Flex>
      {/* {$running} */}
    </Box>
  );
}

type ProgressBarProps = {
  phases: {
    status: PhaseStatus;
    text: string;
  }[];
};

export type PhaseStatus = 'done' | 'processing' | 'waiting' | 'error';

export default function ProgressBar({ phases }: ProgressBarProps) {
  return (
    <Flex flexDirection="column">
      {phases.map((phase, index) => (
        <Flex
          py="12px"
          px="0"
          key={phase.text}
          style={{ position: 'relative' }}
        >
          <Phase status={phase.status} isLast={index === phases.length - 1} />
          {index + 1}. {phase.text}
        </Flex>
      ))}
    </Flex>
  );
}

function Phase({ status, isLast }) {
  let bg = 'action.disabledBackground';

  if (status === 'done') {
    bg = 'success';
  }

  if (status === 'error') {
    bg = 'error.main';
  }

  return (
    <>
      <StyledPhase mr="3" bg={bg}>
        <PhaseIcon status={status} />
      </StyledPhase>
      {!isLast && (
        <StyledLine
          color={status === 'done' ? 'success' : 'action.disabledBackground'}
        />
      )}
    </>
  );
}

const StyledPhase = styled(Box)`
  width: 24px;
  height: 24px;
  display: inline-block;
  position: relative;
  border-radius: 50%;

  span,
  div {
    position: absolute;
    top: 50%;
    right: 50%;
    transform: translate(50%, -50%);
  }
`;

const StyledLine = styled(Box)`
  width: 0px;
  height: 24px;
  position: absolute;
  left: 11px;
  bottom: -12px;
  border: 1px solid ${props => props.color};
`;

function PhaseIcon({ status }) {
  if (status === 'done') {
    return <icons.Check />;
  }

  if (status === 'error') {
    return <icons.Warning />;
  }

  if (status === 'processing') {
    return (
      <>
        <StyledIndicator fontSize="24px" style={{ top: 0, left: 0 }} />
        <icons.Restore />
      </>
    );
  }

  return (
    <Box
      borderRadius="50%"
      bg="action.disabledBackground"
      height="14px"
      width="14px"
    />
  );
}

const StyledIndicator = styled(Indicator)`
  color: ${props => props.theme.colors.success};
  margin: 0;
  opacity: 1;
`;
