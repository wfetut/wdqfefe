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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/web"
	"github.com/gravitational/trace"
)

const (
	cookie = "__Host-grv_csrf=54034323553f2d32b48e39164cf72ea11505ffff951d82ba2e103bf873ce3f5d; __Host-session=7b2275736572223a22626f62222c22736964223a2264363535626136383137343636333532363635363365343337336565623735653538396538393236386238353636306131323431363138633231653532336537227d"
	auth   = "4379f858ad08b0d8ed72ebb14ed4f076859874ed5507244d1c6bf412ec3a4e2b"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	u := url.URL{
		Host:   "example.com:3080",
		Scheme: client.WSS,
		Path:   "/v1/webapi/command/example.com/execute",
	}

	requestData := web.CommandRequest{
		Command: "ls",
		//Login:   "ubuntu",
		Login: "jnyckowski",
		Labels: map[string]string{
			"env": "dev",
		},
		NodesID: []string{
			"e5a6d0b9-1fb0-4584-8fcb-16de60e513fa",
			"854e9299-c604-4af8-baa9-2580c4337a84",
		},
	}

	data, err := json.Marshal(requestData)
	if err != nil {
		log.Fatal(err)
	}

	q := u.Query()
	q.Set("params", string(data))
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{}
	dialer.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}

	header := http.Header{}
	header.Add("Origin", "https://example.com")
	header.Add("Authorization", "Bearer "+auth)
	header.Add("Cookie", cookie)

	ws, resp, err := dialer.Dial(u.String(), header)
	if err != nil {
		log.Fatal(trace.Wrap(err))
	}

	defer func() {
		ws.Close()
		resp.Body.Close()
	}()

	type payloadEnv struct {
		NodeID  string `json:"node_id"`
		Type    string `json:"type"`
		Payload []byte `json:"payload"`
	}
	for {
		ty, raw, err := ws.ReadMessage()
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}

		env := web.Envelope{}
		if err := proto.Unmarshal(raw, &env); err != nil {
			log.Fatal(err)
		}

		p := &payloadEnv{}
		if err := json.Unmarshal([]byte(env.Payload), p); err != nil {
			log.Print(err, env.Payload)
			continue
		}

		fmt.Printf("%v %v %s %s\n%s\n", ty, err, p.NodeID, p.Type, string(p.Payload))
	}
}
