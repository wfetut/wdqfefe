/*
 *
 * Copyright 2023 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package userpreferencesv1

import (
	"context"

	"github.com/gravitational/trace"
	"google.golang.org/protobuf/types/known/emptypb"

	userpreferences "github.com/gravitational/teleport/api/gen/proto/go/userpreferences/v1"
	"github.com/gravitational/teleport/lib/services"
)

// ServiceConfig holds configuration options for
// the assist gRPC service.
type ServiceConfig struct {
	Backend services.UserPreferences
}

// Service implements the teleport.assist.v1.AssistService RPC service.
type Service struct {
	userpreferences.UnimplementedUserPreferencesServiceServer

	backend services.UserPreferences
}

// NewService returns a new assist gRPC service.
func NewService(cfg *ServiceConfig) (*Service, error) {
	switch {
	case cfg.Backend == nil:
		return nil, trace.BadParameter("backend is required")
	}

	return &Service{
		backend: cfg.Backend,
	}, nil
}

// GetUserPreferences returns the user preferences for a given user.
func (a *Service) GetUserPreferences(ctx context.Context, req *userpreferences.GetUserPreferencesRequest) (*userpreferences.UserPreferences, error) {
	return a.backend.GetUserPreferences(ctx, req)
}

// UpdateUserPreferences updates the user preferences for the given user.
func (a *Service) UpdateUserPreferences(ctx context.Context, req *userpreferences.UpdateUserPreferencesRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, trace.Wrap(a.backend.UpdateUserPreferences(ctx, req))
}
