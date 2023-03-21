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

import {
  SearchResultDatabase,
  SearchResultKube,
  SearchResultServer,
} from 'teleterm/ui/Search/searchResult';

type Base<T, R> = {
  kind: T;
  searchResult: R;
};

export type ActionSshConnect = Base<'action.ssh-connect', SearchResultServer>;
export type ActionDbConnect = Base<'action.db-connect', SearchResultDatabase>;
export type ActionKubeConnect = Base<'action.kube-connect', SearchResultKube>;
export type ActionSshLogin = Base<'action.ssh-login', { login: string }>;
export type ActionDbUsername = Base<'action.db-username', { username: string }>;

export type SearchBarPicker = {
  onFilter(value: string): SearchBarAction[] | Promise<SearchBarAction[]>;
  onPick(item: SearchBarAction): void;
  getPlaceholder(): string;
};

export type SearchBarAction =
  | ActionSshConnect
  | ActionDbConnect
  | ActionKubeConnect
  // to consider whether we want the "argument" items to be of the same type as regular search results
  | ActionSshLogin
  | ActionDbUsername;
