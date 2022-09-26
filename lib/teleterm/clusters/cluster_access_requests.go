// Copyright 2022 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clusters

import (
	"context"
	"time"

	"github.com/gravitational/teleport/api/types"
	apiutils "github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/services"
	api "github.com/gravitational/teleport/lib/teleterm/api/protogen/golang/v1"
	"github.com/gravitational/teleport/lib/teleterm/api/uri"

	"github.com/gravitational/trace"
)

type AccessRequest struct {
	URI uri.ResourceURI
	types.AccessRequest
}

// Returns all access requests available to the user
func (c *Cluster) GetAccessRequests(ctx context.Context, req *api.GetAccessRequestsRequest) ([]AccessRequest, error) {
	var (
		requests    []types.AccessRequest
		authClient  auth.ClientI
		proxyClient *client.ProxyClient
		err         error
	)
	err = addMetadataToRetryableError(ctx, func() error {
		proxyClient, err = c.clusterClient.ConnectToProxy(ctx)
		if err != nil {
			return trace.Wrap(err)
		}
		defer proxyClient.Close()

		authClient, err = proxyClient.ConnectToCluster(ctx, c.clusterClient.SiteName)
		if err != nil {
			return trace.Wrap(err)
		}
		defer authClient.Close()

		requests, err = authClient.GetAccessRequests(ctx, types.AccessRequestFilter{
			ID:    req.Id,
			State: types.RequestState(req.State),
			User:  req.User,
		})

		return nil
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	results := []AccessRequest{}
	for _, req := range requests {
		results = append(results, AccessRequest{
			URI:           c.URI.AppendAccessRequest(req.GetName()),
			AccessRequest: req,
		})
	}

	return results, nil
}

// Creates an access request
func (c *Cluster) CreateAccessRequest(ctx context.Context, req *api.CreateAccessRequestRequest) (*AccessRequest, error) {
	var (
		err         error
		authClient  auth.ClientI
		proxyClient *client.ProxyClient
		request     types.AccessRequest
	)

	resourceIDs := make([]types.ResourceID, 0, len(req.ResourceIds))
	for _, resource := range req.ResourceIds {
		resourceIDs = append(resourceIDs, types.ResourceID{
			ClusterName: resource.ClusterName,
			Name:        resource.Name,
			Kind:        resource.Kind,
		})
	}

	err = addMetadataToRetryableError(ctx, func() error {
		proxyClient, err = c.clusterClient.ConnectToProxy(ctx)
		if err != nil {
			return trace.Wrap(err)
		}
		defer proxyClient.Close()

		authClient, err = proxyClient.ConnectToCluster(ctx, c.clusterClient.SiteName)
		if err != nil {
			return trace.Wrap(err)
		}
		defer authClient.Close()

		if len(req.Roles) > 0 {
			request, err = services.NewAccessRequest(c.status.Username, req.Roles...)
		} else {
			request, err = services.NewAccessRequestWithResources(c.status.Username, nil, resourceIDs)
		}
		if err != nil {
			return trace.Wrap(err)
		}

		request.SetRequestReason(req.Reason)
		request.SetSuggestedReviewers(req.SuggestedReviewers)

		if err := authClient.CreateAccessRequest(ctx, request); err != nil {
			return trace.Wrap(err)
		}

		return nil
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &AccessRequest{
		URI:           c.URI.AppendAccessRequest(request.GetName()),
		AccessRequest: request,
	}, nil
}

func (c *Cluster) ReviewAccessRequest(ctx context.Context, req *api.ReviewAccessRequestRequest) (*AccessRequest, error) {
	var (
		err            error
		authClient     auth.ClientI
		proxyClient    *client.ProxyClient
		updatedRequest types.AccessRequest
	)

	var reviewState types.RequestState
	if err := reviewState.Parse(req.State); err != nil {
		return nil, trace.Wrap(err)
	}

	err = addMetadataToRetryableError(ctx, func() error {
		proxyClient, err = c.clusterClient.ConnectToProxy(ctx)
		if err != nil {
			return trace.Wrap(err)
		}
		defer proxyClient.Close()

		authClient, err = proxyClient.ConnectToCluster(ctx, c.clusterClient.SiteName)
		if err != nil {
			return trace.Wrap(err)
		}
		defer authClient.Close()

		reviewSubmission := types.AccessReviewSubmission{
			RequestID: req.RequestId,
			Review: types.AccessReview{
				Roles:         req.Roles,
				ProposedState: reviewState,
				Reason:        req.Reason,
				Created:       time.Now(),
			},
		}

		updatedRequest, err = authClient.SubmitAccessReview(ctx, reviewSubmission)
		if err != nil {
			return trace.Wrap(err)
		}

		return nil
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return &AccessRequest{
		URI:           c.URI.AppendAccessRequest(updatedRequest.GetName()),
		AccessRequest: updatedRequest,
	}, nil
}

func (c *Cluster) DeleteAccessRequest(ctx context.Context, req *api.DeleteAccessRequestRequest) error {
	var (
		err         error
		authClient  auth.ClientI
		proxyClient *client.ProxyClient
	)

	if req.RequestId == "" {
		return trace.BadParameter("missing request id")
	}

	err = addMetadataToRetryableError(ctx, func() error {
		proxyClient, err = c.clusterClient.ConnectToProxy(ctx)
		if err != nil {
			return trace.Wrap(err)
		}
		defer proxyClient.Close()

		authClient, err = proxyClient.ConnectToCluster(ctx, c.clusterClient.SiteName)
		if err != nil {
			return trace.Wrap(err)
		}
		defer authClient.Close()

		return authClient.DeleteAccessRequest(ctx, req.RequestId)
	})
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (c *Cluster) AssumeRole(ctx context.Context, req *api.AssumeRoleRequest) error {
	var (
		err error
	)

	err = addMetadataToRetryableError(ctx, func() error {
		params := client.ReissueParams{
			AccessRequests:     req.AccessRequestIds,
			DropAccessRequests: req.DropRequestIds,
			RouteToCluster:     c.clusterClient.SiteName,
		}

		// keep existing access requests that aren't included in the droprequests
		for _, reqID := range c.status.ActiveRequests.AccessRequests {
			if !apiutils.SliceContainsStr(req.DropRequestIds, reqID) {
				params.AccessRequests = append(params.AccessRequests, reqID)
			}
		}

		err = c.clusterClient.ReissueUserCerts(ctx, client.CertCacheKeep, params)
		if err != nil {
			return trace.Wrap(err)
		}

		if err := c.clusterClient.SaveProfile(c.dir, true); err != nil {
			return trace.Wrap(err)
		}

		return nil
	})
	if err != nil {
		return trace.Wrap(err)
	}

	return nil

}
