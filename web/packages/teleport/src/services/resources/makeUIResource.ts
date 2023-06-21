import { UIResource } from './types';

export function makeUIResource(json: any): UIResource {
  json = json || {};

  return {
    kind: json.kind,
    name: json.name,
    labels: json.tags ?? [],
  };
}
