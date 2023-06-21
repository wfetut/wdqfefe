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

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/cloud"
	"github.com/gravitational/teleport/lib/cloud/gcp"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/trace"
	"golang.org/x/exp/slices"
)

type GCPInstances struct {
	Zone            string
	ProjectID       string
	ScriptName      string
	PublicProxyAddr string
	Parameters      []string
	Instances       []*gcp.Instance
}

func NewGCPWatcher(ctx context.Context, matchers []services.GCPMatcher, clients cloud.Clients) (*Watcher, error) {
	cancelCtx, cancelFn := context.WithCancel(ctx)
	watcher := Watcher{
		fetchers:      []Fetcher{},
		ctx:           cancelCtx,
		cancel:        cancelFn,
		fetchInterval: time.Minute,
		InstancesC:    make(chan Instances),
	}
	client, err := clients.GetGCPInstancesClient(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	for _, matcher := range matchers {
		watcher.fetchers = append(watcher.fetchers, newGCPInstanceFetcher(gcpFetcherConfig{
			Matcher:   matcher,
			GCPClient: client,
		}))
	}
	return &watcher, nil
}

type gcpFetcherConfig struct {
	Matcher   services.GCPMatcher
	GCPClient gcp.InstancesClient
}

type gcpInstanceFetcher struct {
	GCP             gcp.InstancesClient
	ProjectIDs      []string
	Zones           []string
	ProjectID       string
	ServiceAccounts []string
	Labels          types.Labels
	Parameters      map[string]string
}

func newGCPInstanceFetcher(cfg gcpFetcherConfig) *gcpInstanceFetcher {
	return &gcpInstanceFetcher{
		GCP:             cfg.GCPClient,
		Zones:           cfg.Matcher.Locations,
		ProjectIDs:      cfg.Matcher.ProjectIDs,
		ServiceAccounts: cfg.Matcher.ServiceAccounts,
		Labels:          cfg.Matcher.Tags,
		Parameters: map[string]string{
			"token":           cfg.Matcher.Params.JoinToken,
			"scriptName":      cfg.Matcher.Params.ScriptName,
			"publicProxyAddr": cfg.Matcher.Params.PublicProxyAddr,
		},
	}
}

func (*gcpInstanceFetcher) GetMatchingInstances(_ []types.Server, _ bool) ([]Instances, error) {
	return nil, trace.NotImplemented("not implemented for gcp fetchers")
}

func (f *gcpInstanceFetcher) GetInstances(ctx context.Context, _ bool) ([]Instances, error) {
	fmt.Println("getting GCP instances")
	// Key by project ID, then by zone.
	instanceMap := make(map[string]map[string][]*gcp.Instance)
	fmt.Printf("%+v\n", f.GCP)
	for _, projectID := range f.ProjectIDs {
		instanceMap[projectID] = make(map[string][]*gcp.Instance)
		for _, zone := range f.Zones {
			instanceMap[projectID][zone] = make([]*gcp.Instance, 0)
			fmt.Println("looking for instances in zone", zone)
			vms, err := f.GCP.ListInstances(ctx, projectID, zone)
			if err != nil {
				return nil, trace.Wrap(err)
			}
			filteredVMs := make([]*gcp.Instance, 0, len(vms))
			for _, vm := range vms {
				if len(f.ServiceAccounts) > 0 && !slices.Contains(f.ServiceAccounts, vm.ServiceAccount) {
					continue
				}
				if match, _, _ := services.MatchLabels(f.Labels, vm.Labels); !match {
					continue
				}
				filteredVMs = append(filteredVMs, vm)
			}
			fmt.Printf("found %v instances\n", len(filteredVMs))
			instanceMap[projectID][zone] = filteredVMs
		}
	}

	var instances []Instances
	for projectID, vmsByZone := range instanceMap {
		for zone, vms := range vmsByZone {
			if len(vms) > 0 {
				instances = append(instances, Instances{GCP: &GCPInstances{
					ProjectID:       projectID,
					Zone:            zone,
					Instances:       vms,
					ScriptName:      f.Parameters["scriptName"],
					PublicProxyAddr: f.Parameters["publicProxyAddr"],
					Parameters:      []string{f.Parameters["token"]},
				}})
			}
		}
	}

	return instances, nil
}
