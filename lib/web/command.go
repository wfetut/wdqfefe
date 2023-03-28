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

package web

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/httplib"
	"github.com/gravitational/trace"
	"github.com/julienschmidt/httprouter"
	"github.com/sashabaranov/go-openai"
)

func (h *Handler) postCommandComplete(_ http.ResponseWriter, r *http.Request, params httprouter.Params /*, sctx *SessionContext*/) (any, error) {
	var req client.CommandComplete
	if err := httplib.ReadJSON(r, &req); err != nil {
		return nil, trace.Wrap(err)
	}

	apiKey, err := os.ReadFile("/Users/jnyckowski/PycharmProjects/openai_test/apikey")
	if err != nil {
		return nil, trace.Wrap(err)
	}
	client := openai.NewClient(string(apiKey))
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: "You will be asked to connect to servers and for you to run commands on the servers. You will take the human input and translate it as follows. To connect to a server, you will return the command \"connect:{server_name}\". For other queries, you will return the Linux command in the format \"exec:{command}\". If the user does not say to run a command on a server, respond as usual.",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: req.Query,
				},
			},
		},
	)

	if err != nil {
		return nil, trace.Wrap(err)
	}

	lines := strings.Split(resp.Choices[0].Message.Content, "\n")
	var exec, production string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "exec"):
			exec = strings.TrimPrefix(line, "exec:")
		case strings.HasPrefix(line, "production"):
			production = strings.TrimPrefix(line, "production:")
		}
	}

	return struct {
		Response struct {
			Exec       string `json:"exec"`
			Production string `json:"production"`
		} `json:"response"`
	}{
		Response: struct {
			Exec       string `json:"exec"`
			Production string `json:"production"`
		}{
			Exec:       exec,
			Production: production,
		},
	}, nil
}

func (h *Handler) postCommand(_ http.ResponseWriter, r *http.Request, params httprouter.Params, sctx *SessionContext) (any, error) {
	var req client.CreateCommand
	if err := httplib.ReadJSON(r, &req); err != nil {
		return nil, trace.Wrap(err)
	}

	cl, err := sctx.GetClient()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	_, err = cl.UpsertCommand(r.Context(), &types.CommandV1{
		Kind: "command",
		Metadata: types.Metadata{
			Name:      req.Name,
			Namespace: "default",
		},
		Spec: types.CommandSpecV1{
			Interpreter:   "/bin/bash",
			Command:       req.Command,
			LabelSelector: nil,
		},
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return OK(), nil
}
