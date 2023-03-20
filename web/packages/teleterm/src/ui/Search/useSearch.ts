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

import { useCallback } from 'react';

import { useAppContext } from 'teleterm/ui/appContextProvider';
import {
  LabelMatch,
  ResourceMatch,
  SearchResult,
} from 'teleterm/ui/services/resources';
import { assertUnreachable } from 'teleterm/ui/utils';

import type * as types from 'teleterm/services/tshd/types';

/**
 * useSearch returns a function which searches for the given list of space-separated keywords across
 * all root and leaf clusters that the user is currently logged in to.
 *
 * It does so by issuing a separate request for each resource type to each cluster. It fails if any
 * of those requests fail.
 */
export function useSearch() {
  const { clustersService, resourcesService } = useAppContext();
  clustersService.useState();

  return useCallback(
    async (search: string) => {
      const connectedClusters = clustersService
        .getClusters()
        .filter(c => c.connected);
      const searchPromises = connectedClusters.map(cluster =>
        resourcesService.searchResources(cluster.uri, search)
      );

      return {
        results: (await Promise.all(searchPromises)).flat(),
        search,
      };
    },
    [clustersService, resourcesService]
  );
}

export function sortResults(
  searchResults: SearchResult[],
  search: string
): SearchResult[] {
  const terms = search
    .split(' ')
    .filter(Boolean)
    // We have to match the implementation of the search algorithm as closely as possible. It uses
    // strings.ToLower from Go which unfortunately doesn't have a good equivalent in JavaScript.
    //
    // strings.ToLower uses some kind of a universal map for lowercasing non-ASCII characters such
    // as the Turkish İ. JavaScript doesn't have such a function, possibly because it's not possible
    // to have universal case mapping. [1]
    //
    // The closest thing that JS has is toLocaleLowerCase. Since we don't know what locale the
    // search string uses, we let the runtime figure it out based on the system settings.
    // The assumption is that if someone has a resource with e.g. Turkish characters, their system
    // is set to the appropriate locale and the search results will be properly scored.
    //
    // Highlighting will have problems with some non-ASCII characters anyway because the library we
    // use for highlighting uses a regex with the i flag underneath.
    //
    // [1] https://web.archive.org/web/20190113111936/https://blogs.msdn.microsoft.com/oldnewthing/20030905-00/?p=42643
    .map(term => term.toLocaleLowerCase());

  // Highest score first.
  // TODO: Add displayed name as the tie breaker.
  return searchResults
    .map(searchResult => calculateScore(populateMatches(searchResult, terms)))
    .sort((a, b) => b.score - a.score);
}

function populateMatches(
  searchResult: SearchResult,
  terms: string[]
): SearchResult {
  const labelMatches: LabelMatch[] = [];
  const resourceMatches = [];

  terms.forEach(term => {
    searchResult.resource.labelsList.forEach(label => {
      // indexOf is faster on Chrome than includes or regex.
      // https://jsbench.me/b7lf9kvrux/1
      const nameIndex = label.name.toLocaleLowerCase().indexOf(term);
      const valueIndex = label.value.toLocaleLowerCase().indexOf(term);

      if (nameIndex >= 0) {
        labelMatches.push({
          kind: 'label-name',
          labelName: label.name,
          searchTerm: term,
        });
      }

      if (valueIndex >= 0) {
        labelMatches.push({
          kind: 'label-value',
          labelName: label.name,
          searchTerm: term,
        });
      }
    });

    switch (searchResult.kind) {
      case 'server': {
        // TODO(ravicious): Handle "tunnel" as address.
        ['name' as const, 'hostname' as const, 'addr' as const].forEach(
          field => {
            const index = searchResult.resource[field]
              .toLocaleLowerCase()
              .indexOf(term);

            if (index >= 0) {
              (resourceMatches as ResourceMatch<types.Server>[]).push({
                field,
                searchTerm: term,
              });
            }
          }
        );
        break;
      }
      case 'database': {
        // TODO(ravicious): Handle "cloud" as type.
        [
          'name' as const,
          'desc' as const,
          'protocol' as const,
          'type' as const,
        ].forEach(field => {
          const index = searchResult.resource[field]
            .toLocaleLowerCase()
            .indexOf(term);

          if (index >= 0) {
            (resourceMatches as ResourceMatch<types.Database>[]).push({
              field,
              searchTerm: term,
            });
          }
        });
        break;
      }
      case 'kube': {
        const index = searchResult.resource.name
          .toLocaleLowerCase()
          .indexOf(term);

        if (index >= 0) {
          (resourceMatches as ResourceMatch<types.Database>[]).push({
            field: 'name',
            searchTerm: term,
          });
        }
        break;
      }
      default: {
        assertUnreachable(searchResult);
      }
    }
  });

  return { ...searchResult, labelMatches, resourceMatches };
}

function calculateScore(searchResult: SearchResult): SearchResult {
  let totalScore = 0;

  for (const match of searchResult.labelMatches) {
    const { searchTerm } = match;
    switch (match.kind) {
      case 'label-name': {
        const label = searchResult.resource.labelsList.find(
          label => label.name === match.labelName
        );
        const score = Math.floor((searchTerm.length / label.name.length) * 100);
        totalScore += score;
        break;
      }
      case 'label-value': {
        const label = searchResult.resource.labelsList.find(
          label => label.name === match.labelName
        );
        const score = Math.floor(
          (searchTerm.length / label.value.length) * 100
        );
        totalScore += score;
        break;
      }
      default: {
        assertUnreachable(match.kind);
      }
    }
  }

  for (const match of searchResult.resourceMatches) {
    const { searchTerm } = match;

    const field = searchResult.resource[match.field];
    const score = Math.floor((searchTerm.length / field.length) * 100 * 2);
    totalScore += score;
  }

  return { ...searchResult, score: totalScore };
}
