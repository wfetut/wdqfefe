import { ReactElement } from 'react';

import { SearchResult } from 'teleterm/ui/Search/searchResult';

export interface SimpleAction {
  type: 'simple-action';
  searchResult: SearchResult;

  perform(): void;
}

export interface ParametrizedAction {
  type: 'parametrized-action';
  searchResult: SearchResult;
  parameter: {
    getSuggestions(): Promise<string[]>;
    placeholder: string;
  };

  perform(parameter: string): void;
}

export type SearchAction = SimpleAction | ParametrizedAction;

export interface SearchPicker {
  picker: ReactElement;
  placeholder: string;
}
