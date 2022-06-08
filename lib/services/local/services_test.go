/*
Copyright 2015-2019 Gravitational, Inc.

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

package local

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/backend/lite"
	"github.com/gravitational/teleport/lib/services/suite"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"gopkg.in/check.v1"
)

func TestMain(m *testing.M) {
	utils.InitLoggerForTests()
	os.Exit(m.Run())
}

type ServicesSuite struct {
	bk    backend.Backend
	suite *suite.ServicesTestSuite
}

var _ = check.Suite(&ServicesSuite{})

func newServicesSuite(t *testing.T) *ServicesSuite {
	var err error
	ctx := context.Background()

	clock := clockwork.NewFakeClock()

	s := &ServicesSuite{}
	s.bk, err = lite.NewWithConfig(ctx, lite.Config{
		Path:             t.TempDir(),
		PollStreamPeriod: 200 * time.Millisecond,
		Clock:            clock,
	})
	require.NoError(t, err)

	configService, err := NewClusterConfigurationService(s.bk)
	require.NoError(t, err)

	eventsService := NewEventsService(s.bk)
	presenceService := NewPresenceService(s.bk)

	s.suite = &suite.ServicesTestSuite{
		CAS:           NewCAService(s.bk),
		PresenceS:     presenceService,
		ProvisioningS: NewProvisioningService(s.bk),
		WebS:          NewIdentityService(s.bk),
		Access:        NewAccessService(s.bk),
		EventsS:       eventsService,
		ChangesC:      make(chan interface{}),
		ConfigS:       configService,
		RestrictionsS: NewRestrictionsService(s.bk),
		Clock:         clock,
	}
	t.Cleanup(func() {
		require.Nil(t, s.bk.Close())
	})

	return s
}

func TestUserCACRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.CertAuthCRUD(t)
}

func TestServerCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.ServerCRUD(t)
}

// TestAppServerCRUD tests CRUD functionality for services.App.
func TestAppServerCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.AppServerCRUD(t)
}

func TestReverseTunnelsCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.ReverseTunnelsCRUD(t)
}

func TestUsersCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.UsersCRUD(t)
}

func TestUsersExpiry(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.UsersExpiry(t)
}

func TestLoginAttempts(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.LoginAttempts(t)
}

func TestPasswordHashCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.PasswordHashCRUD(t)
}

func TestWebSessionCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.WebSessionCRUD(t)
}

func TestToken(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.TokenCRUD(t)
}

func TestRoles(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.RolesCRUD(t)
}

func TestSAMLCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.SAMLCRUD(t)
}

func TestTunnelConnectionsCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.TunnelConnectionsCRUD(t)
}

func TestGithubConnectorCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.GithubConnectorCRUD(t)
}

func TestRemoteClustersCRUD(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.RemoteClustersCRUD(t)
}

func TestEvents(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.Events(t)
}

func TestEventsClusterConfig(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.EventsClusterConfig(t)
}

func TestSemaphoreLock(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.SemaphoreLock(t)
}

func TestSemaphoreConcurrency(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.SemaphoreConcurrency(t)
}

func TestSemaphoreContention(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.SemaphoreContention(t)
}

func TestSemaphoreFlakiness(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.SemaphoreFlakiness(t)
}

func TestNetworkRestrictions(t *testing.T) {
	s := newServicesSuite(t)
	s.suite.NetworkRestrictions(t)
}
