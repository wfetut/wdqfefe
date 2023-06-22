import { UnifiedResource } from './types';

export function makeUnifiedResource(json: any): UnifiedResource {
  json = json || {};

  return {
    kind: json.kind,
    name: json.name,
    labels: json.tags ?? [],
  };
}
