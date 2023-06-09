/*
Copyright 2023 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/internalutils/stream"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/trace"
)

// ReconcileServerInfos periodically reconciles the labels of ServerInfo
// resources with their corresponding Teleport SSH servers.
func (s *Server) ReconcileServerInfos() error {
	ctx := s.CloseContext()
	const batchSize = 100

	for {
		// Iterate over nodes in batches.
		nodeStream := s.StreamNodes(ctx, defaults.Namespace)
		var nodes []types.Server
		moreNodes := true
		for moreNodes {
			nodes, moreNodes = stream.Take(nodeStream, batchSize)

			// Iterate over ServerInfos in batches.
			serverInfoStream := s.GetServerInfos(ctx)
			var serverInfos []types.ServerInfo
			moreServerInfos := true
			for moreServerInfos {
				serverInfos, moreServerInfos = stream.Take(serverInfoStream, batchSize)
				fmt.Println(nodes, serverInfos)

				select {
				case <-s.clock.After(time.Second):
					if err := s.processServerInfos(ctx, serverInfos, nodes); err != nil {
						return trace.Wrap(err)
					}
				case <-ctx.Done():
					return trace.Wrap(ctx.Err())
				}
			}
		}
	}
}

func (s *Server) processServerInfos(ctx context.Context, serverInfos []types.ServerInfo, nodes []types.Server) error {
	for _, si := range serverInfos {
		for _, node := range nodes {
			matchers := services.ServerMatchersFromServerInfo(si)
			if services.MatchServer(matchers, node) {
				fmt.Printf("matched si %v to node %v\n", si.GetName(), node.GetName())
				err := s.UpdateLabels(ctx, proto.InventoryUpdateLabelsRequest{
					ServerID: node.GetName(),
					Labels:   si.GetStaticLabels(),
				})
				if err != nil {
					if trace.IsNotFound(err) {
						log.WithError(err).Debugf("no control stream for server %v", node.GetName())
						break
					}
					return trace.Wrap(err)
				}
			}
		}
	}
	return nil
}
