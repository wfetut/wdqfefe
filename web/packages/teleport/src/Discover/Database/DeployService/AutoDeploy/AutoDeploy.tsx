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

import React, { useState, useEffect } from 'react';
import styled from 'styled-components';
import { Box, ButtonSecondary, Link, Text } from 'design';
import * as Icons from 'design/Icon';
import FieldInput from 'shared/components/FieldInput';
import Validation, { Validator } from 'shared/components/Validation';
import { requiredField } from 'shared/components/Validation/rules';
import useAttempt from 'shared/hooks/useAttemptNext';

import { TextSelectCopyMulti } from 'teleport/components/TextSelectCopy';
import { usePingTeleport } from 'teleport/Discover/Shared/PingTeleportContext';
import {
  HintBox,
  SuccessBox,
  WaitingInfo,
} from 'teleport/Discover/Shared/HintBox';
import {
  AwsOidcDeployServiceResponse,
  integrationService,
} from 'teleport/services/integrations';
import { useDiscover, type DbMeta } from 'teleport/Discover/useDiscover';
import { DiscoverEventStatus } from 'teleport/services/userEvent';

import {
  ActionButtons,
  HeaderSubtitle,
  TextIcon,
  useShowHint,
  Header,
  DiscoverLabel,
  AlternateInstructionButton,
} from '../../../Shared';

import { DeployServiceProp } from '../DeployService';
import { hasMatchingLabels, Labels } from '../../common';

import type { Database } from 'teleport/services/databases';

export function AutoDeploy({ toggleDeployMethod }: Partial<DeployServiceProp>) {
  const { emitErrorEvent, nextStep, emitEvent, agentMeta } = useDiscover();
  const { attempt, setAttempt } = useAttempt('');
  const [showLabelMatchErr, setShowLabelMatchErr] = useState(true);

  const [taskRoleArn, setTaskRoleArn] = useState('LisaKimTestDeployService');
  const [deploySvcResp, setDeploySvcResp] =
    useState<AwsOidcDeployServiceResponse>();
  const [deployFinished, setDeployFinished] = useState(false);

  const hasDbLabels = agentMeta?.agentMatcherLabels?.length;
  const dbLabels = hasDbLabels ? agentMeta.agentMatcherLabels : [];
  const [labels, setLabels] = useState<DiscoverLabel[]>([
    { name: '*', value: '*', isFixed: dbLabels.length === 0 },
  ]);

  useEffect(() => {
    // Turn off error once user changes labels.
    if (showLabelMatchErr) {
      setShowLabelMatchErr(false);
    }
  }, [labels]);

  function handleDeploy(validator: Validator) {
    if (!validator.validate()) {
      return;
    }

    if (!hasMatchingLabels(dbLabels, labels)) {
      setShowLabelMatchErr(true);
      return;
    }

    setShowLabelMatchErr(false);
    setAttempt({ status: 'processing' });
    const agent = agentMeta as DbMeta;
    integrationService
      .deployAwsOidcService(agent.integrationName, {
        deploymentMode: 'database-service',
        region: agent.awsRdsDb?.region,
        subnetIds: agent.awsRdsDb?.subnets,
        taskRoleArn,
        databaseAgentMatcherLabels: labels,
      })
      .then(setDeploySvcResp)
      .catch((err: Error) => {
        setAttempt({ status: 'failed', statusText: err.message });
        emitErrorEvent(`deploy request failed: ${err.message}`);
      });
  }

  function handleOnProceed() {
    nextStep(2); // skip the IAM policy view
    emitEvent(
      { stepStatus: DiscoverEventStatus.Success },
      { deployMethod: 'auto' } // TODO(lisa)
    );
  }

  function handleDeployFinished() {
    setDeployFinished(true);
  }

  function abortDeploying() {
    emitErrorEvent(`aborted in middle of deploying (>= 5 minutes of waiting)`);
    toggleDeployMethod();
  }

  const isProcessing = attempt.status === 'processing' && !!deploySvcResp;
  const isDeploying = isProcessing && !!deploySvcResp;
  const hasError = attempt.status === 'failed';

  return (
    <Box>
      <Validation>
        {({ validator }) => (
          <>
            <Heading toggleDeployMethod={toggleDeployMethod} />

            {/* step one */}
            <CreateAccessRole
              taskRoleArn={taskRoleArn}
              setTaskRoleArn={setTaskRoleArn}
              disabled={isProcessing}
            />

            {/* step two */}
            <StyledBox mb={5}>
              <Text bold>Step 2</Text>
              <Box mb={4}>
                <Labels
                  labels={labels}
                  setLabels={setLabels}
                  disableBtns={isProcessing}
                  showLabelMatchErr={showLabelMatchErr}
                  dbLabels={dbLabels}
                />
              </Box>
              <ButtonSecondary
                width="215px"
                type="submit"
                onClick={() => handleDeploy(validator)}
                disabled={isProcessing}
                mb={2}
              >
                Deploy Teleport Service
              </ButtonSecondary>
              {hasError && (
                <Box>
                  <TextIcon mt={3}>
                    <Icons.Warning ml={1} color="error.main" />
                    Encountered Error: {attempt.statusText}
                  </TextIcon>
                </Box>
              )}
            </StyledBox>

            {/* step three */}
            {isDeploying && (
              <DeployHints
                deployFinished={handleDeployFinished}
                resourceName={(agentMeta as DbMeta).resourceName}
                abortDeploying={abortDeploying}
                deploySvcResp={deploySvcResp}
              />
            )}

            <ActionButtons
              onProceed={handleOnProceed}
              disableProceed={!deployFinished}
            />
          </>
        )}
      </Validation>
    </Box>
  );
}

const Heading = ({ toggleDeployMethod }: { toggleDeployMethod(): void }) => {
  return (
    <>
      <Header>Automatically Deploy a Database Service</Header>
      <HeaderSubtitle>
        Teleport needs an agent to be able to connect to your database. With a
        few permission configurations, Teleport can spin up an ECS Fargate
        container (0.xxx vCPU, 1GB memory) in your Amazon account with the
        ability to access databases in this region. You will only need to do
        this once for all databases per geographical region. <br />
        <br />
        Want to deploy an agent manually from one of your existing servers?{' '}
        <AlternateInstructionButton onClick={toggleDeployMethod} />
      </HeaderSubtitle>
    </>
  );
};

const CreateAccessRole = ({
  taskRoleArn,
  setTaskRoleArn,
  disabled,
}: {
  taskRoleArn: string;
  setTaskRoleArn(r: string): void;
  disabled: boolean;
}) => {
  return (
    <StyledBox mb={5}>
      <Text bold>Step 1</Text>
      <Text mb={2}>Create an Access Role for the Database Service</Text>
      <FieldInput
        mb={4}
        disabled={disabled}
        rule={requiredField('Task Role ARN is required')}
        label="Name a Task Role ARN"
        autoFocus
        value={taskRoleArn}
        placeholder="teleport"
        width="400px"
        mr="3"
        onChange={e => setTaskRoleArn(e.target.value)}
        toolTipContent="Lorem ipsume dolores"
      />
      <Text mb={2}>
        Then open{' '}
        <Link
          href="https://console.aws.amazon.com/cloudshell/home"
          target="_blank"
        >
          Amazon CloudShell
        </Link>{' '}
        and copy/paste the following command to create an access role for your
        database service:
      </Text>
      <Box mb={2}>
        <TextSelectCopyMulti
          lines={[
            {
              text: '$ sudo bash -c "$(curl -fsSL https://kenny-r-test.teleport.sh/scripts/40884566df6fbdb02411364e641f78b2/set-up-aws-role.sh)"',
            },
          ]}
        />
      </Box>
    </StyledBox>
  );
};

const DeployHints = ({
  resourceName,
  deployFinished,
  abortDeploying,
  deploySvcResp,
}: {
  resourceName: string;
  deployFinished(): void;
  abortDeploying(): void;
  deploySvcResp: AwsOidcDeployServiceResponse;
}) => {
  // Starts resource querying interval.
  const { result, active } = usePingTeleport<Database>(resourceName);

  const showHint = useShowHint(active);

  useEffect(() => {
    if (result) {
      deployFinished();
    }
  }, [result]);

  let hint;
  if (showHint && !result) {
    hint = (
      <HintBox header="We're still in the process of creating your Database Service">
        <Text mb={3}>
          The network may be slow. Try continuing to wait for a few more minutes
          or{' '}
          <AlternateInstructionButton onClick={abortDeploying}>
            try manually deploying your own service.
          </AlternateInstructionButton>
        </Text>
        <Text mb={3}>
          The network may be slow. Try continuing to wait for a few more minutes
          or{' '}
          <AlternateInstructionButton onClick={abortDeploying}>
            try manually deploying your own service.
          </AlternateInstructionButton>
        </Text>
      </HintBox>
    );
  } else if (result) {
    hint = (
      <SuccessBox>
        Successfully created and detected your new Database Service.
      </SuccessBox>
    );
  } else {
    hint = (
      <WaitingInfo>
        <TextIcon
          css={`
            white-space: pre;
            margin-right: 4px;
            padding-right: 4px;
          `}
        >
          <Icons.Restore fontSize={4} />
        </TextIcon>
        Teleport is currently deploying a Database Service. It will take at
        least a minute for the Database Service to be created and joined to your
        cluster. <br />
        We will update this status once detected, meanwhile visit your AWS{' '}
        <Link target="_blank" href={deploySvcResp.serviceDashboardUrl}>
          dashboard
        </Link>{' '}
        to see progress details.
      </WaitingInfo>
    );
  }

  return <>{hint}</>;
};

const StyledBox = styled(Box)`
  max-width: 1000px;
  background-color: ${props => props.theme.colors.spotBackground[0]};
  padding: ${props => `${props.theme.space[3]}px`};
  border-radius: ${props => `${props.theme.space[2]}px`};
`;
