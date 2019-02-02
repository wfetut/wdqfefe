/*
Copyright 2019 Gravitational, Inc.

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

package common

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gravitational/teleport/lib/asciitable"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/service"
	"github.com/gravitational/teleport/lib/session"

	"github.com/gravitational/kingpin"
	"github.com/gravitational/trace"
)

// SessionsCommand implements `tctl token` group of commands.
type SessionsCommand struct {
	config *service.Config

	sessions *kingpin.CmdClause
}

// Initialize allows StatusCommand to plug itself into the CLI parser.
func (c *SessionsCommand) Initialize(app *kingpin.Application, config *service.Config) {
	c.config = config
	c.sessions = app.Command("sessions", "List all active sessions.")
}

// TryRun takes the CLI command as an argument (like "nodes ls") and executes it.
func (c *SessionsCommand) TryRun(cmd string, client auth.ClientI) (match bool, err error) {
	switch cmd {
	case c.sessions.FullCommand():
		err = c.Sessions(client)
	default:
		return false, nil
	}
	return true, trace.Wrap(err)
}

// Sessions is called to return a list of active sessions on the cluster.
func (c *SessionsCommand) Sessions(client auth.ClientI) error {
	clusterName, err := client.GetClusterName()
	if err != nil {
		return trace.Wrap(err)
	}

	sessions, err := client.GetSessions(defaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}
	if len(sessions) == 0 {
		fmt.Printf("No active sessions found in cluster %v.\n", clusterName.GetClusterName())
		return nil
	}

	t := asciitable.MakeTable([]string{"Session ID", "User(s)", "Node", "Created"})
	for _, s := range sessions {
		partyChunks := chunkParties(s.Parties, 2)
		for i, v := range partyChunks {
			var sessionID string
			var nodeName string
			var createdAt string

			if i == 0 {
				sessionID = s.ID.String()
				nodeName = ""
				createdAt = s.Created.Format(time.RFC3339)
			}
			t.AddRow([]string{sessionID, strings.Join(v, ", "), nodeName, createdAt})
		}
	}
	fmt.Println(t.AsBuffer().String())

	return nil
}

// chunkParties breaks parties list into sized chunks. Used to improve
// readability of "tsh sessions".
func chunkParties(parties []session.Party, chunkSize int) [][]string {
	// First sort party members so they always occur in the same order.
	sorted := make([]string, 0, len(parties))
	for _, v := range parties {
		sorted = append(sorted, v.User)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	// Then chunk labels into sized chunks.
	var chunks [][]string
	for chunkSize < len(sorted) {
		sorted, chunks = sorted[chunkSize:], append(chunks, sorted[0:chunkSize:chunkSize])
	}
	chunks = append(chunks, sorted)

	return chunks
}
