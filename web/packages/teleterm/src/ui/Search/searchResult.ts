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

import type * as resourcesServiceTypes from 'teleterm/ui/services/resources';
import type { SearchResultResource } from 'teleterm/ui/services/resources';

export { SearchResultResource };

type SearchResultBase<Result extends resourcesServiceTypes.SearchResult> =
  Result & {
    labelMatches: LabelMatch[];
    resourceMatches: ResourceMatch<Result['resource']>[];
    score: number;
  };

export type SearchResultServer =
  SearchResultBase<resourcesServiceTypes.SearchResultServer>;
export type SearchResultDatabase =
  SearchResultBase<resourcesServiceTypes.SearchResultDatabase>;
export type SearchResultKube =
  SearchResultBase<resourcesServiceTypes.SearchResultKube>;
export type SearchResult =
  | SearchResultServer
  | SearchResultDatabase
  | SearchResultKube;

export type LabelMatch = {
  kind: 'label-name' | 'label-value';
  labelName: string;
  searchTerm: string;
};

export type ResourceMatch<Resource extends SearchResult['resource']> = {
  field: keyof Resource;
  searchTerm: string;
};

/**
 * mainResourceName returns the main identifier for the given resource displayed in the UI.
 */
export function mainResourceName(searchResult: SearchResult): string {
  return searchResult.resource[mainResourceField[searchResult.kind]];
}

export const mainResourceField: {
  [Kind in SearchResult['kind']]: keyof SearchResultResource<Kind>;
} = {
  server: 'hostname',
  database: 'name',
  kube: 'name',
} as const;

// The usage of Exclude here is a workaround to make sure that the fields in the array point only to
// fields of string type.
export const searchableFields: {
  [Kind in SearchResult['kind']]: ReadonlyArray<
    Exclude<keyof SearchResultResource<Kind>, 'labelsList'>
  >;
} = {
  server: ['name', 'hostname', 'addr'],
  database: ['name', 'desc', 'protocol', 'type'],
  kube: ['name'],
} as const;
