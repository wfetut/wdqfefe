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

package main

import (
	"fmt"
	"io"
	"sort"

	protossh "github.com/gravitational/teleport/api/gen/proto/go/teleport/ssh/v1"
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client"
)

func onListCommands(cf *CLIConf) error {
	if cf.ListAll {
		return trace.Wrap(listNodesAllClusters(cf))
	}

	tc, err := makeClient(cf, true)
	if err != nil {
		return trace.Wrap(err)
	}

	tc.AllowHeadless = true

	// Get list of all nodes in the backend and sort by "Node Name".
	var nodes []types.Command
	err = client.RetryWithRelogin(cf.Context, tc, func() error {
		nodes, err = tc.ListCommandsWithFilters(cf.Context)
		return err
	})
	if err != nil {
		return trace.Wrap(err)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].GetName() < nodes[j].GetName()
	})

	fmt.Println("Available commands:")
	for _, cmd := range nodes {
		fmt.Printf("%s\t%s\t%v\n", cmd.GetName(), cmd.GetCommand(), cmd.GetLabels())
	}

	return nil
}

func onExecCommand(cf *CLIConf) error {
	if cf.ExecCommand == "" {
		return trace.BadParameter("missing param")
	}
	tc, err := makeClient(cf, false)
	if err != nil {
		return trace.Wrap(err)
	}

	tc.AllowHeadless = true

	proxyGRPCClient, err := tc.NewCommandServiceClient(cf.Context)
	if err != nil {
		return trace.Wrap(err)
	}
	req := protossh.ExecuteRequest{
		CommandId: cf.ExecCommand,
		Labels:    tc.Labels,
	}

	logs, err := proxyGRPCClient.Execute(cf.Context, &req)
	if err != nil {
		return trace.Wrap(err)
	}
	defer logs.CloseSend()

	for {
		resp, err := logs.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return trace.Wrap(err)
		}

		fmt.Printf("%v\n", resp)
	}

	return nil
}
