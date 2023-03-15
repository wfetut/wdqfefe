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
import { SearchResult } from 'teleterm/ui/services/resources';

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
  const labelMatches = [];

  terms.forEach(term => {
    searchResult.resource.labelsList.forEach(label => {
      const nameIndex = label.name.toLowerCase().indexOf(term);
      const valueIndex = label.value.toLowerCase().indexOf(term);

      if (nameIndex >= 0) {
        labelMatches.push({
          matchedValue: { kind: 'label-name', labelName: label.name },
          searchTerm: term,
          index: nameIndex,
        });
      }

      if (valueIndex >= 0) {
        labelMatches.push({
          matchedValue: { kind: 'label-value', labelName: label.name },
          searchTerm: term,
          index: valueIndex,
        });
      }
    });
  });

  return { ...searchResult, labelMatches };
}

function calculateScore(searchResult: SearchResult): SearchResult {
  let totalScore = 0;

  for (const match of searchResult.labelMatches) {
    const { matchedValue, searchTerm } = match;
    switch (matchedValue.kind) {
      case 'label-name': {
        const label = searchResult.resource.labelsList.find(
          label => label.name === matchedValue.labelName
        );
        const score = Math.floor((searchTerm.length / label.name.length) * 100);
        console.log('score', score, match);
        totalScore += score;
        continue;
      }
      case 'label-value': {
        const label = searchResult.resource.labelsList.find(
          label => label.name === matchedValue.labelName
        );
        const score = Math.floor(
          (searchTerm.length / label.value.length) * 100
        );
        console.log('score', score, match);
        totalScore += score;
        continue;
      }
    }
  }

  return { ...searchResult, score: totalScore };
}
