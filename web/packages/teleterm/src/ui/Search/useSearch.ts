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
  SearchResult,
} from 'teleterm/ui/services/resources';
import { assertUnreachable } from 'teleterm/ui/utils';

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
    .map(term => term.toLowerCase());

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

  terms.forEach(term => {
    searchResult.resource.labelsList.forEach(label => {
      // indexOf is faster on Chrome than includes or regex.
      // https://jsbench.me/b7lf9kvrux/1
      const nameIndex = label.name.toLowerCase().indexOf(term);
      const valueIndex = label.value.toLowerCase().indexOf(term);

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
  });

  return { ...searchResult, labelMatches };
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

  return { ...searchResult, score: totalScore };
}
