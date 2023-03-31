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
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/gorilla/websocket"
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/lib/client"
)

const (
	cookie = "__Host-grv_csrf=54034323553f2d32b48e39164cf72ea11505ffff951d82ba2e103bf873ce3f5d; __Host-session=7b2275736572223a22626f62222c22736964223a2236636335663462373963363435363561363939613134333236303033356664636136626130386433643036366363663536663130373566653737336664303866227d"
	auth   = "039950ffc915f4990cb045875878c735284fd441ce49706ed0ce3fc74274367c"
)

type queryMessage struct {
	Message string `json:"message"`
}

func main() {
	u := url.URL{
		Host:   "example.com:3080",
		Scheme: client.WSS,
		Path:   "/v1/webapi/assistant",
	}

	if len(os.Args) > 1 {
		conversationID := os.Args[1]

		q := u.Query()
		q.Set("conversation_id", conversationID)
		u.RawQuery = q.Encode()
	}

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

	if err := ws.WriteJSON(&queryMessage{Message: "sample query"}); err != nil {
		log.Fatal(err)
	}

	for {
		ty, raw, err := ws.ReadMessage()
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}

		fmt.Printf("%v %v %v\n", ty, err, string(raw))
	}
}
