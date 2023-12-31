/*
Copyright 2021 Gravitational, Inc.

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

// Example Allow List based access plugin.
//
// This plugin approves/denies access requests based on a simple Allow List
// of usernames and associated allowed roles. Requests from allowed users
// for allowed roles are approved, all others are denied.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils"

	"github.com/gravitational/trace"
	"github.com/pelletier/go-toml"
)

type config struct {
	// PluginName is used to associate events and stored plugin data with a plugin
	PluginName string `toml:"plugin_name"`
	// Addr is the address used to connect to your Teleport Auth server. This can
	// be the auth, proxy, or tunnel proxy address.
	Addr string `toml:"addr"`
	// IdentityFile is used to authenticate a connection to the Teleport
	// Auth server and authorize client requests.
	IdentityFile string `toml:"identity_file"`
	// AllowList is a list of users to automatically approve access requests for.
	AllowList []string `toml:"allow_list"`
}

func loadConfig(filepath string) (*config, error) {
	t, err := toml.LoadFile(filepath)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	conf := &config{}
	if err := t.Unmarshal(conf); err != nil {
		return nil, trace.Wrap(err)
	}
	return conf, nil
}

func main() {
	ctx := context.Background()

	cfg, err := loadConfig("config.toml")
	if err != nil {
		log.Fatalf("ERROR: %s", err)
	}

	if err := run(ctx, cfg); err != nil {
		log.Fatalf("ERROR: %s", err)
	}
}

func run(ctx context.Context, cfg *config) error {
	client, err := client.New(ctx, client.Config{
		Addrs: []string{cfg.Addr},
		Credentials: []client.Credentials{
			client.LoadIdentityFile(cfg.IdentityFile),
		},
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer client.Close()

	filter := types.AccessRequestFilter{
		State: types.RequestState_PENDING,
	}

	// Register a watcher for pending access requests.
	watcher, err := client.NewWatcher(ctx, types.Watch{
		Kinds: []types.WatchKind{{
			Kind:   types.KindAccessRequest,
			Filter: filter.IntoMap(),
		}},
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer watcher.Close()

	for {
		select {
		case event := <-watcher.Events():
			switch event.Type {
			case types.OpInit:
				log.Printf("watcher initialized...")
			case types.OpPut:
				// OpPut indicates that a request has been created or updated. Since we specified
				// StatePending in our filter, only pending requests should appear here.
				log.Printf("Handling request: %+v", event.Resource)

				req, ok := event.Resource.(*types.AccessRequestV3)
				if !ok {
					return trace.BadParameter("unexpected resource type %T", event.Resource)
				}

				// Gather AccessRequestUpdate params
				params := types.AccessRequestUpdate{
					RequestID: req.GetName(),
					Annotations: map[string][]string{
						"strategy": {"allow_list"},
					},
				}

				// Searching through allowList for a user match
				// and update the request state accordingly.
				allowed := false
				for _, user := range cfg.AllowList {
					if req.GetUser() == user {
						allowed = true
						break
					}
				}
				if allowed {
					log.Printf("User %q in Allow List, approving request...", req.GetUser())
					params.State = types.RequestState_APPROVED
					params.Reason = "user in Allow List"
				} else {
					log.Printf("User %q not in Allow List, denying request...", req.GetUser())
					params.State = types.RequestState_DENIED
					params.Reason = "user not in Allow List"
				}

				// Set delegator so that the event generated by setting the access
				// request state has an event field 'delegator: <plugin_name>:<delegator>'
				delegator := "delegator"
				ctx = utils.WithDelegator(ctx, fmt.Sprintf("%s:%s", cfg.PluginName, delegator))
				if err := client.SetAccessRequestState(ctx, params); err != nil {
					return trace.Wrap(err)
				}
				log.Printf("Request state set: %v.", params.State)
			case types.OpDelete:
				// request has been removed (expired).
				// Due to some limitations in Teleport's event system, filters
				// don't really work with OpDelete events. As such, we may get
				// OpDelete events for requests that would not typically match
				// the filter argument we supplied above.
				log.Printf("Request %s has automatically expired.", event.Resource.GetName())
			}
		case <-watcher.Done():
			return watcher.Error()
		}
	}
}
