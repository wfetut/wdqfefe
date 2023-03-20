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

import { Store, useStore } from 'shared/libs/stores';

import { CommandLauncher } from 'teleterm/ui/commandLauncher';

import { Database, Server } from 'teleterm/services/tshd/types';

import { ClustersService } from 'teleterm/ui/services/clusters';
import { ResourcesService } from 'teleterm/ui/services/resources';
import { WorkspacesService } from 'teleterm/ui/services/workspacesService';
import { ConnectionTrackerService } from 'teleterm/ui/services/connectionTracker';

import * as pickers from './suggestionPickers';
import { SearchBarPicker } from './types';
import { DbUsernamePicker, SshLoginPicker } from './suggestionPickers';

type State = {
  picker: SearchBarPicker;
  visible: boolean;
};

export class SearchBarService extends Store<State> {
  state: State = {
    picker: null,
    visible: false,
  };
  allResultsPicker: pickers.AllResultsPicker;
  getSshLoginPicker: (server: Server) => pickers.SshLoginPicker;
  getDbUsernamePicker: (database: Database) => pickers.DbUsernamePicker;
  lastFocused: WeakRef<HTMLElement>;

  constructor(
    private launcher: CommandLauncher,
    private clustersService: ClustersService,
    private resourcesService: ResourcesService,
    private workspacesService: WorkspacesService,
    private connectionTrackerService: ConnectionTrackerService
  ) {
    super();
    this.lastFocused = new WeakRef(document.createElement('div'));
    this.allResultsPicker = new pickers.AllResultsPicker(
      this.resourcesService,
      this.clustersService,
      this.launcher
    );
    this.getSshLoginPicker = (server: Server) =>
      new SshLoginPicker(server, this.clustersService, this.launcher);
    this.getDbUsernamePicker = (database: Database) =>
      new DbUsernamePicker(
        database,
        this.resourcesService,
        this.workspacesService,
        this.connectionTrackerService
      );

    this.setState({
      picker: this.allResultsPicker,
    });
  }

  goBack = () => {
    if (this.state.picker !== this.allResultsPicker) {
      this.setState({
        picker: this.allResultsPicker,
      });
      return;
    }

    this.setState({
      visible: false,
    });

    const el = this.lastFocused.deref();
    el?.focus();
  };

  show = (picker: SearchBarPicker = this.allResultsPicker) => {
    this.setState({
      picker,
      visible: true,
    });
  };

  hide = () => {
    this.setState({
      visible: false,
    });
  };

  revertDefaultAndHide = () => {
    this.setState({
      picker: this.allResultsPicker,
      visible: false,
    });
  };

  useState() {
    return useStore<SearchBarService>(this).state;
  }
}
