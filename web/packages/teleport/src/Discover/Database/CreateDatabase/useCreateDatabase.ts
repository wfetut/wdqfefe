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
import { useEffect, useState } from 'react';

import useAttempt from 'shared/hooks/useAttemptNext';
import { getErrMessage } from 'shared/utils/errorType';

import useTeleport from 'teleport/useTeleport';
import { useDiscover } from 'teleport/Discover/useDiscover';
import { usePoll } from 'teleport/Discover/Shared/usePoll';
import { compareByString } from 'teleport/lib/util';
import { ApiError } from 'teleport/services/api/parseError';

import { matchLabels } from '../util';

import type {
  CreateDatabaseRequest,
  Database as DatabaseResource,
  DatabaseService,
} from 'teleport/services/databases';
import type { AgentLabel } from 'teleport/services/agents';
import type { DbMeta } from 'teleport/Discover/useDiscover';

export const WAITING_TIMEOUT = 30000; // 30 seconds

export function useCreateDatabase() {
  const ctx = useTeleport();
  const clusterId = ctx.storeUser.getClusterId();
  const { attempt, setAttempt } = useAttempt('');
  const {
    emitErrorEvent,
    updateAgentMeta,
    agentMeta,
    nextStep,
    prevStep,
    resourceSpec,
  } = useDiscover();

  // isDbCreateErr is a flag that indicates
  // attempt failed from trying to create a database.
  const [isDbCreateErr, setIsDbCreateErr] = useState(false);

  const [pollTimeout, setPollTimeout] = useState(0);
  const [pollActive, setPollActive] = useState(false);
  const [timedOut, setTimedOut] = useState(false);

  // Required persisted states to determine if we can skip a request
  // because there can be multiple failed points:
  //  1) failed to create database (network, duplicate name, incorrect format etc)
  //  2) failed to fetch db services (probably mostly network issues)
  //  3) failed to query if there was a matching database service:
  //    - timed out due to combined previous requests taking longer than WAITING_TIMEOUT
  //    - timed out due to failure to query (this would most likely be some kind of
  //      backend error or network failure)
  const [createdDb, setCreatedDb] = useState<CreateDatabaseRequest>();

  const dbPollingResult = usePoll<DatabaseResource>(
    signal => fetchDatabaseServer(signal),
    pollActive, // does not poll on init, since the value is false.
    3000 // interval: poll every 3 seconds
  );

  // Handles setting a timeout when polling becomes active.
  useEffect(() => {
    if (pollActive && pollTimeout > Date.now()) {
      const id = window.setTimeout(() => {
        setTimedOut(true);
      }, pollTimeout - Date.now());

      return () => clearTimeout(id);
    }
  }, [pollActive, pollTimeout]);

  // Handles polling timeout.
  useEffect(() => {
    if (timedOut) {
      // reset timer fields and set errors.
      setPollTimeout(null);
      setPollActive(false);
      setTimedOut(false);
      setAttempt({
        status: 'failed',
        statusText:
          'Teleport could not detect your new database in time. Please try again.',
      });
      emitErrorEvent(
        `timeout polling for new database with an existing service`
      );
    }
  }, [timedOut]);

  // Handles when polling successfully gets
  // a response.
  useEffect(() => {
    if (!dbPollingResult) return;

    setPollTimeout(null);
    setPollActive(false);

    updateAgentMeta({
      ...(agentMeta as DbMeta),
      resourceName: createdDb.name,
      agentMatcherLabels: dbPollingResult.labels,
      db: dbPollingResult,
    });

    setAttempt({ status: 'success' });
  }, [dbPollingResult]);

  function fetchDatabaseServer(signal: AbortSignal) {
    const request = {
      search: createdDb.name,
      limit: 1,
    };
    return ctx.databaseService
      .fetchDatabases(clusterId, request, signal)
      .then(res => {
        if (res.agents.length) {
          return res.agents[0];
        }
        return null;
      });
  }

  function fetchDatabaseServers(query: string, limit: number) {
    const request = {
      query,
      limit,
    };
    return ctx.databaseService.fetchDatabases(clusterId, request);
  }

  async function registerDatabase(db: CreateDatabaseRequest, newDb = false) {
    // Set the timeout now, because this entire registering process
    // should take less than WAITING_TIMEOUT.
    setPollTimeout(Date.now() + WAITING_TIMEOUT);
    setAttempt({ status: 'processing' });
    setIsDbCreateErr(false);

    // Attempt creating a new Database resource.
    if (!createdDb || newDb) {
      try {
        await ctx.databaseService.createDatabase(clusterId, db);
        setCreatedDb(db);
      } catch (err) {
        // Check if the error is a result of an existing database.
        if (err instanceof ApiError) {
          if (err.response.status === 409) {
            const isAwsRds = Boolean(db.awsRdsDb && db.awsRdsDb.accountId);
            return attemptDbServerQueryAndBuildErrMsg(db.name, isAwsRds);
          }
        }
        handleRequestError(err, 'failed to create database: ');
        setIsDbCreateErr(true);
        return;
      }
    }

    // Check and see if database resource need to be updated.
    if (!newDb && requiresDbUpdate(db)) {
      try {
        await ctx.databaseService.updateDatabase(clusterId, {
          ...db,
        });
        setCreatedDb(db);
      } catch (err) {
        handleRequestError(err, 'failed to update database: ');
        return;
      }
    }

    // See if this new database can be picked up by an existing
    // database service. If there is no active database service,
    // user is led to the next step.
    try {
      const { services } = await ctx.databaseService.fetchDatabaseServices(
        clusterId
      );

      if (!findActiveDatabaseSvc(db.labels, services)) {
        updateAgentMeta({
          ...(agentMeta as DbMeta),
          resourceName: db.name,
          agentMatcherLabels: db.labels,
          awsRdsDb: db.awsRdsDb,
        });
        setAttempt({ status: 'success' });
        return;
      }
    } catch (err) {
      handleRequestError(err, 'failed to fetch database services: ');
      return;
    }

    // Start polling until new database is picked up by an
    // existing database service.
    setPollActive(true);
  }

  // attemptDbServerQueryAndBuildErrMsg tests if the duplicated `dbName`
  // (determined by an error returned from the initial register db attempt)
  // is already a part of the cluster by querying for its db server.
  // This is an attempt to provide accurate actionable steps for the
  // user.
  async function attemptDbServerQueryAndBuildErrMsg(
    dbName: string,
    isAwsRds = false
  ) {
    const preErrMsg = 'failed to register database: ';
    const nonAwsMsg = `use a different name and try again`;
    const awsMsg = `change (or define) the value of the \
    tag "teleport.dev/database_name" on the RDS instance and try again`;

    try {
      await ctx.databaseService.fetchDatabase(clusterId, dbName);
      let message = `a database with the name "${dbName}" is already \
      a part of this cluster, ${isAwsRds ? awsMsg : nonAwsMsg}`;
      handleRequestError(new Error(message), preErrMsg);
    } catch (e) {
      // No database server were found for the database name.
      if (e instanceof ApiError) {
        if (e.response.status === 404) {
          let message = `a database with the name "${dbName}" already exists \
          but there are no database servers for it, you can remove this \
          database using the command, “tctl rm db/${dbName}”, or ${
            isAwsRds ? awsMsg : nonAwsMsg
          }`;
          handleRequestError(new Error(message), preErrMsg);
        }
        return;
      }

      // Display other errors as is.
      handleRequestError(e, preErrMsg);
    }
    setIsDbCreateErr(true);
  }

  function requiresDbUpdate(db: CreateDatabaseRequest) {
    if (!createdDb) {
      return false;
    }

    if (createdDb.labels.length === db.labels.length) {
      // Sort by label keys.
      const a = createdDb.labels.sort((a, b) =>
        compareByString(a.name, b.name)
      );
      const b = db.labels.sort((a, b) => compareByString(a.name, b.name));

      for (let i = 0; i < a.length; i++) {
        if (JSON.stringify(a[i]) !== JSON.stringify(b[i])) {
          return true;
        }
      }
    }

    return (
      createdDb.uri !== db.uri ||
      createdDb.awsRdsDb?.accountId !== db.awsRdsDb?.accountId ||
      createdDb.awsRdsDb?.resourceId !== db.awsRdsDb?.resourceId
    );
  }

  function clearAttempt() {
    setAttempt({ status: '' });
  }

  function handleRequestError(err: Error, preErrMsg = '') {
    const message = getErrMessage(err);
    setAttempt({ status: 'failed', statusText: `${preErrMsg}${message}` });
    emitErrorEvent(`${preErrMsg}${message}`);
  }

  const access = ctx.storeUser.getDatabaseAccess();
  return {
    createdDb,
    attempt,
    clearAttempt,
    registerDatabase,
    fetchDatabaseServers,
    canCreateDatabase: access.create,
    pollTimeout,
    dbEngine: resourceSpec.dbMeta.engine,
    dbLocation: resourceSpec.dbMeta.location,
    isDbCreateErr,
    prevStep,
    // If there was a result from database polling, then
    // allow user to skip the next step.
    nextStep: dbPollingResult ? () => nextStep(2) : () => nextStep(),
  };
}

export type State = ReturnType<typeof useCreateDatabase>;

export function findActiveDatabaseSvc(
  newDbLabels: AgentLabel[],
  dbServices: DatabaseService[]
) {
  if (!dbServices.length) {
    return null;
  }

  for (let i = 0; i < dbServices.length; i++) {
    // Loop through the current service label keys and its value set.
    const currService = dbServices[i];
    const match = matchLabels(newDbLabels, currService.matcherLabels);

    if (match) {
      return currService;
    }
  }

  return null;
}
