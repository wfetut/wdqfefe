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
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/teleport/tool/common"
	"github.com/gravitational/trace"
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

	// Get list of all nodes in backend and sort by "Node Name".
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
	tc, err := makeClient(cf, false)
	if err != nil {
		return trace.Wrap(err)
	}

	tc.AllowHeadless = true

	tc.Stdin = os.Stdin
	err = retryWithAccessRequest(cf, tc, func() error {
		err = client.RetryWithRelogin(cf.Context, tc, func() error {
			commands, err := tc.ListCommandsWithFilters(cf.Context)
			if err != nil {
				return trace.Wrap(err)
			}

			var command types.Command
			for _, cmd := range commands {
				if cmd.GetName() == cf.ExecCommand {
					command = cmd
					break
				}
			}
			if command == nil {
				return trace.Errorf("command not found")
			}

			tc.Labels = map[string]string{
				"env": "example",
			}

			return tc.SSH(cf.Context, []string{command.GetCommand()}, cf.LocalExec)
		})
		if err != nil {
			if strings.Contains(utils.UserMessageFromError(err), teleport.NodeIsAmbiguous) {
				// Match on hostname or host ID, user could have given either
				expr := fmt.Sprintf(hostnameOrIDPredicateTemplate, tc.Host)
				tc.PredicateExpression = expr
				nodes, err := tc.ListNodesWithFilters(cf.Context)
				if err != nil {
					return trace.Wrap(err)
				}
				fmt.Fprintf(cf.Stderr(), "error: ambiguous host could match multiple nodes\n\n")
				printNodesAsText(cf.Stderr(), nodes, true)
				fmt.Fprintf(cf.Stderr(), "Hint: try addressing the node by unique id (ex: tsh ssh user@node-id)\n")
				fmt.Fprintf(cf.Stderr(), "Hint: use 'tsh ls -v' to list all nodes with their unique ids\n")
				fmt.Fprintf(cf.Stderr(), "\n")
				return trace.Wrap(&common.ExitCodeError{Code: 1})
			}
			return trace.Wrap(err)
		}
		return nil
	})
	// Exit with the same exit status as the failed command.
	if tc.ExitStatus != 0 {
		var exitErr *common.ExitCodeError
		if errors.As(err, &exitErr) {
			// Already have an exitCodeError, return that.
			return trace.Wrap(err)
		}
		if err != nil {
			// Print the error here so we don't lose it when returning the exitCodeError.
			fmt.Fprintln(os.Stderr, utils.UserMessageFromError(err))
		}
		err = &common.ExitCodeError{Code: tc.ExitStatus}
		return trace.Wrap(err)
	}
	return trace.Wrap(err)
}
