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

import { ClustersService } from 'teleterm/ui/services/clusters';
import { CommandLauncher } from 'teleterm/ui/commandLauncher';

import { ResourcesService } from 'teleterm/ui/services/resources';
import {
  Database,
  GatewayProtocol,
  Server,
} from 'teleterm/services/tshd/types';
import { searchResources } from 'teleterm/ui/Search/useSearch';

import { routing } from 'teleterm/ui/uri';
import { WorkspacesService } from 'teleterm/ui/services/workspacesService';
import { ConnectionTrackerService } from 'teleterm/ui/services/connectionTracker';

import {
  ActionSshLogin,
  SearchBarPicker,
  SearchBarAction,
  ActionDbUsername,
} from './types';

export class SshLoginPicker implements SearchBarPicker {
  constructor(
    private server: Server,
    private clustersService: ClustersService,
    private launcher: CommandLauncher
  ) {}

  stringToAction(s: string): ActionSshLogin {
    return {
      kind: 'action.ssh-login',
      searchResult: {
        login: s,
      },
    };
  }

  onFilter(value = '') {
    const loginsList = this.clustersService.findClusterByResource(
      this.server.uri
    )?.loggedInUser.sshLoginsList;

    const filtered = loginsList
      .filter(v => v.toLocaleLowerCase().includes(value.toLocaleLowerCase()))
      .map(this.stringToAction);

    if (value) {
      filtered.unshift(this.stringToAction(value));
    }
    return filtered;
  }

  getPlaceholder() {
    return 'Select login';
  }

  onPick(actionSshLogin: ActionSshLogin) {
    this.launcher.executeCommand('tsh-ssh', {
      localClusterUri: this.server.uri,
      loginHost: `${actionSshLogin.searchResult.login}@${this.server.hostname}`,
    });
  }
}

function getTargetUser(
  protocol: GatewayProtocol,
  providedDbUser: string
): string {
  // we are replicating tsh behavior (user can be omitted for Redis)
  // https://github.com/gravitational/teleport/blob/796e37bdbc1cb6e0a93b07115ffefa0e6922c529/tool/tsh/db.go#L240-L244
  // but unlike tsh, Connect has to provide a user that is then used in a gateway document
  if (protocol === 'redis') {
    return providedDbUser || 'default';
  }

  return providedDbUser;
}

export class DbUsernamePicker implements SearchBarPicker {
  constructor(
    private database: Database,
    private resourcesService: ResourcesService,
    private workspacesService: WorkspacesService,
    private connectionTrackerService: ConnectionTrackerService
  ) {}

  stringToAction(s: string): ActionDbUsername {
    return {
      kind: 'action.db-username',
      searchResult: {
        username: s,
      },
    };
  }

  async onFilter(value = '') {
    const dbUsers = await this.resourcesService.getDbUsers(this.database.uri);

    const filtered = dbUsers
      .filter(v => v.toLocaleLowerCase().includes(value.toLocaleLowerCase()))
      .map(this.stringToAction);

    if (value) {
      filtered.unshift(this.stringToAction(value));
    }
    return filtered;
  }

  getPlaceholder() {
    return 'Select database username';
  }

  onPick(actionDbUsername: ActionDbUsername) {
    const rootClusterUri = routing.ensureRootClusterUri(this.database.uri);
    const documentsService =
      this.workspacesService.getWorkspaceDocumentService(rootClusterUri);

    const doc = documentsService.createGatewayDocument({
      // Not passing the `gatewayUri` field here, as at this point the gateway doesn't exist yet.
      // `port` is not passed as well, we'll let the tsh daemon pick a random one.
      targetUri: this.database.uri,
      targetName: this.database.name,
      targetUser: getTargetUser(
        this.database.protocol as GatewayProtocol,
        actionDbUsername.searchResult.username
      ),
    });

    const connectionToReuse =
      this.connectionTrackerService.findConnectionByDocument(doc);

    if (connectionToReuse) {
      this.connectionTrackerService.activateItem(connectionToReuse.id);
    } else {
      documentsService.add(doc);
      documentsService.open(doc.uri);
    }
  }
}

export class AllResultsPicker implements SearchBarPicker {
  constructor(
    private resourceService: ResourcesService,
    private clustersService: ClustersService,
    private commandLauncher: CommandLauncher
  ) {}

  async onFilter(value: string) {
    if (!value) {
      return [];
    }
    const res = await searchResources(
      this.clustersService,
      this.resourceService,
      value
    );
    // mapping search results to actions, but I'm not sure about it
    // maye we can just have search results?
    return res.map(searchResult => {
      switch (searchResult.kind) {
        case 'server': {
          return {
            kind: 'action.ssh-connect' as const,
            searchResult,
          };
        }
        case 'kube': {
          return {
            kind: 'action.kube-connect' as const,
            searchResult,
          };
        }
        case 'database': {
          return {
            kind: 'action.db-connect' as const,
            searchResult,
          };
        }
      }
    });
  }

  getPlaceholder() {
    return 'Search for something';
  }

  onPick(item: SearchBarAction) {
    this.commandLauncher.executeSearchAction(item);
  }
}
