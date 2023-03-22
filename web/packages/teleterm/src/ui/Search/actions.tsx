import { IAppContext } from 'teleterm/ui/types';
import { routing } from 'teleterm/ui/uri';
import { GatewayProtocol } from 'teleterm/services/tshd/types';
import { SearchResult } from 'teleterm/ui/Search/searchResult';

import { SearchAction } from './types';

export function mapToActions(
  ctx: IAppContext,
  searchResults: SearchResult[]
): SearchAction[] {
  return searchResults.map(result => {
    if (result.kind === 'server') {
      return {
        type: 'parametrized-action',
        searchResult: result,
        parameter: {
          getSuggestions: async () =>
            ctx.clustersService.findClusterByResource(result.resource.uri)
              ?.loggedInUser?.sshLoginsList,
          placeholder: 'Provide login',
        },
        perform(login) {
          ctx.commandLauncher.executeCommand('tsh-ssh', {
            localClusterUri: result.resource.uri,
            loginHost: `${login}@${result.resource.hostname}`,
          });
        },
      };
    }
    if (result.kind === 'kube') {
      return {
        type: 'simple-action',
        searchResult: result,
        perform() {
          ctx.commandLauncher.executeCommand('kube-connect', {
            kubeUri: result.resource.uri,
          });
        },
      };
    }
    if (result.kind === 'database') {
      return {
        type: 'parametrized-action',
        searchResult: result,
        parameter: {
          getSuggestions: () =>
            ctx.resourcesService.getDbUsers(result.resource.uri),
          placeholder: 'Provide db username',
        },
        perform(dbUsername) {
          const rootClusterUri = routing.ensureRootClusterUri(
            result.resource.uri
          );
          const documentsService =
            ctx.workspacesService.getWorkspaceDocumentService(rootClusterUri);

          const doc = documentsService.createGatewayDocument({
            // Not passing the `gatewayUri` field here, as at this point the gateway doesn't exist yet.
            // `port` is not passed as well, we'll let the tsh daemon pick a random one.
            targetUri: result.resource.uri,
            targetName: result.resource.name,
            targetUser: getTargetUser(
              result.resource.protocol as GatewayProtocol,
              dbUsername
            ),
          });

          const connectionToReuse =
            ctx.connectionTracker.findConnectionByDocument(doc);

          if (connectionToReuse) {
            ctx.connectionTracker.activateItem(connectionToReuse.id);
          } else {
            documentsService.add(doc);
            documentsService.open(doc.uri);
          }
        },
      };
    }
  });
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
