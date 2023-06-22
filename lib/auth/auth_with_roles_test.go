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

package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/pquerna/otp/totp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/api/types/installers"
	"github.com/gravitational/teleport/api/types/webauthn"
	"github.com/gravitational/teleport/api/types/wrappers"
	apiutils "github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/api/utils/sshutils"
	"github.com/gravitational/teleport/lib/auth/native"
	"github.com/gravitational/teleport/lib/auth/testauthority"
	"github.com/gravitational/teleport/lib/authz"
	libdefaults "github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/events/eventstest"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/session"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
)

func TestGenerateUserCerts_MFAVerifiedFieldSet(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)

	u, err := createUserWithSecondFactors(srv)
	require.NoError(t, err)
	client, err := srv.NewClient(TestUser(u.username))
	require.NoError(t, err)

	_, pub, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	for _, test := range []struct {
		desc           string
		getMFAResponse func() *proto.MFAAuthenticateResponse
		wantErr        string
	}{
		{
			desc: "valid mfa response",
			getMFAResponse: func() *proto.MFAAuthenticateResponse {
				// Get a totp code to re-auth.
				totpCode, err := totp.GenerateCode(u.totpDev.TOTPSecret, srv.AuthServer.Clock().Now().Add(30*time.Second))
				require.NoError(t, err)

				return &proto.MFAAuthenticateResponse{
					Response: &proto.MFAAuthenticateResponse_TOTP{
						TOTP: &proto.TOTPResponse{Code: totpCode},
					},
				}
			},
		},
		{
			desc: "valid empty mfa response",
			getMFAResponse: func() *proto.MFAAuthenticateResponse {
				return nil
			},
		},
		{
			desc:    "invalid mfa response",
			wantErr: "invalid totp token",
			getMFAResponse: func() *proto.MFAAuthenticateResponse {
				return &proto.MFAAuthenticateResponse{
					Response: &proto.MFAAuthenticateResponse_TOTP{
						TOTP: &proto.TOTPResponse{Code: "invalid-totp-code"},
					},
				}
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			mfaResponse := test.getMFAResponse()
			certs, err := client.GenerateUserCerts(context.Background(), proto.UserCertsRequest{
				PublicKey:   pub,
				Username:    u.username,
				Expires:     time.Now().Add(time.Hour),
				MFAResponse: mfaResponse,
			})

			switch {
			case test.wantErr != "":
				require.True(t, trace.IsAccessDenied(err), "GenerateUserCerts returned err = %v (%T), wanted trace.AccessDenied", err, err)
				require.ErrorContains(t, err, test.wantErr)
				return
			default:
				require.NoError(t, err)
			}

			sshCert, err := sshutils.ParseCertificate(certs.SSH)
			require.NoError(t, err)
			mfaVerified := sshCert.Permissions.Extensions[teleport.CertExtensionMFAVerified]

			switch {
			case mfaResponse == nil:
				require.Empty(t, mfaVerified, "GenerateUserCerts returned certificate with non-empty CertExtensionMFAVerified")
			default:
				require.Equal(t, mfaVerified, u.totpDev.MFA.Id, "GenerateUserCerts returned certificate with unexpected CertExtensionMFAVerified")
			}
		})
	}
}

// TestLocalUserCanReissueCerts tests that local users can reissue
// certificates for themselves with varying TTLs.
func TestLocalUserCanReissueCerts(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)

	_, pub, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	start := srv.AuthServer.Clock().Now()

	for _, test := range []struct {
		desc         string
		renewable    bool
		roleRequests bool
		reqTTL       time.Duration
		expiresIn    time.Duration
	}{
		{
			desc:      "not-renewable",
			renewable: false,
			// expiration limited to duration of the user's session (default 1 hour)
			reqTTL:    4 * time.Hour,
			expiresIn: 1 * time.Hour,
		},
		{
			desc:         "not-renewable-role-requests",
			renewable:    false,
			roleRequests: true,
			// expiration is allowed to be pushed out into the future
			reqTTL:    4 * time.Hour,
			expiresIn: 4 * time.Hour,
		},
		{
			desc:      "renewable",
			renewable: true,
			reqTTL:    4 * time.Hour,
			expiresIn: 4 * time.Hour,
		},
		{
			desc:         "renewable-role-requests",
			renewable:    true,
			roleRequests: true,
			reqTTL:       4 * time.Hour,
			expiresIn:    4 * time.Hour,
		},
		{
			desc:      "max-renew",
			renewable: true,
			// expiration is allowed to be pushed out into the future,
			// but no more than the maximum renewable cert TTL
			reqTTL:    2 * libdefaults.MaxRenewableCertTTL,
			expiresIn: libdefaults.MaxRenewableCertTTL,
		},
		{
			desc:         "not-renewable-role-requests-max-renew",
			renewable:    false,
			roleRequests: true,
			reqTTL:       2 * libdefaults.MaxRenewableCertTTL,
			expiresIn:    libdefaults.MaxRenewableCertTTL,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			ctx := context.Background()
			user, role, err := CreateUserAndRole(srv.Auth(), test.desc, []string{"role"}, nil)
			require.NoError(t, err)
			authPref, err := srv.Auth().GetAuthPreference(ctx)
			require.NoError(t, err)
			authPref.SetDefaultSessionTTL(types.Duration(test.expiresIn))
			srv.Auth().SetAuthPreference(ctx, authPref)

			var id TestIdentity
			if test.renewable {
				id = TestRenewableUser(user.GetName(), 0)

				meta := user.GetMetadata()
				meta.Labels = map[string]string{
					types.BotGenerationLabel: "0",
				}
				user.SetMetadata(meta)
				err := srv.Auth().UpsertUser(user)
				require.NoError(t, err)
			} else {
				id = TestUser(user.GetName())
			}

			client, err := srv.NewClient(id)
			require.NoError(t, err)

			req := proto.UserCertsRequest{
				PublicKey: pub,
				Username:  user.GetName(),
				Expires:   start.Add(test.reqTTL),
			}
			if test.roleRequests {
				// Reconfigure role to allow impersonation of its own role.
				role.SetImpersonateConditions(types.Allow, types.ImpersonateConditions{
					Roles: []string{role.GetName()},
				})
				require.NoError(t, srv.Auth().UpsertRole(ctx, role))

				req.UseRoleRequests = true
				req.RoleRequests = []string{role.GetName()}
			}

			certs, err := client.GenerateUserCerts(ctx, req)
			require.NoError(t, err)

			x509, err := tlsca.ParseCertificatePEM(certs.TLS)
			require.NoError(t, err)

			require.WithinDuration(t, start.Add(test.expiresIn), x509.NotAfter, 1*time.Second)
		})
	}
}

// TestSSOUserCanReissueCert makes sure that SSO user can reissue certificate
// for themselves.
func TestSSOUserCanReissueCert(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test SSO user.
	user, _, err := CreateUserAndRole(srv.Auth(), "sso-user", []string{"role"}, nil)
	require.NoError(t, err)
	user.SetCreatedBy(types.CreatedBy{
		Connector: &types.ConnectorRef{Type: "oidc", ID: "google"},
	})
	err = srv.Auth().UpdateUser(ctx, user)
	require.NoError(t, err)

	client, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)

	_, pub, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	_, err = client.GenerateUserCerts(ctx, proto.UserCertsRequest{
		PublicKey: pub,
		Username:  user.GetName(),
		Expires:   time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
}

func TestInstaller(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	_, err := CreateRole(ctx, srv.Auth(), "test-empty", types.RoleSpecV6{})
	require.NoError(t, err)

	_, err = CreateRole(ctx, srv.Auth(), "test-read", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindInstaller},
					Verbs:     []string{types.VerbRead},
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = CreateRole(ctx, srv.Auth(), "test-update", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindInstaller},
					Verbs:     []string{types.VerbUpdate, types.VerbCreate},
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = CreateRole(ctx, srv.Auth(), "test-delete", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindInstaller},
					Verbs:     []string{types.VerbDelete},
				},
			},
		},
	})
	require.NoError(t, err)
	user, err := CreateUser(srv.Auth(), "testuser")
	require.NoError(t, err)

	inst, err := types.NewInstallerV1(installers.InstallerScriptName, "contents")
	require.NoError(t, err)
	err = srv.Auth().SetInstaller(ctx, inst)
	require.NoError(t, err)

	for _, tc := range []struct {
		roles           []string
		assert          require.ErrorAssertionFunc
		installerAction func(*Client) error
	}{{
		roles:  []string{"test-empty"},
		assert: require.Error,
		installerAction: func(c *Client) error {
			_, err := c.GetInstaller(ctx, installers.InstallerScriptName)
			return err
		},
	}, {
		roles:  []string{"test-read"},
		assert: require.NoError,
		installerAction: func(c *Client) error {
			_, err := c.GetInstaller(ctx, installers.InstallerScriptName)
			return err
		},
	}, {
		roles:  []string{"test-update"},
		assert: require.NoError,
		installerAction: func(c *Client) error {
			inst, err := types.NewInstallerV1(installers.InstallerScriptName, "new-contents")
			require.NoError(t, err)
			return c.SetInstaller(ctx, inst)
		},
	}, {
		roles:  []string{"test-delete"},
		assert: require.NoError,
		installerAction: func(c *Client) error {
			err := c.DeleteInstaller(ctx, installers.InstallerScriptName)
			return err
		},
	}} {
		user.SetRoles(tc.roles)
		err = srv.Auth().UpsertUser(user)
		require.NoError(t, err)

		client, err := srv.NewClient(TestUser(user.GetName()))
		require.NoError(t, err)
		tc.assert(t, tc.installerAction(client))
	}
}

func TestGithubAuthRequest(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	emptyRole, err := CreateRole(ctx, srv.Auth(), "test-empty", types.RoleSpecV6{})
	require.NoError(t, err)

	access1Role, err := CreateRole(ctx, srv.Auth(), "test-access-1", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindGithubRequest},
					Verbs:     []string{types.VerbCreate},
				},
			},
		},
	})
	require.NoError(t, err)

	access2Role, err := CreateRole(ctx, srv.Auth(), "test-access-2", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindGithub},
					Verbs:     []string{types.VerbCreate},
				},
			},
		},
	})
	require.NoError(t, err)

	access3Role, err := CreateRole(ctx, srv.Auth(), "test-access-3", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindGithub, types.KindGithubRequest},
					Verbs:     []string{types.VerbCreate},
				},
			},
		},
	})
	require.NoError(t, err)

	readerRole, err := CreateRole(ctx, srv.Auth(), "test-access-4", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindGithubRequest},
					Verbs:     []string{types.VerbRead},
				},
			},
		},
	})
	require.NoError(t, err)

	conn, err := types.NewGithubConnector("example", types.GithubConnectorSpecV3{
		ClientID:     "example-client-id",
		ClientSecret: "example-client-secret",
		RedirectURL:  "https://localhost:3080/v1/webapi/github/callback",
		Display:      "sign in with github",
		TeamsToLogins: []types.TeamMapping{
			{
				Organization: "octocats",
				Team:         "idp-admin",
				Logins:       []string{"access"},
			},
		},
	})
	require.NoError(t, err)

	err = srv.Auth().UpsertGithubConnector(context.Background(), conn)
	require.NoError(t, err)

	reqNormal := types.GithubAuthRequest{ConnectorID: conn.GetName(), Type: constants.Github}
	reqTest := types.GithubAuthRequest{ConnectorID: conn.GetName(), Type: constants.Github, SSOTestFlow: true, ConnectorSpec: &types.GithubConnectorSpecV3{
		ClientID:     "example-client-id",
		ClientSecret: "example-client-secret",
		RedirectURL:  "https://localhost:3080/v1/webapi/github/callback",
		Display:      "sign in with github",
		TeamsToLogins: []types.TeamMapping{
			{
				Organization: "octocats",
				Team:         "idp-admin",
				Logins:       []string{"access"},
			},
		},
	}}

	tests := []struct {
		desc               string
		roles              []string
		request            types.GithubAuthRequest
		expectAccessDenied bool
	}{
		{
			desc:               "empty role - no access",
			roles:              []string{emptyRole.GetName()},
			request:            reqNormal,
			expectAccessDenied: true,
		},
		{
			desc:               "can create regular request with normal access",
			roles:              []string{access1Role.GetName()},
			request:            reqNormal,
			expectAccessDenied: false,
		},
		{
			desc:               "cannot create sso test request with normal access",
			roles:              []string{access1Role.GetName()},
			request:            reqTest,
			expectAccessDenied: true,
		},
		{
			desc:               "cannot create normal request with connector access",
			roles:              []string{access2Role.GetName()},
			request:            reqNormal,
			expectAccessDenied: true,
		},
		{
			desc:               "cannot create sso test request with connector access",
			roles:              []string{access2Role.GetName()},
			request:            reqTest,
			expectAccessDenied: true,
		},
		{
			desc:               "can create regular request with combined access",
			roles:              []string{access3Role.GetName()},
			request:            reqNormal,
			expectAccessDenied: false,
		},
		{
			desc:               "can create sso test request with combined access",
			roles:              []string{access3Role.GetName()},
			request:            reqTest,
			expectAccessDenied: false,
		},
	}

	user, err := CreateUser(srv.Auth(), "dummy")
	require.NoError(t, err)

	userReader, err := CreateUser(srv.Auth(), "dummy-reader", readerRole)
	require.NoError(t, err)

	clientReader, err := srv.NewClient(TestUser(userReader.GetName()))
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			user.SetRoles(tt.roles)
			err = srv.Auth().UpsertUser(user)
			require.NoError(t, err)

			client, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			request, err := client.CreateGithubAuthRequest(ctx, tt.request)
			if tt.expectAccessDenied {
				require.Error(t, err)
				require.True(t, trace.IsAccessDenied(err), "expected access denied, got: %v", err)
				return
			}

			require.NoError(t, err)
			require.NotEmpty(t, request.StateToken)
			require.Equal(t, tt.request.ConnectorID, request.ConnectorID)

			requestCopy, err := clientReader.GetGithubAuthRequest(ctx, request.StateToken)
			require.NoError(t, err)
			require.Equal(t, request, requestCopy)
		})
	}
}

func TestSSODiagnosticInfo(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// empty role
	emptyRole, err := CreateRole(ctx, srv.Auth(), "test-empty", types.RoleSpecV6{})
	require.NoError(t, err)

	// privileged role
	privRole, err := CreateRole(ctx, srv.Auth(), "priv-access", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindSAMLRequest},
					Verbs:     []string{types.VerbRead},
				},
			},
		},
	})
	require.NoError(t, err)

	user, err := CreateUser(srv.Auth(), "dummy", emptyRole)
	require.NoError(t, err)

	userPriv, err := CreateUser(srv.Auth(), "superDummy", privRole)
	require.NoError(t, err)

	client, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)

	clientPriv, err := srv.NewClient(TestUser(userPriv.GetName()))
	require.NoError(t, err)

	// fresh server, no SSO diag info, request fails
	info, err := client.GetSSODiagnosticInfo(ctx, types.KindSAML, "XXX-INVALID-ID")
	require.Error(t, err)
	require.Nil(t, info)

	infoCreate := types.SSODiagnosticInfo{
		TestFlow: true,
		Error:    "aaa bbb ccc",
	}

	// invalid auth kind returns error, no information stored.
	err = srv.Auth().CreateSSODiagnosticInfo(ctx, "XXX-BAD-KIND", "ABC123", infoCreate)
	require.Error(t, err)
	info, err = client.GetSSODiagnosticInfo(ctx, "XXX-BAD-KIND", "XXX-INVALID-ID")
	require.Error(t, err)
	require.Nil(t, info)

	// proper record can be stored, retrieved, if user has access.
	err = srv.Auth().CreateSSODiagnosticInfo(ctx, types.KindSAML, "ABC123", infoCreate)
	require.NoError(t, err)

	info, err = client.GetSSODiagnosticInfo(ctx, types.KindSAML, "ABC123")
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))
	require.Nil(t, info)

	info, err = clientPriv.GetSSODiagnosticInfo(ctx, types.KindSAML, "ABC123")
	require.NoError(t, err)
	require.Equal(t, &infoCreate, info)
}

func TestGenerateUserCertsWithRoleRequest(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	emptyRole, err := CreateRole(ctx, srv.Auth(), "test-empty", types.RoleSpecV6{})
	require.NoError(t, err)

	accessFooRole, err := CreateRole(ctx, srv.Auth(), "test-access-foo", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Logins: []string{"foo"},
		},
	})
	require.NoError(t, err)

	accessBarRole, err := CreateRole(ctx, srv.Auth(), "test-access-bar", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Logins: []string{"bar"},
		},
	})
	require.NoError(t, err)

	loginsTraitsRole, err := CreateRole(ctx, srv.Auth(), "test-access-traits", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Logins: []string{"{{internal.logins}}"},
		},
	})
	require.NoError(t, err)

	impersonatorRole, err := CreateRole(ctx, srv.Auth(), "test-impersonator", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Impersonate: &types.ImpersonateConditions{
				Roles: []string{
					accessFooRole.GetName(),
					accessBarRole.GetName(),
					loginsTraitsRole.GetName(),
				},
			},
		},
	})
	require.NoError(t, err)

	denyBarRole, err := CreateRole(ctx, srv.Auth(), "test-deny", types.RoleSpecV6{
		Deny: types.RoleConditions{
			Impersonate: &types.ImpersonateConditions{
				Roles: []string{accessBarRole.GetName()},
			},
		},
	})
	require.NoError(t, err)

	dummyUserRole, err := types.NewRole("dummy-user-role", types.RoleSpecV6{})
	require.NoError(t, err)

	dummyUser, err := CreateUser(srv.Auth(), "dummy-user", dummyUserRole)
	require.NoError(t, err)

	dummyUserImpersonatorRole, err := CreateRole(ctx, srv.Auth(), "dummy-user-impersonator", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Impersonate: &types.ImpersonateConditions{
				Users: []string{dummyUser.GetName()},
				Roles: []string{dummyUserRole.GetName()},
			},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		desc             string
		username         string
		userTraits       wrappers.Traits
		roles            []string
		roleRequests     []string
		useRoleRequests  bool
		expectPrincipals []string
		expectRoles      []string
		expectError      func(error) bool
	}{
		{
			desc:             "requesting all allowed roles",
			username:         "alice",
			roles:            []string{emptyRole.GetName(), impersonatorRole.GetName()},
			roleRequests:     []string{accessFooRole.GetName(), accessBarRole.GetName()},
			useRoleRequests:  true,
			expectPrincipals: []string{"foo", "bar"},
		},
		{
			desc:     "requesting a subset of allowed roles",
			username: "bob",
			userTraits: wrappers.Traits{
				// We don't expect this login trait to appear in the principals
				// as "test-access-foo" does not contain {{internal.logins}}
				constants.TraitLogins: []string{"trait-login"},
			},
			roles:            []string{emptyRole.GetName(), impersonatorRole.GetName()},
			roleRequests:     []string{accessFooRole.GetName()},
			useRoleRequests:  true,
			expectPrincipals: []string{"foo"},
		},
		{
			// Users traits should be preserved in role impersonation
			desc:     "requesting a role preserves user traits",
			username: "ash",
			userTraits: wrappers.Traits{
				constants.TraitLogins: []string{"trait-login"},
			},
			roles: []string{
				emptyRole.GetName(),
				impersonatorRole.GetName(),
			},
			roleRequests:     []string{loginsTraitsRole.GetName()},
			useRoleRequests:  true,
			expectPrincipals: []string{"trait-login"},
		},
		{
			// Users not using role requests should keep their own roles
			desc:            "requesting no roles",
			username:        "charlie",
			roles:           []string{emptyRole.GetName()},
			roleRequests:    []string{},
			useRoleRequests: false,
			expectRoles:     []string{emptyRole.GetName()},
		},
		{
			// An empty role request should fail when role requests are
			// expected.
			desc:            "requesting no roles with UseRoleRequests",
			username:        "charlie",
			roles:           []string{emptyRole.GetName()},
			roleRequests:    []string{},
			useRoleRequests: true,
			expectError: func(err error) bool {
				return trace.IsBadParameter(err)
			},
		},
		{
			desc:            "requesting a disallowed role",
			username:        "dave",
			roles:           []string{emptyRole.GetName()},
			roleRequests:    []string{accessFooRole.GetName()},
			useRoleRequests: true,
			expectError: func(err error) bool {
				return err != nil && trace.IsAccessDenied(err)
			},
		},
		{
			desc:            "requesting a nonexistent role",
			username:        "erin",
			roles:           []string{emptyRole.GetName()},
			roleRequests:    []string{"doesnotexist"},
			useRoleRequests: true,
			expectError: func(err error) bool {
				return err != nil && trace.IsNotFound(err)
			},
		},
		{
			desc:             "requesting an allowed role with a separate deny role",
			username:         "frank",
			roles:            []string{emptyRole.GetName(), impersonatorRole.GetName(), denyBarRole.GetName()},
			roleRequests:     []string{accessFooRole.GetName()},
			useRoleRequests:  true,
			expectPrincipals: []string{"foo"},
		},
		{
			desc:            "requesting a denied role",
			username:        "geoff",
			roles:           []string{emptyRole.GetName(), impersonatorRole.GetName(), denyBarRole.GetName()},
			roleRequests:    []string{accessBarRole.GetName()},
			useRoleRequests: true,
			expectError: func(err error) bool {
				return err != nil && trace.IsAccessDenied(err)
			},
		},
		{
			desc:            "misusing a role intended for user impersonation",
			username:        "helen",
			roles:           []string{emptyRole.GetName(), dummyUserImpersonatorRole.GetName()},
			roleRequests:    []string{dummyUserRole.GetName()},
			useRoleRequests: true,
			expectError: func(err error) bool {
				return err != nil && trace.IsAccessDenied(err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			user, err := CreateUser(srv.Auth(), tt.username)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, srv.Auth().DeleteUser(ctx, tt.username), "failed cleaning up testing user: %s", tt.username)
			})
			for _, role := range tt.roles {
				user.AddRole(role)
			}
			if tt.userTraits != nil {
				user.SetTraits(tt.userTraits)
			}
			err = srv.Auth().UpsertUser(user)
			require.NoError(t, err)

			client, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			_, pub, err := testauthority.New().GenerateKeyPair()
			require.NoError(t, err)

			certs, err := client.GenerateUserCerts(ctx, proto.UserCertsRequest{
				PublicKey:       pub,
				Username:        user.GetName(),
				Expires:         time.Now().Add(time.Hour),
				RoleRequests:    tt.roleRequests,
				UseRoleRequests: tt.useRoleRequests,
			})
			if tt.expectError != nil {
				require.True(t, tt.expectError(err), "error: %+v: %s", err, trace.DebugReport(err))
				return
			}
			require.NoError(t, err)

			// Parse the Identity
			impersonatedTLSCert, err := tlsca.ParseCertificatePEM(certs.TLS)
			require.NoError(t, err)
			impersonatedIdent, err := tlsca.FromSubject(impersonatedTLSCert.Subject, impersonatedTLSCert.NotAfter)
			require.NoError(t, err)

			userCert, err := sshutils.ParseCertificate(certs.SSH)
			require.NoError(t, err)

			roles, ok := userCert.Extensions[teleport.CertExtensionTeleportRoles]
			require.True(t, ok)

			parsedRoles, err := services.UnmarshalCertRoles(roles)
			require.NoError(t, err)

			if len(tt.expectPrincipals) > 0 {
				expectPrincipals := append(tt.expectPrincipals, teleport.SSHSessionJoinPrincipal)
				require.ElementsMatch(t, expectPrincipals, userCert.ValidPrincipals, "principals must match")
			}

			if tt.expectRoles != nil {
				require.ElementsMatch(t, tt.expectRoles, parsedRoles, "granted roles must match expected values")
			} else {
				require.ElementsMatch(t, tt.roleRequests, parsedRoles, "granted roles must match requests")
			}

			_, disallowReissue := userCert.Extensions[teleport.CertExtensionDisallowReissue]
			if len(tt.roleRequests) > 0 {
				impersonator, ok := userCert.Extensions[teleport.CertExtensionImpersonator]
				require.True(t, ok, "impersonator must be set if any role requests exist")
				require.Equal(t, tt.username, impersonator, "certificate must show self-impersonation")

				require.True(t, disallowReissue)
				require.True(t, impersonatedIdent.DisallowReissue)
			} else {
				require.False(t, disallowReissue)
				require.False(t, impersonatedIdent.DisallowReissue)
			}
		})
	}
}

// TestRoleRequestDenyReimpersonation make sure role requests can't be used to
// re-escalate privileges using a (perhaps compromised) set of role
// impersonated certs.
func TestRoleRequestDenyReimpersonation(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	accessFooRole, err := CreateRole(ctx, srv.Auth(), "test-access-foo", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Logins: []string{"foo"},
		},
	})
	require.NoError(t, err)

	accessBarRole, err := CreateRole(ctx, srv.Auth(), "test-access-bar", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Logins: []string{"bar"},
		},
	})
	require.NoError(t, err)

	impersonatorRole, err := CreateRole(ctx, srv.Auth(), "test-impersonator", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Impersonate: &types.ImpersonateConditions{
				Roles: []string{accessFooRole.GetName(), accessBarRole.GetName()},
			},
		},
	})
	require.NoError(t, err)

	// Create a testing user.
	user, err := CreateUser(srv.Auth(), "alice")
	require.NoError(t, err)
	user.AddRole(impersonatorRole.GetName())
	err = srv.Auth().UpsertUser(user)
	require.NoError(t, err)

	// Generate cert with a role request.
	client, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)
	priv, pub, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	// Request certs for only the `foo` role.
	certs, err := client.GenerateUserCerts(ctx, proto.UserCertsRequest{
		PublicKey:    pub,
		Username:     user.GetName(),
		Expires:      time.Now().Add(time.Hour),
		RoleRequests: []string{accessFooRole.GetName()},
	})
	require.NoError(t, err)

	// Make an impersonated client.
	impersonatedTLSCert, err := tls.X509KeyPair(certs.TLS, priv)
	require.NoError(t, err)
	impersonatedClient := srv.NewClientWithCert(impersonatedTLSCert)

	// Attempt a request.
	_, err = impersonatedClient.GetClusterName()
	require.NoError(t, err)

	// Attempt to generate new certs for a different (allowed) role.
	_, err = impersonatedClient.GenerateUserCerts(ctx, proto.UserCertsRequest{
		PublicKey:    pub,
		Username:     user.GetName(),
		Expires:      time.Now().Add(time.Hour),
		RoleRequests: []string{accessBarRole.GetName()},
	})
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))

	// Attempt to generate new certs for the same role.
	_, err = impersonatedClient.GenerateUserCerts(ctx, proto.UserCertsRequest{
		PublicKey:    pub,
		Username:     user.GetName(),
		Expires:      time.Now().Add(time.Hour),
		RoleRequests: []string{accessFooRole.GetName()},
	})
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))

	// Attempt to generate new certs with no role requests
	// (If allowed, this might issue certs for the original user without role
	// requests.)
	_, err = impersonatedClient.GenerateUserCerts(ctx, proto.UserCertsRequest{
		PublicKey: pub,
		Username:  user.GetName(),
		Expires:   time.Now().Add(time.Hour),
	})
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))
}

// TestGenerateDatabaseCert makes sure users and services with appropriate
// permissions can generate certificates for self-hosted databases.
func TestGenerateDatabaseCert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// This user can't impersonate anyone and can't generate database certs.
	userWithoutAccess, _, err := CreateUserAndRole(srv.Auth(), "user", []string{"role1"}, nil)
	require.NoError(t, err)

	// This user can impersonate system role Db.
	userImpersonateDb, roleDb, err := CreateUserAndRole(srv.Auth(), "user-impersonate-db", []string{"role2"}, nil)
	require.NoError(t, err)
	roleDb.SetImpersonateConditions(types.Allow, types.ImpersonateConditions{
		Users: []string{string(types.RoleDatabase)},
		Roles: []string{string(types.RoleDatabase)},
	})
	require.NoError(t, srv.Auth().UpsertRole(ctx, roleDb))

	tests := []struct {
		desc     string
		identity TestIdentity
		err      string
	}{
		{
			desc:     "user can't sign database certs",
			identity: TestUser(userWithoutAccess.GetName()),
			err:      "access denied",
		},
		{
			desc:     "user can impersonate Db and sign database certs",
			identity: TestUser(userImpersonateDb.GetName()),
		},
		{
			desc:     "built-in admin can sign database certs",
			identity: TestAdmin(),
		},
		{
			desc:     "database service can sign database certs",
			identity: TestBuiltin(types.RoleDatabase),
		},
	}

	// Generate CSR once for speed sake.
	priv, err := native.GeneratePrivateKey()
	require.NoError(t, err)

	csr, err := tlsca.GenerateCertificateRequestPEM(pkix.Name{CommonName: "test"}, priv)
	require.NoError(t, err)

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)

			_, err = client.GenerateDatabaseCert(ctx, &proto.DatabaseCertRequest{CSR: csr})
			if test.err != "" {
				require.ErrorContains(t, err, test.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type testDynamicallyConfigurableRBACParams struct {
	kind                          string
	storeDefault, storeConfigFile func(*Server)
	get, set, reset               func(*ServerWithRoles) error
	alwaysReadable                bool
}

// TestDynamicConfigurationRBACVerbs tests the dynamic configuration RBAC verbs described
// in rfd/0016-dynamic-configuration.md § Implementation.
func testDynamicallyConfigurableRBAC(t *testing.T, p testDynamicallyConfigurableRBACParams) {
	testAuth, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	testOperation := func(op func(*ServerWithRoles) error, allowRules []types.Rule, expectErr, withConfigFile bool) func(*testing.T) {
		return func(t *testing.T) {
			if withConfigFile {
				p.storeConfigFile(testAuth.AuthServer)
			} else {
				p.storeDefault(testAuth.AuthServer)
			}
			server := serverWithAllowRules(t, testAuth, allowRules)
			opErr := op(server)
			if expectErr {
				require.Error(t, opErr)
			} else {
				require.NoError(t, opErr)
			}
		}
	}

	// runTestCases generates all non-empty RBAC verb combinations and checks the expected
	// error for each operation.
	runTestCases := func(withConfigFile bool) {
		for _, canCreate := range []bool{false, true} {
			for _, canUpdate := range []bool{false, true} {
				for _, canRead := range []bool{false, true} {
					if !canRead && !canUpdate && !canCreate {
						continue
					}
					verbs := []string{}
					expectGetErr, expectSetErr, expectResetErr := true, true, true
					if canRead || p.alwaysReadable {
						verbs = append(verbs, types.VerbRead)
						expectGetErr = false
					}
					if canUpdate {
						verbs = append(verbs, types.VerbUpdate)
						if !withConfigFile {
							expectSetErr, expectResetErr = false, false
						}
					}
					if canCreate {
						verbs = append(verbs, types.VerbCreate)
						if canUpdate {
							expectSetErr = false
						}
					}
					allowRules := []types.Rule{
						{
							Resources: []string{p.kind},
							Verbs:     verbs,
						},
					}
					t.Run(fmt.Sprintf("get %v %v", verbs, withConfigFile), testOperation(p.get, allowRules, expectGetErr, withConfigFile))
					t.Run(fmt.Sprintf("set %v %v", verbs, withConfigFile), testOperation(p.set, allowRules, expectSetErr, withConfigFile))
					t.Run(fmt.Sprintf("reset %v %v", verbs, withConfigFile), testOperation(p.reset, allowRules, expectResetErr, withConfigFile))
				}
			}
		}
	}

	runTestCases(false)
	runTestCases(true)
}

func TestAuthPreferenceRBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	testDynamicallyConfigurableRBAC(t, testDynamicallyConfigurableRBACParams{
		kind: types.KindClusterAuthPreference,
		storeDefault: func(s *Server) {
			s.SetAuthPreference(ctx, types.DefaultAuthPreference())
		},
		storeConfigFile: func(s *Server) {
			authPref := types.DefaultAuthPreference()
			authPref.SetOrigin(types.OriginConfigFile)
			s.SetAuthPreference(ctx, authPref)
		},
		get: func(s *ServerWithRoles) error {
			_, err := s.GetAuthPreference(ctx)
			return err
		},
		set: func(s *ServerWithRoles) error {
			return s.SetAuthPreference(ctx, types.DefaultAuthPreference())
		},
		reset: func(s *ServerWithRoles) error {
			return s.ResetAuthPreference(ctx)
		},
		alwaysReadable: true,
	})
}

func TestClusterNetworkingConfigRBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	testDynamicallyConfigurableRBAC(t, testDynamicallyConfigurableRBACParams{
		kind: types.KindClusterNetworkingConfig,
		storeDefault: func(s *Server) {
			s.SetClusterNetworkingConfig(ctx, types.DefaultClusterNetworkingConfig())
		},
		storeConfigFile: func(s *Server) {
			netConfig := types.DefaultClusterNetworkingConfig()
			netConfig.SetOrigin(types.OriginConfigFile)
			s.SetClusterNetworkingConfig(ctx, netConfig)
		},
		get: func(s *ServerWithRoles) error {
			_, err := s.GetClusterNetworkingConfig(ctx)
			return err
		},
		set: func(s *ServerWithRoles) error {
			return s.SetClusterNetworkingConfig(ctx, types.DefaultClusterNetworkingConfig())
		},
		reset: func(s *ServerWithRoles) error {
			return s.ResetClusterNetworkingConfig(ctx)
		},
	})
}

func TestSessionRecordingConfigRBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	testDynamicallyConfigurableRBAC(t, testDynamicallyConfigurableRBACParams{
		kind: types.KindSessionRecordingConfig,
		storeDefault: func(s *Server) {
			s.SetSessionRecordingConfig(ctx, types.DefaultSessionRecordingConfig())
		},
		storeConfigFile: func(s *Server) {
			recConfig := types.DefaultSessionRecordingConfig()
			recConfig.SetOrigin(types.OriginConfigFile)
			s.SetSessionRecordingConfig(ctx, recConfig)
		},
		get: func(s *ServerWithRoles) error {
			_, err := s.GetSessionRecordingConfig(ctx)
			return err
		},
		set: func(s *ServerWithRoles) error {
			return s.SetSessionRecordingConfig(ctx, types.DefaultSessionRecordingConfig())
		},
		reset: func(s *ServerWithRoles) error {
			return s.ResetSessionRecordingConfig(ctx)
		},
	})
}

// go test ./lib/auth -bench=. -run=^$ -v -benchtime 1x
// goos: darwin
// goarch: amd64
// pkg: github.com/gravitational/teleport/lib/auth
// cpu: Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz
// BenchmarkListNodes
// BenchmarkListNodes/simple_labels
// BenchmarkListNodes/simple_labels-16                    1        1079886286 ns/op        525128104 B/op   8831939 allocs/op
// BenchmarkListNodes/simple_expression
// BenchmarkListNodes/simple_expression-16                1         770118479 ns/op        432667432 B/op   6514790 allocs/op
// BenchmarkListNodes/labels
// BenchmarkListNodes/labels-16                           1        1931843502 ns/op        741444360 B/op  15159333 allocs/op
// BenchmarkListNodes/expression
// BenchmarkListNodes/expression-16                       1        1040855282 ns/op        509643128 B/op   8120970 allocs/op
// BenchmarkListNodes/complex_labels
// BenchmarkListNodes/complex_labels-16                   1        2274376396 ns/op        792948904 B/op  17084107 allocs/op
// BenchmarkListNodes/complex_expression
// BenchmarkListNodes/complex_expression-16               1        1518800599 ns/op        738532920 B/op  12483748 allocs/op
// PASS
// ok      github.com/gravitational/teleport/lib/auth      11.679s
func BenchmarkListNodes(b *testing.B) {
	const nodeCount = 50_000
	const roleCount = 32

	logger := logrus.StandardLogger()
	logger.ReplaceHooks(make(logrus.LevelHooks))
	logrus.SetFormatter(utils.NewTestJSONFormatter())
	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(io.Discard)

	ctx := context.Background()
	srv := newTestTLSServer(b)

	var ids []string
	for i := 0; i < roleCount; i++ {
		ids = append(ids, uuid.New().String())
	}

	ids[0] = "hidden"

	var hiddenNodes int
	// Create test nodes.
	for i := 0; i < nodeCount; i++ {
		name := uuid.New().String()
		id := ids[i%len(ids)]
		if id == "hidden" {
			hiddenNodes++
		}
		node, err := types.NewServerWithLabels(
			name,
			types.KindNode,
			types.ServerSpecV2{},
			map[string]string{
				"key":   id,
				"group": "users",
			},
		)
		require.NoError(b, err)

		_, err = srv.Auth().UpsertNode(ctx, node)
		require.NoError(b, err)
	}
	testNodes, err := srv.Auth().GetNodes(ctx, defaults.Namespace)
	require.NoError(b, err)
	require.Len(b, testNodes, nodeCount)

	for _, tc := range []struct {
		desc     string
		editRole func(types.Role, string)
	}{
		{
			desc: "simple labels",
			editRole: func(r types.Role, id string) {
				if id == "hidden" {
					r.SetNodeLabels(types.Deny, types.Labels{"key": {id}})
				} else {
					r.SetNodeLabels(types.Allow, types.Labels{"key": {id}})
				}
			},
		},
		{
			desc: "simple expression",
			editRole: func(r types.Role, id string) {
				if id == "hidden" {
					err = r.SetLabelMatchers(types.Deny, types.KindNode, types.LabelMatchers{
						Expression: `labels.key == "hidden"`,
					})
					require.NoError(b, err)
				} else {
					err := r.SetLabelMatchers(types.Allow, types.KindNode, types.LabelMatchers{
						Expression: fmt.Sprintf(`labels.key == %q`, id),
					})
					require.NoError(b, err)
				}
			},
		},
		{
			desc: "labels",
			editRole: func(r types.Role, id string) {
				r.SetNodeLabels(types.Allow, types.Labels{
					"key":   {id},
					"group": {"{{external.group}}"},
				})
				r.SetNodeLabels(types.Deny, types.Labels{"key": {"hidden"}})
			},
		},
		{
			desc: "expression",
			editRole: func(r types.Role, id string) {
				err := r.SetLabelMatchers(types.Allow, types.KindNode, types.LabelMatchers{
					Expression: fmt.Sprintf(`labels.key == %q && contains(user.spec.traits["group"], labels["group"])`,
						id),
				})
				require.NoError(b, err)
				err = r.SetLabelMatchers(types.Deny, types.KindNode, types.LabelMatchers{
					Expression: `labels.key == "hidden"`,
				})
				require.NoError(b, err)
			},
		},
		{
			desc: "complex labels",
			editRole: func(r types.Role, id string) {
				r.SetNodeLabels(types.Allow, types.Labels{
					"key": {"other", id, "another"},
					"group": {
						`{{regexp.replace(external.group, "^(.*)$", "$1")}}`,
						"{{email.local(external.email)}}",
					},
				})
				r.SetNodeLabels(types.Deny, types.Labels{"key": {"hidden"}})
			},
		},
		{
			desc: "complex expression",
			editRole: func(r types.Role, id string) {
				expr := fmt.Sprintf(
					`(labels.key == "other" || labels.key == %q || labels.key == "another") &&
					 (contains(email.local(user.spec.traits["email"]), labels["group"]) ||
						 contains(regexp.replace(user.spec.traits["group"], "^(.*)$", "$1"), labels["group"]))`,
					id)
				err := r.SetLabelMatchers(types.Allow, types.KindNode, types.LabelMatchers{
					Expression: expr,
				})
				require.NoError(b, err)
				err = r.SetLabelMatchers(types.Deny, types.KindNode, types.LabelMatchers{
					Expression: `labels.key == "hidden"`,
				})
				require.NoError(b, err)
			},
		},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			benchmarkListNodes(
				b, ctx,
				nodeCount, roleCount, hiddenNodes,
				srv,
				ids,
				tc.editRole,
			)
		})
	}
}

func benchmarkListNodes(
	b *testing.B, ctx context.Context,
	nodeCount, roleCount, hiddenNodes int,
	srv *TestTLSServer,
	ids []string,
	editRole func(r types.Role, id string),
) {
	var roles []types.Role
	for _, id := range ids {
		role, err := types.NewRole(fmt.Sprintf("role-%s", id), types.RoleSpecV6{})
		require.NoError(b, err)
		editRole(role, id)
		roles = append(roles, role)
	}

	// create user, role, and client
	username := "user"

	user, err := CreateUser(srv.Auth(), username, roles...)
	require.NoError(b, err)
	user.SetTraits(map[string][]string{
		"group": {"users"},
		"email": {"test@example.com"},
	})
	err = srv.Auth().UpsertUser(user)
	require.NoError(b, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		var resources []types.ResourceWithLabels
		req := proto.ListResourcesRequest{
			ResourceType: types.KindNode,
			Namespace:    defaults.Namespace,
			Limit:        1_000,
		}
		for {
			rsp, err := clt.ListResources(ctx, req)
			require.NoError(b, err)

			resources = append(resources, rsp.Resources...)
			req.StartKey = rsp.NextKey
			if req.StartKey == "" {
				break
			}
		}
		require.Len(b, resources, nodeCount-hiddenNodes)
	}
}

// TestGetAndList_Nodes users can retrieve nodes with various filters
// and with the appropriate permissions.
func TestGetAndList_Nodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test nodes.
	for i := 0; i < 10; i++ {
		name := uuid.New().String()
		node, err := types.NewServerWithLabels(
			name,
			types.KindNode,
			types.ServerSpecV2{},
			map[string]string{"name": name},
		)
		require.NoError(t, err)

		_, err = srv.Auth().UpsertNode(ctx, node)
		require.NoError(t, err)
	}

	testNodes, err := srv.Auth().GetNodes(ctx, defaults.Namespace)
	require.NoError(t, err)

	// create user, role, and client
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// permit user to list all nodes
	role.SetNodeLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	// Convert nodes retrieved earlier as types.ResourcesWithLabels
	testResources := make([]types.ResourceWithLabels, len(testNodes))
	for i, node := range testNodes {
		testResources[i] = node
	}

	// listing nodes 0-4 should list first 5 nodes
	resp, err := clt.ListResources(ctx, proto.ListResourcesRequest{
		ResourceType: types.KindNode,
		Namespace:    defaults.Namespace,
		Limit:        5,
	})
	require.NoError(t, err)
	require.Len(t, resp.Resources, 5)
	expectedNodes := testResources[:5]
	require.Empty(t, cmp.Diff(expectedNodes, resp.Resources))

	// remove permission for third node
	role.SetNodeLabels(types.Deny, types.Labels{"name": {testResources[3].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	// listing nodes 0-4 should skip the third node and add the fifth to the end.
	resp, err = clt.ListResources(ctx, proto.ListResourcesRequest{
		ResourceType: types.KindNode,
		Namespace:    defaults.Namespace,
		Limit:        5,
	})
	require.NoError(t, err)
	require.Len(t, resp.Resources, 5)
	expectedNodes = append(testResources[:3], testResources[4:6]...)
	require.Empty(t, cmp.Diff(expectedNodes, resp.Resources))

	// Test various filtering.
	baseRequest := proto.ListResourcesRequest{
		ResourceType: types.KindNode,
		Namespace:    defaults.Namespace,
		Limit:        int32(len(testResources) + 1),
	}

	// Test label match.
	withLabels := baseRequest
	withLabels.Labels = map[string]string{"name": testResources[0].GetName()}
	resp, err = clt.ListResources(ctx, withLabels)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test search keywords match.
	withSearchKeywords := baseRequest
	withSearchKeywords.SearchKeywords = []string{"name", testResources[0].GetName()}
	resp, err = clt.ListResources(ctx, withSearchKeywords)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test expression match.
	withExpression := baseRequest
	withExpression.PredicateExpression = fmt.Sprintf(`labels.name == "%s"`, testResources[0].GetName())
	resp, err = clt.ListResources(ctx, withExpression)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))
}

// TestStreamSessionEventsRBAC ensures that session events can not be streamed
// by users who lack the read permission on the session resource.
func TestStreamSessionEventsRBAC(t *testing.T) {
	t.Parallel()

	role, err := types.NewRole("deny-sessions", types.RoleSpecV6{
		Allow: types.RoleConditions{
			NodeLabels: types.Labels{
				"*": []string{types.Wildcard},
			},
		},
		Deny: types.RoleConditions{
			Rules: []types.Rule{
				types.NewRule(types.KindSession, []string{types.VerbRead}),
			},
		},
	})
	require.NoError(t, err)

	srv := newTestTLSServer(t)

	user, err := CreateUser(srv.Auth(), "user", role)
	require.NoError(t, err)

	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	_, errC := clt.StreamSessionEvents(context.Background(), "foo", 0)
	select {
	case err := <-errC:
		require.True(t, trace.IsAccessDenied(err), "expected access denied error, got %v", err)
	case <-time.After(1 * time.Second):
		require.FailNow(t, "expected access denied error but stream succeeded")
	}
}

// TestStreamSessionEvents_User ensures that when a user streams a session's events, it emits an audit event.
func TestStreamSessionEvents_User(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	srv := newTestTLSServer(t)

	username := "user"
	user, _, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)

	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// ignore the response as we don't want the events or the error (the session will not exist)
	_, _ = clt.StreamSessionEvents(ctx, "44c6cea8-362f-11ea-83aa-125400432324", 0)

	// we need to wait for a short period to ensure the event is returned
	time.Sleep(500 * time.Millisecond)

	searchEvents, _, err := srv.AuthServer.AuditLog.SearchEvents(ctx, events.SearchEventsRequest{
		From:       srv.Clock().Now().Add(-time.Hour),
		To:         srv.Clock().Now().Add(time.Hour),
		EventTypes: []string{events.SessionRecordingAccessEvent},
		Limit:      1,
		Order:      types.EventOrderDescending,
	})
	require.NoError(t, err)

	event := searchEvents[0].(*apievents.SessionRecordingAccess)
	require.Equal(t, username, event.User)
}

// TestStreamSessionEvents_Builtin ensures that when a builtin role streams a session's events, it does not emit
// an audit event.
func TestStreamSessionEvents_Builtin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	srv := newTestTLSServer(t)

	identity := TestBuiltin(types.RoleProxy)
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// ignore the response as we don't want the events or the error (the session will not exist)
	_, _ = clt.StreamSessionEvents(ctx, "44c6cea8-362f-11ea-83aa-125400432324", 0)

	// we need to wait for a short period to ensure the event is returned
	time.Sleep(500 * time.Millisecond)

	searchEvents, _, err := srv.AuthServer.AuditLog.SearchEvents(ctx, events.SearchEventsRequest{
		From:       srv.Clock().Now().Add(-time.Hour),
		To:         srv.Clock().Now().Add(time.Hour),
		EventTypes: []string{events.SessionRecordingAccessEvent},
		Limit:      1,
		Order:      types.EventOrderDescending,
	})
	require.NoError(t, err)

	require.Equal(t, 0, len(searchEvents))
}

// TestGetSessionEvents ensures that when a user streams a session's events, it emits an audit event.
func TestGetSessionEvents(t *testing.T) {
	t.Parallel()

	srv := newTestTLSServer(t)

	username := "user"
	user, _, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)

	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// ignore the response as we don't want the events or the error (the session will not exist)
	_, _ = clt.GetSessionEvents(defaults.Namespace, "44c6cea8-362f-11ea-83aa-125400432324", 0)

	// we need to wait for a short period to ensure the event is returned
	time.Sleep(500 * time.Millisecond)
	ctx := context.Background()
	searchEvents, _, err := srv.AuthServer.AuditLog.SearchEvents(ctx, events.SearchEventsRequest{
		From:       srv.Clock().Now().Add(-time.Hour),
		To:         srv.Clock().Now().Add(time.Hour),
		EventTypes: []string{events.SessionRecordingAccessEvent},
		Limit:      1,
		Order:      types.EventOrderDescending,
	})
	require.NoError(t, err)

	event := searchEvents[0].(*apievents.SessionRecordingAccess)
	require.Equal(t, username, event.User)
}

// TestAPILockedOut tests Auth API when there are locks involved.
func TestAPILockedOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create user, role and client.
	user, role, err := CreateUserAndRole(srv.Auth(), "test-user", nil, nil)
	require.NoError(t, err)
	clt, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)

	// Prepare an operation requiring authorization.
	testOp := func() error {
		_, err := clt.GetUser(user.GetName(), false)
		return err
	}

	// With no locks, the operation should pass with no error.
	require.NoError(t, testOp())

	// With a lock targeting the user, the operation should be denied.
	lock, err := types.NewLock("user-lock", types.LockSpecV2{
		Target: types.LockTarget{User: user.GetName()},
	})
	require.NoError(t, err)
	require.NoError(t, srv.Auth().UpsertLock(ctx, lock))
	require.Eventually(t, func() bool { return trace.IsAccessDenied(testOp()) }, time.Second, time.Second/10)

	// Delete the lock.
	require.NoError(t, srv.Auth().DeleteLock(ctx, lock.GetName()))
	require.Eventually(t, func() bool { return testOp() == nil }, time.Second, time.Second/10)

	// Create a new lock targeting the user's role.
	roleLock, err := types.NewLock("role-lock", types.LockSpecV2{
		Target: types.LockTarget{Role: role.GetName()},
	})
	require.NoError(t, err)
	require.NoError(t, srv.Auth().UpsertLock(ctx, roleLock))
	require.Eventually(t, func() bool { return trace.IsAccessDenied(testOp()) }, time.Second, time.Second/10)
}

func serverWithAllowRules(t *testing.T, srv *TestAuthServer, allowRules []types.Rule) *ServerWithRoles {
	username := "test-user"
	_, role, err := CreateUserAndRoleWithoutRoles(srv.AuthServer, username, nil)
	require.NoError(t, err)
	role.SetRules(types.Allow, allowRules)
	err = srv.AuthServer.UpsertRole(context.TODO(), role)
	require.NoError(t, err)

	localUser := authz.LocalUser{Username: username, Identity: tlsca.Identity{Username: username}}
	authContext, err := authz.ContextForLocalUser(localUser, srv.AuthServer, srv.ClusterName, true /* disableDeviceAuthz */)
	require.NoError(t, err)

	return &ServerWithRoles{
		authServer: srv.AuthServer,
		alog:       srv.AuditLog,
		context:    *authContext,
	}
}

// TestDatabasesCRUDRBAC verifies RBAC is applied to database CRUD methods.
func TestDatabasesCRUDRBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Setup a couple of users:
	// - "dev" only has access to databases with labels env=dev
	// - "admin" has access to all databases
	dev, devRole, err := CreateUserAndRole(srv.Auth(), "dev", nil, nil)
	require.NoError(t, err)
	devRole.SetDatabaseLabels(types.Allow, types.Labels{"env": {"dev"}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, devRole))
	devClt, err := srv.NewClient(TestUser(dev.GetName()))
	require.NoError(t, err)

	admin, adminRole, err := CreateUserAndRole(srv.Auth(), "admin", nil, nil)
	require.NoError(t, err)
	adminRole.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, adminRole))
	adminClt, err := srv.NewClient(TestUser(admin.GetName()))
	require.NoError(t, err)

	// Prepare a couple of database resources.
	devDatabase, err := types.NewDatabaseV3(types.Metadata{
		Name:   "dev",
		Labels: map[string]string{"env": "dev", types.OriginLabel: types.OriginDynamic},
	}, types.DatabaseSpecV3{
		Protocol: libdefaults.ProtocolPostgres,
		URI:      "localhost:5432",
	})
	require.NoError(t, err)
	adminDatabase, err := types.NewDatabaseV3(types.Metadata{
		Name:   "admin",
		Labels: map[string]string{"env": "prod", types.OriginLabel: types.OriginDynamic},
	}, types.DatabaseSpecV3{
		Protocol: libdefaults.ProtocolMySQL,
		URI:      "localhost:3306",
	})
	require.NoError(t, err)

	// Dev shouldn't be able to create prod database...
	err = devClt.CreateDatabase(ctx, adminDatabase)
	require.True(t, trace.IsAccessDenied(err))

	// ... but can create dev database.
	err = devClt.CreateDatabase(ctx, devDatabase)
	require.NoError(t, err)

	// Admin can create prod database.
	err = adminClt.CreateDatabase(ctx, adminDatabase)
	require.NoError(t, err)

	// Dev shouldn't be able to update prod database...
	err = devClt.UpdateDatabase(ctx, adminDatabase)
	require.True(t, trace.IsAccessDenied(err))

	// ... but can update dev database.
	err = devClt.UpdateDatabase(ctx, devDatabase)
	require.NoError(t, err)

	// Dev shouldn't be able to update labels on the prod database.
	adminDatabase.SetStaticLabels(map[string]string{"env": "dev", types.OriginLabel: types.OriginDynamic})
	err = devClt.UpdateDatabase(ctx, adminDatabase)
	require.True(t, trace.IsAccessDenied(err))
	adminDatabase.SetStaticLabels(map[string]string{"env": "prod", types.OriginLabel: types.OriginDynamic}) // Reset.

	// Dev shouldn't be able to get prod database...
	_, err = devClt.GetDatabase(ctx, adminDatabase.GetName())
	require.True(t, trace.IsAccessDenied(err))

	// ... but can get dev database.
	db, err := devClt.GetDatabase(ctx, devDatabase.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(devDatabase, db,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin can get both databases.
	db, err = adminClt.GetDatabase(ctx, adminDatabase.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(adminDatabase, db,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))
	db, err = adminClt.GetDatabase(ctx, devDatabase.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(devDatabase, db,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// When listing databases, dev should only see one.
	dbs, err := devClt.GetDatabases(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Database{devDatabase}, dbs,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin should see both.
	dbs, err = adminClt.GetDatabases(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Database{adminDatabase, devDatabase}, dbs,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Dev shouldn't be able to delete dev database...
	err = devClt.DeleteDatabase(ctx, adminDatabase.GetName())
	require.True(t, trace.IsAccessDenied(err))

	// ... but can delete dev database.
	err = devClt.DeleteDatabase(ctx, devDatabase.GetName())
	require.NoError(t, err)

	// Admin should be able to delete admin database.
	err = adminClt.DeleteDatabase(ctx, adminDatabase.GetName())
	require.NoError(t, err)

	// Create both databases again to test "delete all" functionality.
	require.NoError(t, devClt.CreateDatabase(ctx, devDatabase))
	require.NoError(t, adminClt.CreateDatabase(ctx, adminDatabase))

	// Dev should only be able to delete dev database.
	err = devClt.DeleteAllDatabases(ctx)
	require.NoError(t, err)
	dbs, err = adminClt.GetDatabases(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Database{adminDatabase}, dbs,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin should be able to delete all.
	err = adminClt.DeleteAllDatabases(ctx)
	require.NoError(t, err)
	dbs, err = adminClt.GetDatabases(ctx)
	require.NoError(t, err)
	require.Len(t, dbs, 0)
}

func TestGetAndList_DatabaseServers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test databases.
	for i := 0; i < 5; i++ {
		name := uuid.New().String()
		db, err := types.NewDatabaseServerV3(types.Metadata{
			Name:   name,
			Labels: map[string]string{"name": name},
		}, types.DatabaseServerSpecV3{
			Protocol: "postgres",
			URI:      "example.com",
			Hostname: "host",
			HostID:   "hostid",
		})
		require.NoError(t, err)

		_, err = srv.Auth().UpsertDatabaseServer(ctx, db)
		require.NoError(t, err)
	}

	testServers, err := srv.Auth().GetDatabaseServers(ctx, defaults.Namespace)
	require.NoError(t, err)

	testResources := make([]types.ResourceWithLabels, len(testServers))
	for i, server := range testServers {
		testResources[i] = server
	}

	// create user, role, and client
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	listRequest := proto.ListResourcesRequest{
		Namespace: defaults.Namespace,
		// Guarantee that the list will all the servers.
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindDatabaseServer,
	}

	// permit user to get the first database
	role.SetDatabaseLabels(types.Allow, types.Labels{"name": {testServers[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err := clt.GetDatabaseServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Empty(t, cmp.Diff(testServers[0:1], servers))
	resp, err := clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// permit user to get all databases
	role.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetDatabaseServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, len(testServers), len(servers))
	require.Empty(t, cmp.Diff(testServers, servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources))
	require.Empty(t, cmp.Diff(testResources, resp.Resources))

	// Test various filtering.
	baseRequest := proto.ListResourcesRequest{
		Namespace:    defaults.Namespace,
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindDatabaseServer,
	}

	// list only database with label
	withLabels := baseRequest
	withLabels.Labels = map[string]string{"name": testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withLabels)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test search keywords match.
	withSearchKeywords := baseRequest
	withSearchKeywords.SearchKeywords = []string{"name", testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withSearchKeywords)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test expression match.
	withExpression := baseRequest
	withExpression.PredicateExpression = fmt.Sprintf(`labels.name == "%s"`, testServers[0].GetName())
	resp, err = clt.ListResources(ctx, withExpression)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// deny user to get the first database
	role.SetDatabaseLabels(types.Deny, types.Labels{"name": {testServers[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetDatabaseServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, len(testServers[1:]), len(servers))
	require.Empty(t, cmp.Diff(testServers[1:], servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources[1:]))
	require.Empty(t, cmp.Diff(testResources[1:], resp.Resources))

	// deny user to get all databases
	role.SetDatabaseLabels(types.Deny, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetDatabaseServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.Empty(t, servers)
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Empty(t, resp.Resources)
}

// TestGetAndList_ApplicationServers verifies RBAC and filtering is applied when fetching app servers.
func TestGetAndList_ApplicationServers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test app servers.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("app-%v", i)
		app, err := types.NewAppV3(types.Metadata{
			Name:   name,
			Labels: map[string]string{"name": name},
		},
			types.AppSpecV3{URI: "localhost"})
		require.NoError(t, err)
		server, err := types.NewAppServerV3FromApp(app, "host", "hostid")
		require.NoError(t, err)

		_, err = srv.Auth().UpsertApplicationServer(ctx, server)
		require.NoError(t, err)
	}

	testServers, err := srv.Auth().GetApplicationServers(ctx, defaults.Namespace)
	require.NoError(t, err)

	testResources := make([]types.ResourceWithLabels, len(testServers))
	for i, server := range testServers {
		testResources[i] = server
	}

	listRequest := proto.ListResourcesRequest{
		Namespace: defaults.Namespace,
		// Guarantee that the list will all the servers.
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindAppServer,
	}

	// create user, role, and client
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// permit user to get the first app
	role.SetAppLabels(types.Allow, types.Labels{"name": {testServers[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err := clt.GetApplicationServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, 1, len(servers))
	require.Empty(t, cmp.Diff(testServers[0:1], servers))
	resp, err := clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// permit user to get all apps
	role.SetAppLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetApplicationServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, len(testServers), len(servers))
	require.Empty(t, cmp.Diff(testServers, servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources))
	require.Empty(t, cmp.Diff(testResources, resp.Resources))

	// Test various filtering.
	baseRequest := proto.ListResourcesRequest{
		Namespace:    defaults.Namespace,
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindAppServer,
	}

	// list only application with label
	withLabels := baseRequest
	withLabels.Labels = map[string]string{"name": testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withLabels)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test search keywords match.
	withSearchKeywords := baseRequest
	withSearchKeywords.SearchKeywords = []string{"name", testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withSearchKeywords)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test expression match.
	withExpression := baseRequest
	withExpression.PredicateExpression = fmt.Sprintf(`labels.name == "%s"`, testServers[0].GetName())
	resp, err = clt.ListResources(ctx, withExpression)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// deny user to get the first app
	role.SetAppLabels(types.Deny, types.Labels{"name": {testServers[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetApplicationServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, len(testServers[1:]), len(servers))
	require.Empty(t, cmp.Diff(testServers[1:], servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources[1:]))
	require.Empty(t, cmp.Diff(testResources[1:], resp.Resources))

	// deny user to get all apps
	role.SetAppLabels(types.Deny, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetApplicationServers(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Empty(t, resp.Resources)
}

// TestApps verifies RBAC is applied to app resources.
func TestApps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Setup a couple of users:
	// - "dev" only has access to apps with labels env=dev
	// - "admin" has access to all apps
	dev, devRole, err := CreateUserAndRole(srv.Auth(), "dev", nil, nil)
	require.NoError(t, err)
	devRole.SetAppLabels(types.Allow, types.Labels{"env": {"dev"}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, devRole))
	devClt, err := srv.NewClient(TestUser(dev.GetName()))
	require.NoError(t, err)

	admin, adminRole, err := CreateUserAndRole(srv.Auth(), "admin", nil, nil)
	require.NoError(t, err)
	adminRole.SetAppLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, adminRole))
	adminClt, err := srv.NewClient(TestUser(admin.GetName()))
	require.NoError(t, err)

	// Prepare a couple of app resources.
	devApp, err := types.NewAppV3(types.Metadata{
		Name:   "dev",
		Labels: map[string]string{"env": "dev", types.OriginLabel: types.OriginDynamic},
	}, types.AppSpecV3{
		URI: "localhost1",
	})
	require.NoError(t, err)
	adminApp, err := types.NewAppV3(types.Metadata{
		Name:   "admin",
		Labels: map[string]string{"env": "prod", types.OriginLabel: types.OriginDynamic},
	}, types.AppSpecV3{
		URI: "localhost2",
	})
	require.NoError(t, err)

	// Dev shouldn't be able to create prod app...
	err = devClt.CreateApp(ctx, adminApp)
	require.True(t, trace.IsAccessDenied(err))

	// ... but can create dev app.
	err = devClt.CreateApp(ctx, devApp)
	require.NoError(t, err)

	// Admin can create prod app.
	err = adminClt.CreateApp(ctx, adminApp)
	require.NoError(t, err)

	// Dev shouldn't be able to update prod app...
	err = devClt.UpdateApp(ctx, adminApp)
	require.True(t, trace.IsAccessDenied(err))

	// ... but can update dev app.
	err = devClt.UpdateApp(ctx, devApp)
	require.NoError(t, err)

	// Dev shouldn't be able to update labels on the prod app.
	adminApp.SetStaticLabels(map[string]string{"env": "dev", types.OriginLabel: types.OriginDynamic})
	err = devClt.UpdateApp(ctx, adminApp)
	require.True(t, trace.IsAccessDenied(err))
	adminApp.SetStaticLabels(map[string]string{"env": "prod", types.OriginLabel: types.OriginDynamic}) // Reset.

	// Dev shouldn't be able to get prod app...
	_, err = devClt.GetApp(ctx, adminApp.GetName())
	require.True(t, trace.IsAccessDenied(err))

	// ... but can get dev app.
	app, err := devClt.GetApp(ctx, devApp.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(devApp, app,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin can get both apps.
	app, err = adminClt.GetApp(ctx, adminApp.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(adminApp, app,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))
	app, err = adminClt.GetApp(ctx, devApp.GetName())
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(devApp, app,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// When listing apps, dev should only see one.
	apps, err := devClt.GetApps(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Application{devApp}, apps,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin should see both.
	apps, err = adminClt.GetApps(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Application{adminApp, devApp}, apps,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Dev shouldn't be able to delete dev app...
	err = devClt.DeleteApp(ctx, adminApp.GetName())
	require.True(t, trace.IsAccessDenied(err))

	// ... but can delete dev app.
	err = devClt.DeleteApp(ctx, devApp.GetName())
	require.NoError(t, err)

	// Admin should be able to delete admin app.
	err = adminClt.DeleteApp(ctx, adminApp.GetName())
	require.NoError(t, err)

	// Create both apps again to test "delete all" functionality.
	require.NoError(t, devClt.CreateApp(ctx, devApp))
	require.NoError(t, adminClt.CreateApp(ctx, adminApp))

	// Dev should only be able to delete dev app.
	err = devClt.DeleteAllApps(ctx)
	require.NoError(t, err)
	apps, err = adminClt.GetApps(ctx)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]types.Application{adminApp}, apps,
		cmpopts.IgnoreFields(types.Metadata{}, "ID"),
	))

	// Admin should be able to delete all.
	err = adminClt.DeleteAllApps(ctx)
	require.NoError(t, err)
	apps, err = adminClt.GetApps(ctx)
	require.NoError(t, err)
	require.Len(t, apps, 0)
}

// TestReplaceRemoteLocksRBAC verifies that only a remote proxy may replace the
// remote locks associated with its cluster.
func TestReplaceRemoteLocksRBAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	user, _, err := CreateUserAndRole(srv.AuthServer, "test-user", []string{}, nil)
	require.NoError(t, err)

	targetCluster := "cluster"
	tests := []struct {
		desc     string
		identity TestIdentity
		checkErr func(error) bool
	}{
		{
			desc:     "users may not replace remote locks",
			identity: TestUser(user.GetName()),
			checkErr: trace.IsAccessDenied,
		},
		{
			desc:     "local proxy may not replace remote locks",
			identity: TestBuiltin(types.RoleProxy),
			checkErr: trace.IsAccessDenied,
		},
		{
			desc:     "remote proxy of a non-target cluster may not replace the target's remote locks",
			identity: TestRemoteBuiltin(types.RoleProxy, "non-"+targetCluster),
			checkErr: trace.IsAccessDenied,
		},
		{
			desc:     "remote proxy of the target cluster may replace its remote locks",
			identity: TestRemoteBuiltin(types.RoleProxy, targetCluster),
			checkErr: func(err error) bool { return err == nil },
		},
	}

	lock, err := types.NewLock("test-lock", types.LockSpecV2{Target: types.LockTarget{User: "test-user"}})
	require.NoError(t, err)

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, test.identity.I))
			require.NoError(t, err)

			s := &ServerWithRoles{
				authServer: srv.AuthServer,
				alog:       srv.AuditLog,
				context:    *authContext,
			}

			err = s.ReplaceRemoteLocks(ctx, targetCluster, []types.Lock{lock})
			require.True(t, test.checkErr(err), trace.DebugReport(err))
		})
	}
}

// TestIsMFARequiredMFADB tests isMFARequest logic per database protocol where different role matchers are used.
func TestIsMFARequiredMFADB(t *testing.T) {
	const (
		databaseName = "test-database"
		userName     = "test-username"
	)

	type modifyRoleFunc func(role types.Role)
	tests := []struct {
		name               string
		userRoleRequireMFA bool
		checkMFA           require.BoolAssertionFunc
		modifyRoleFunc     modifyRoleFunc
		dbProtocol         string
		req                *proto.IsMFARequiredRequest
	}{
		{
			name:       "RequireSessionMFA on MySQL protocol doesn't match database name",
			dbProtocol: libdefaults.ProtocolMySQL,
			req: &proto.IsMFARequiredRequest{
				Target: &proto.IsMFARequiredRequest_Database{
					Database: &proto.RouteToDatabase{
						ServiceName: databaseName,
						Protocol:    libdefaults.ProtocolMySQL,
						Username:    userName,
						Database:    "example",
					},
				},
			},
			modifyRoleFunc: func(role types.Role) {
				roleOpt := role.GetOptions()
				roleOpt.RequireMFAType = types.RequireMFAType_SESSION
				role.SetOptions(roleOpt)

				role.SetDatabaseUsers(types.Allow, []string{types.Wildcard})
				role.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
				role.SetDatabaseNames(types.Allow, nil)
			},
			checkMFA: require.True,
		},
		{
			name:       "RequireSessionMFA off",
			dbProtocol: libdefaults.ProtocolMySQL,
			req: &proto.IsMFARequiredRequest{
				Target: &proto.IsMFARequiredRequest_Database{
					Database: &proto.RouteToDatabase{
						ServiceName: databaseName,
						Protocol:    libdefaults.ProtocolMySQL,
						Username:    userName,
						Database:    "example",
					},
				},
			},
			modifyRoleFunc: func(role types.Role) {
				roleOpt := role.GetOptions()
				roleOpt.RequireMFAType = types.RequireMFAType_OFF
				role.SetOptions(roleOpt)

				role.SetDatabaseUsers(types.Allow, []string{types.Wildcard})
				role.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
				role.SetDatabaseNames(types.Allow, nil)
			},
			checkMFA: require.False,
		},
		{
			name:       "RequireSessionMFA on Postgres protocol database name doesn't match",
			dbProtocol: libdefaults.ProtocolPostgres,
			req: &proto.IsMFARequiredRequest{
				Target: &proto.IsMFARequiredRequest_Database{
					Database: &proto.RouteToDatabase{
						ServiceName: databaseName,
						Protocol:    libdefaults.ProtocolPostgres,
						Username:    userName,
						Database:    "example",
					},
				},
			},
			modifyRoleFunc: func(role types.Role) {
				roleOpt := role.GetOptions()
				roleOpt.RequireMFAType = types.RequireMFAType_SESSION
				role.SetOptions(roleOpt)

				role.SetDatabaseUsers(types.Allow, []string{types.Wildcard})
				role.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
				role.SetDatabaseNames(types.Allow, nil)
			},
			checkMFA: require.False,
		},
		{
			name:       "RequireSessionMFA on Postgres protocol database name matches",
			dbProtocol: libdefaults.ProtocolPostgres,
			req: &proto.IsMFARequiredRequest{
				Target: &proto.IsMFARequiredRequest_Database{
					Database: &proto.RouteToDatabase{
						ServiceName: databaseName,
						Protocol:    libdefaults.ProtocolPostgres,
						Username:    userName,
						Database:    "example",
					},
				},
			},
			modifyRoleFunc: func(role types.Role) {
				roleOpt := role.GetOptions()
				roleOpt.RequireMFAType = types.RequireMFAType_SESSION
				role.SetOptions(roleOpt)

				role.SetDatabaseUsers(types.Allow, []string{types.Wildcard})
				role.SetDatabaseLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
				role.SetDatabaseNames(types.Allow, []string{"example"})
			},
			checkMFA: require.True,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			srv := newTestTLSServer(t)

			// Enable MFA support.
			authPref, err := types.NewAuthPreference(types.AuthPreferenceSpecV2{
				Type:         constants.Local,
				SecondFactor: constants.SecondFactorOptional,
				Webauthn: &types.Webauthn{
					RPID: "teleport",
				},
			})
			require.NoError(t, err)
			err = srv.Auth().SetAuthPreference(ctx, authPref)
			require.NoError(t, err)

			database, err := types.NewDatabaseServerV3(
				types.Metadata{
					Name: databaseName,
					Labels: map[string]string{
						"env": "dev",
					},
				},
				types.DatabaseServerSpecV3{
					Protocol: tc.dbProtocol,
					URI:      "example.com",
					Hostname: "host",
					HostID:   "hostID",
				},
			)
			require.NoError(t, err)

			_, err = srv.Auth().UpsertDatabaseServer(ctx, database)
			require.NoError(t, err)

			user, role, err := CreateUserAndRole(srv.Auth(), userName, []string{"test-role"}, nil)
			require.NoError(t, err)

			if tc.modifyRoleFunc != nil {
				tc.modifyRoleFunc(role)
			}
			err = srv.Auth().UpsertRole(ctx, role)
			require.NoError(t, err)

			cl, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			resp, err := cl.IsMFARequired(ctx, tc.req)
			require.NoError(t, err)
			tc.checkMFA(t, resp.GetRequired())
		})
	}
}

// TestKindClusterConfig verifies that types.KindClusterConfig can be used
// as an alternative privilege to provide access to cluster configuration
// resources.
func TestKindClusterConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	getClusterConfigResources := func(ctx context.Context, user types.User) []error {
		authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, TestUser(user.GetName()).I))
		require.NoError(t, err, trace.DebugReport(err))
		s := &ServerWithRoles{
			authServer: srv.AuthServer,
			alog:       srv.AuditLog,
			context:    *authContext,
		}
		_, err1 := s.GetClusterAuditConfig(ctx)
		_, err2 := s.GetClusterNetworkingConfig(ctx)
		_, err3 := s.GetSessionRecordingConfig(ctx)
		return []error{err1, err2, err3}
	}

	t.Run("without KindClusterConfig privilege", func(t *testing.T) {
		user, err := CreateUser(srv.AuthServer, "test-user")
		require.NoError(t, err)
		for _, err := range getClusterConfigResources(ctx, user) {
			require.Error(t, err)
			require.True(t, trace.IsAccessDenied(err))
		}
	})

	t.Run("with KindClusterConfig privilege", func(t *testing.T) {
		role, err := types.NewRole("test-role", types.RoleSpecV6{
			Allow: types.RoleConditions{
				Rules: []types.Rule{
					types.NewRule(types.KindClusterConfig, []string{types.VerbRead}),
				},
			},
		})
		require.NoError(t, err)
		user, err := CreateUser(srv.AuthServer, "test-user", role)
		require.NoError(t, err)
		for _, err := range getClusterConfigResources(ctx, user) {
			require.NoError(t, err)
		}
	})
}

func TestGetAndList_KubernetesServers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test kube servers.
	for i := 0; i < 5; i++ {
		// insert legacy kube servers
		name := uuid.NewString()
		cluster, err := types.NewKubernetesClusterV3(
			types.Metadata{
				Name: name, Labels: map[string]string{"name": name},
			},
			types.KubernetesClusterSpecV3{},
		)
		require.NoError(t, err)

		kubeServer, err := types.NewKubernetesServerV3(
			types.Metadata{
				Name: name, Labels: map[string]string{"name": name},
			},

			types.KubernetesServerSpecV3{
				HostID:   name,
				Hostname: "test",
				Cluster:  cluster,
			},
		)
		require.NoError(t, err)

		_, err = srv.Auth().UpsertKubernetesServer(ctx, kubeServer)
		require.NoError(t, err)
	}

	testServers, err := srv.Auth().GetKubernetesServers(ctx)
	require.NoError(t, err)
	require.Len(t, testServers, 5)

	testResources := make([]types.ResourceWithLabels, len(testServers))
	for i, server := range testServers {
		testResources[i] = server
	}

	// create user, role, and client
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	listRequest := proto.ListResourcesRequest{
		Namespace: defaults.Namespace,
		// Guarantee that the list will all the servers.
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindKubeServer,
	}

	// permit user to get all kubernetes service
	role.SetKubernetesLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err := clt.GetKubernetesServers(ctx)
	require.NoError(t, err)
	require.Len(t, testServers, len(testServers))
	require.Empty(t, cmp.Diff(testServers, servers))
	resp, err := clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources))
	require.Empty(t, cmp.Diff(testResources, resp.Resources))

	// Test various filtering.
	baseRequest := proto.ListResourcesRequest{
		Namespace:    defaults.Namespace,
		Limit:        int32(len(testServers) + 1),
		ResourceType: types.KindKubeServer,
	}

	// Test label match.
	withLabels := baseRequest
	withLabels.Labels = map[string]string{"name": testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withLabels)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test search keywords match.
	withSearchKeywords := baseRequest
	withSearchKeywords.SearchKeywords = []string{"name", testServers[0].GetName()}
	resp, err = clt.ListResources(ctx, withSearchKeywords)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test expression match.
	withExpression := baseRequest
	withExpression.PredicateExpression = fmt.Sprintf(`labels.name == "%s"`, testServers[0].GetName())
	resp, err = clt.ListResources(ctx, withExpression)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// deny user to get the first kubernetes service
	role.SetKubernetesLabels(types.Deny, types.Labels{"name": {testServers[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetKubernetesServers(ctx)
	require.NoError(t, err)
	require.Len(t, servers, len(testServers)-1)
	require.Empty(t, cmp.Diff(testServers[1:], servers))
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources)-1)
	require.Empty(t, cmp.Diff(testResources[1:], resp.Resources))

	// deny user to get all databases
	role.SetKubernetesLabels(types.Deny, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	servers, err = clt.GetKubernetesServers(ctx)
	require.NoError(t, err)
	require.Len(t, servers, 0)
	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Empty(t, resp.Resources)
}

func TestListDatabaseServices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	numInitialResources := 5

	// Create test Database Services.
	for i := 0; i < numInitialResources; i++ {
		name := uuid.NewString()
		s, err := types.NewDatabaseServiceV1(types.Metadata{
			Name: name,
		}, types.DatabaseServiceSpecV1{
			ResourceMatchers: []*types.DatabaseResourceMatcher{
				{
					Labels: &types.Labels{
						"env": []string{name},
					},
				},
			},
		})
		require.NoError(t, err)

		_, err = srv.Auth().UpsertDatabaseService(ctx, s)
		require.NoError(t, err)
	}

	listServicesResp, err := srv.Auth().ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.NoError(t, err)
	databaseServices, err := types.ResourcesWithLabels(listServicesResp.Resources).AsDatabaseServices()
	require.NoError(t, err)

	testResources := make([]types.ResourceWithLabels, len(databaseServices))
	for i, server := range databaseServices {
		testResources[i] = server
	}

	// Create user, role, and client
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// User is not allowed to list DatabseServices
	_, err = clt.ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.True(t, trace.IsAccessDenied(err), "expected access denied because role does not allow Read operations")

	// Change the user's role to allow them to list DatabaseServices
	currentAllowRules := role.GetRules(types.Allow)
	role.SetRules(types.Allow, append(currentAllowRules, types.NewRule(types.KindDatabaseService, services.RO())))
	role.SetDatabaseServiceLabels(types.Allow, types.Labels{types.Wildcard: []string{types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	listServicesResp, err = clt.ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.NoError(t, err)
	usersViewDBServices, err := types.ResourcesWithLabels(listServicesResp.Resources).AsDatabaseServices()
	require.NoError(t, err)
	require.Len(t, usersViewDBServices, numInitialResources)

	require.Empty(t, cmp.Diff(databaseServices, usersViewDBServices))

	// User is not allowed to Upsert DatabaseServices
	extraDatabaseService, err := types.NewDatabaseServiceV1(types.Metadata{
		Name: "extra",
	}, types.DatabaseServiceSpecV1{
		ResourceMatchers: []*types.DatabaseResourceMatcher{
			{
				Labels: &types.Labels{
					"env": []string{"extra"},
				},
			},
		},
	})
	require.NoError(t, err)
	_, err = clt.UpsertDatabaseService(ctx, extraDatabaseService)
	require.True(t, trace.IsAccessDenied(err), "expected access denied because role does not allow Create/Update operations")

	currentAllowRules = role.GetRules(types.Allow)
	role.SetRules(types.Allow, append(currentAllowRules, types.NewRule(types.KindDatabaseService, services.RW())))
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	_, err = clt.UpsertDatabaseService(ctx, extraDatabaseService)
	require.NoError(t, err)

	listServicesResp, err = clt.ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.NoError(t, err)
	usersViewDBServices, err = types.ResourcesWithLabels(listServicesResp.Resources).AsDatabaseServices()
	require.NoError(t, err)
	require.Len(t, usersViewDBServices, numInitialResources+1)

	// User can also delete a single or multiple DatabaseServices because they have RW permissions now
	err = clt.DeleteDatabaseService(ctx, extraDatabaseService.GetName())
	require.NoError(t, err)

	listServicesResp, err = clt.ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.NoError(t, err)
	usersViewDBServices, err = types.ResourcesWithLabels(listServicesResp.Resources).AsDatabaseServices()
	require.NoError(t, err)
	require.Len(t, usersViewDBServices, numInitialResources)

	// After removing all resources, we should have 0 resources being returned.
	err = clt.DeleteAllDatabaseServices(ctx)
	require.NoError(t, err)

	listServicesResp, err = clt.ListResources(ctx,
		proto.ListResourcesRequest{
			ResourceType: types.KindDatabaseService,
			Limit:        defaults.DefaultChunkSize,
		},
	)
	require.NoError(t, err)
	usersViewDBServices, err = types.ResourcesWithLabels(listServicesResp.Resources).AsDatabaseServices()
	require.NoError(t, err)
	require.Empty(t, usersViewDBServices)
}

func TestListResources_NeedTotalCountFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test nodes.
	for i := 0; i < 3; i++ {
		name := uuid.New().String()
		node, err := types.NewServerWithLabels(
			name,
			types.KindNode,
			types.ServerSpecV2{},
			map[string]string{"name": name},
		)
		require.NoError(t, err)

		_, err = srv.Auth().UpsertNode(ctx, node)
		require.NoError(t, err)
	}

	testNodes, err := srv.Auth().GetNodes(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.Len(t, testNodes, 3)

	// create user and client
	user, _, err := CreateUserAndRole(srv.Auth(), "user", nil, nil)
	require.NoError(t, err)
	clt, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)

	// Total returned.
	resp, err := clt.ListResources(ctx, proto.ListResourcesRequest{
		ResourceType:   types.KindNode,
		Limit:          2,
		NeedTotalCount: true,
	})
	require.NoError(t, err)
	require.Len(t, resp.Resources, 2)
	require.NotEmpty(t, resp.NextKey)
	require.Equal(t, len(testNodes), resp.TotalCount)

	// No total.
	resp, err = clt.ListResources(ctx, proto.ListResourcesRequest{
		ResourceType: types.KindNode,
		Limit:        2,
	})
	require.NoError(t, err)
	require.Len(t, resp.Resources, 2)
	require.NotEmpty(t, resp.NextKey)
	require.Empty(t, resp.TotalCount)
}

func TestListResources_SearchAsRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test nodes.
	const numTestNodes = 3
	for i := 0; i < numTestNodes; i++ {
		name := fmt.Sprintf("node%d", i)
		node, err := types.NewServerWithLabels(
			name,
			types.KindNode,
			types.ServerSpecV2{},
			map[string]string{"name": name},
		)
		require.NoError(t, err)

		_, err = srv.Auth().UpsertNode(ctx, node)
		require.NoError(t, err)
	}

	testNodes, err := srv.Auth().GetNodes(ctx, defaults.Namespace)
	require.NoError(t, err)
	require.Len(t, testNodes, numTestNodes)

	// create user and client
	user, role, err := CreateUserAndRole(srv.Auth(), "user", []string{"user"}, nil)
	require.NoError(t, err)

	// only allow user to see first node
	role.SetNodeLabels(types.Allow, types.Labels{"name": {testNodes[0].GetName()}})

	// create a new role which can see second node
	searchAsRole := services.RoleForUser(user)
	searchAsRole.SetName("test_search_role")
	searchAsRole.SetNodeLabels(types.Allow, types.Labels{"name": {testNodes[1].GetName()}})
	searchAsRole.SetLogins(types.Allow, []string{"user"})
	require.NoError(t, srv.Auth().UpsertRole(ctx, searchAsRole))

	// create a third role which can see the third node
	previewAsRole := services.RoleForUser(user)
	previewAsRole.SetName("test_preview_role")
	previewAsRole.SetNodeLabels(types.Allow, types.Labels{"name": {testNodes[2].GetName()}})
	previewAsRole.SetLogins(types.Allow, []string{"user"})
	require.NoError(t, srv.Auth().UpsertRole(ctx, previewAsRole))

	role.SetSearchAsRoles(types.Allow, []string{searchAsRole.GetName()})
	role.SetPreviewAsRoles(types.Allow, []string{previewAsRole.GetName()})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	clt, err := srv.NewClient(TestUser(user.GetName()))
	require.NoError(t, err)

	for _, tc := range []struct {
		desc                   string
		requestOpt             func(*proto.ListResourcesRequest)
		expectNodes            []string
		expectSearchEventRoles []string
	}{
		{
			desc:        "basic",
			expectNodes: []string{testNodes[0].GetName()},
		},
		{
			desc: "search as roles",
			requestOpt: func(req *proto.ListResourcesRequest) {
				req.UseSearchAsRoles = true
			},
			expectNodes:            []string{testNodes[0].GetName(), testNodes[1].GetName()},
			expectSearchEventRoles: []string{role.GetName(), searchAsRole.GetName()},
		},
		{
			desc: "preview as roles",
			requestOpt: func(req *proto.ListResourcesRequest) {
				req.UsePreviewAsRoles = true
			},
			expectNodes:            []string{testNodes[0].GetName(), testNodes[2].GetName()},
			expectSearchEventRoles: []string{role.GetName(), previewAsRole.GetName()},
		},
		{
			desc: "both",
			requestOpt: func(req *proto.ListResourcesRequest) {
				req.UseSearchAsRoles = true
				req.UsePreviewAsRoles = true
			},
			expectNodes:            []string{testNodes[0].GetName(), testNodes[1].GetName(), testNodes[2].GetName()},
			expectSearchEventRoles: []string{role.GetName(), searchAsRole.GetName(), previewAsRole.GetName()},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			req := proto.ListResourcesRequest{
				ResourceType: types.KindNode,
				Limit:        int32(len(testNodes)),
			}
			if tc.requestOpt != nil {
				tc.requestOpt(&req)
			}
			resp, err := clt.ListResources(ctx, req)
			require.NoError(t, err)
			require.Len(t, resp.Resources, len(tc.expectNodes))
			var gotNodes []string
			for _, node := range resp.Resources {
				gotNodes = append(gotNodes, node.GetName())
			}
			require.ElementsMatch(t, tc.expectNodes, gotNodes)

			if len(tc.expectSearchEventRoles) > 0 {
				require.Eventually(t, func() bool {
					// make sure an audit event is logged for the search
					auditEvents, _, err := srv.AuthServer.AuditLog.SearchEvents(ctx, events.SearchEventsRequest{
						From:       time.Time{},
						To:         time.Now(),
						EventTypes: []string{events.AccessRequestResourceSearch},
						Limit:      10,
						Order:      types.EventOrderAscending,
					})
					require.NoError(t, err)
					if len(auditEvents) == 0 {
						t.Log("no search audit events found")
						return false
					}
					lastEvent := auditEvents[len(auditEvents)-1].(*apievents.AccessRequestResourceSearch)
					diff := cmp.Diff(tc.expectSearchEventRoles, lastEvent.SearchAsRoles)
					if diff == "" {
						// Found the event we're looking for.
						return true
					}
					t.Logf("most recent search event does not have the expected roles, diff: %s", diff)
					return false
				}, 10*time.Second, 250*time.Millisecond, "did not find expected search event")
			}
		})
	}
}

func TestGetAndList_WindowsDesktops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create test desktops.
	for i := 0; i < 5; i++ {
		name := uuid.New().String()
		desktop, err := types.NewWindowsDesktopV3(name, map[string]string{"name": name},
			types.WindowsDesktopSpecV3{Addr: "_", HostID: "_"})
		require.NoError(t, err)
		require.NoError(t, srv.Auth().UpsertWindowsDesktop(ctx, desktop))
	}

	// Test all has been upserted.
	testDesktops, err := srv.Auth().GetWindowsDesktops(ctx, types.WindowsDesktopFilter{})
	require.NoError(t, err)
	require.Len(t, testDesktops, 5)

	testResources := types.WindowsDesktops(testDesktops).AsResources()

	// Create user, role, and client.
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// Base request.
	listRequest := proto.ListResourcesRequest{
		ResourceType: types.KindWindowsDesktop,
		Limit:        int32(len(testDesktops) + 1),
	}

	// Permit user to get the first desktop.
	role.SetWindowsDesktopLabels(types.Allow, types.Labels{"name": {testDesktops[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	desktops, err := clt.GetWindowsDesktops(ctx, types.WindowsDesktopFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, len(desktops))
	require.Empty(t, cmp.Diff(testDesktops[0:1], desktops))

	resp, err := clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))
	require.Empty(t, resp.NextKey)
	require.Empty(t, resp.TotalCount)

	// Permit user to get all desktops.
	role.SetWindowsDesktopLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))
	desktops, err = clt.GetWindowsDesktops(ctx, types.WindowsDesktopFilter{})
	require.NoError(t, err)
	require.EqualValues(t, len(testDesktops), len(desktops))
	require.Empty(t, cmp.Diff(testDesktops, desktops))

	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources))
	require.Empty(t, cmp.Diff(testResources, resp.Resources))
	require.Empty(t, resp.NextKey)
	require.Empty(t, resp.TotalCount)

	// Test sorting is supported.
	withSort := listRequest
	withSort.SortBy = types.SortBy{IsDesc: true, Field: types.ResourceMetadataName}
	resp, err = clt.ListResources(ctx, withSort)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources))
	desktops, err = types.ResourcesWithLabels(resp.Resources).AsWindowsDesktops()
	require.NoError(t, err)
	names, err := types.WindowsDesktops(desktops).GetFieldVals(types.ResourceMetadataName)
	require.NoError(t, err)
	require.IsDecreasing(t, names)

	// Filter by labels.
	withLabels := listRequest
	withLabels.Labels = map[string]string{"name": testDesktops[0].GetName()}
	resp, err = clt.ListResources(ctx, withLabels)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))
	require.Empty(t, resp.NextKey)
	require.Empty(t, resp.TotalCount)

	// Test search keywords match.
	withSearchKeywords := listRequest
	withSearchKeywords.SearchKeywords = []string{"name", testDesktops[0].GetName()}
	resp, err = clt.ListResources(ctx, withSearchKeywords)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Test predicate match.
	withExpression := listRequest
	withExpression.PredicateExpression = fmt.Sprintf(`labels.name == "%s"`, testDesktops[0].GetName())
	resp, err = clt.ListResources(ctx, withExpression)
	require.NoError(t, err)
	require.Len(t, resp.Resources, 1)
	require.Empty(t, cmp.Diff(testResources[0:1], resp.Resources))

	// Deny user to get the first desktop.
	role.SetWindowsDesktopLabels(types.Deny, types.Labels{"name": {testDesktops[0].GetName()}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	desktops, err = clt.GetWindowsDesktops(ctx, types.WindowsDesktopFilter{})
	require.NoError(t, err)
	require.EqualValues(t, len(testDesktops[1:]), len(desktops))
	require.Empty(t, cmp.Diff(testDesktops[1:], desktops))

	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Len(t, resp.Resources, len(testResources[1:]))
	require.Empty(t, cmp.Diff(testResources[1:], resp.Resources))

	// Deny user all desktops.
	role.SetWindowsDesktopLabels(types.Deny, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	desktops, err = clt.GetWindowsDesktops(ctx, types.WindowsDesktopFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 0, len(desktops))
	require.Empty(t, cmp.Diff([]types.WindowsDesktop{}, desktops))

	resp, err = clt.ListResources(ctx, listRequest)
	require.NoError(t, err)
	require.Empty(t, resp.Resources)
}

func TestListResources_KindKubernetesCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, TestBuiltin(types.RoleProxy).I))
	require.NoError(t, err)

	s := &ServerWithRoles{
		authServer: srv.AuthServer,
		alog:       srv.AuditLog,
		context:    *authContext,
	}

	testNames := []string{"a", "b", "c", "d"}

	// Add a kube service with 3 clusters.
	createKubeServer(t, s, []string{"d", "b", "a"}, "host1")

	// Add a kube service with 2 clusters.
	// Includes a duplicate cluster name to test deduplicate.
	createKubeServer(t, s, []string{"a", "c"}, "host2")

	// Test upsert.
	kubeServers, err := s.GetKubernetesServers(ctx)
	require.NoError(t, err)
	require.Len(t, kubeServers, 5)

	t.Run("fetch all", func(t *testing.T) {
		t.Parallel()

		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindKubernetesCluster,
			Limit:        10,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, len(testNames))
		require.Empty(t, res.NextKey)
		// There is 2 kube services, but 4 unique clusters.
		require.Equal(t, 4, res.TotalCount)

		clusters, err := types.ResourcesWithLabels(res.Resources).AsKubeClusters()
		require.NoError(t, err)
		names, err := types.KubeClusters(clusters).GetFieldVals(types.ResourceMetadataName)
		require.NoError(t, err)
		require.ElementsMatch(t, names, testNames)
	})

	t.Run("start keys", func(t *testing.T) {
		t.Parallel()

		// First fetch.
		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindKubernetesCluster,
			Limit:        1,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, 1)
		require.Equal(t, kubeServers[1].GetCluster().GetName(), res.NextKey)

		// Second fetch.
		res, err = s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindKubernetesCluster,
			Limit:        1,
			StartKey:     res.NextKey,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, 1)
		require.Equal(t, kubeServers[2].GetCluster().GetName(), res.NextKey)
	})

	t.Run("fetch with sort and total count", func(t *testing.T) {
		t.Parallel()
		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindKubernetesCluster,
			Limit:        10,
			SortBy: types.SortBy{
				IsDesc: true,
				Field:  types.ResourceMetadataName,
			},
			NeedTotalCount: true,
		})
		require.NoError(t, err)
		require.Empty(t, res.NextKey)
		require.Len(t, res.Resources, len(testNames))
		require.Equal(t, res.TotalCount, len(testNames))

		clusters, err := types.ResourcesWithLabels(res.Resources).AsKubeClusters()
		require.NoError(t, err)
		names, err := types.KubeClusters(clusters).GetFieldVals(types.ResourceMetadataName)
		require.NoError(t, err)
		require.IsDecreasing(t, names)
	})
}

func createKubeServer(t *testing.T, s *ServerWithRoles, clusterNames []string, hostID string) {
	for _, clusterName := range clusterNames {
		kubeCluster, err := types.NewKubernetesClusterV3(types.Metadata{
			Name: clusterName,
		}, types.KubernetesClusterSpecV3{})
		require.NoError(t, err)
		kubeServer, err := types.NewKubernetesServerV3FromCluster(kubeCluster, hostID, hostID)
		require.NoError(t, err)
		_, err = s.UpsertKubernetesServer(context.Background(), kubeServer)
		require.NoError(t, err)
	}
}

func TestListResources_KindUserGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	role, err := types.NewRole("test-role", types.RoleSpecV6{
		Allow: types.RoleConditions{
			GroupLabels: types.Labels{
				"label": []string{"value"},
			},
			Rules: []types.Rule{
				{
					Resources: []string{types.KindUserGroup},
					Verbs:     []string{types.VerbCreate, types.VerbRead, types.VerbList},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, srv.AuthServer.UpsertRole(ctx, role))

	user, err := types.NewUser("test-user")
	require.NoError(t, err)
	user.AddRole(role.GetName())
	require.NoError(t, srv.AuthServer.UpsertUser(user))

	// Create the admin context so that we can create all the user groups we need.
	authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, TestBuiltin(types.RoleAdmin).I))
	require.NoError(t, err)

	s := &ServerWithRoles{
		authServer: srv.AuthServer,
		alog:       srv.AuditLog,
		context:    *authContext,
	}

	// Add user groups.
	testUg1 := createUserGroup(t, s, "c", map[string]string{"label": "value"})
	testUg2 := createUserGroup(t, s, "a", map[string]string{"label": "value"})
	testUg3 := createUserGroup(t, s, "b", map[string]string{"label": "value"})

	// This user group should never should up because the user doesn't have group label access to it.
	_ = createUserGroup(t, s, "d", map[string]string{"inaccessible": "value"})

	authContext, err = srv.Authorizer.Authorize(authz.ContextWithUser(ctx, TestUser(user.GetName()).I))
	require.NoError(t, err)

	s = &ServerWithRoles{
		authServer: srv.AuthServer,
		alog:       srv.AuditLog,
		context:    *authContext,
	}

	// Test create.
	userGroups, _, err := s.ListUserGroups(ctx, 0, "")
	require.NoError(t, err)
	require.Len(t, userGroups, 3)

	t.Run("fetch all", func(t *testing.T) {
		t.Parallel()

		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindUserGroup,
			Limit:        10,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, 3)
		require.Empty(t, res.NextKey)
		require.Equal(t, 0, res.TotalCount) // TotalCount is 0 because this is not using fake pagination.

		userGroups, err := types.ResourcesWithLabels(res.Resources).AsUserGroups()
		require.NoError(t, err)
		require.ElementsMatch(t, []types.UserGroup{testUg1, testUg2, testUg3}, userGroups)
	})

	t.Run("start keys", func(t *testing.T) {
		t.Parallel()

		// First fetch.
		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindUserGroup,
			Limit:        1,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, 1)
		require.Equal(t, testUg3.GetName(), res.NextKey)

		// Second fetch.
		res, err = s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindUserGroup,
			Limit:        1,
			StartKey:     res.NextKey,
		})
		require.NoError(t, err)
		require.Len(t, res.Resources, 1)
		require.Equal(t, testUg1.GetName(), res.NextKey)
	})

	t.Run("fetch with sort and total count", func(t *testing.T) {
		t.Parallel()
		res, err := s.ListResources(ctx, proto.ListResourcesRequest{
			ResourceType: types.KindUserGroup,
			Limit:        10,
			SortBy: types.SortBy{
				IsDesc: true,
				Field:  types.ResourceMetadataName,
			},
			NeedTotalCount: true,
		})
		require.NoError(t, err)
		require.Empty(t, res.NextKey)
		require.Len(t, res.Resources, 3)
		require.Equal(t, res.TotalCount, 3)

		userGroups, err := types.ResourcesWithLabels(res.Resources).AsUserGroups()
		require.NoError(t, err)
		names := make([]string, 3)
		for i, userGroup := range userGroups {
			names[i] = userGroup.GetName()
		}
		require.IsDecreasing(t, names)
	})
}

func createUserGroup(t *testing.T, s *ServerWithRoles, name string, labels map[string]string) types.UserGroup {
	userGroup, err := types.NewUserGroup(types.Metadata{
		Name:   name,
		Labels: labels,
	}, types.UserGroupSpecV1{})
	require.NoError(t, err)
	err = s.CreateUserGroup(context.Background(), userGroup)
	require.NoError(t, err)
	return userGroup
}

func TestDeleteUserAppSessions(t *testing.T) {
	ctx := context.Background()

	srv := newTestTLSServer(t)
	t.Cleanup(func() { srv.Close() })

	// Generates a new user client.
	userClient := func(username string) *Client {
		user, _, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
		require.NoError(t, err)
		identity := TestUser(user.GetName())
		clt, err := srv.NewClient(identity)
		require.NoError(t, err)
		return clt
	}

	// Register users.
	aliceClt := userClient("alice")
	bobClt := userClient("bob")

	// Register multiple applications.
	applications := []struct {
		name       string
		publicAddr string
	}{
		{name: "panel", publicAddr: "panel.example.com"},
		{name: "admin", publicAddr: "admin.example.com"},
		{name: "metrics", publicAddr: "metrics.example.com"},
	}

	// Register and create a session for each application.
	for _, application := range applications {
		// Register an application.
		app, err := types.NewAppV3(types.Metadata{
			Name: application.name,
		}, types.AppSpecV3{
			URI:        "localhost",
			PublicAddr: application.publicAddr,
		})
		require.NoError(t, err)
		server, err := types.NewAppServerV3FromApp(app, "host", uuid.New().String())
		require.NoError(t, err)
		_, err = srv.Auth().UpsertApplicationServer(ctx, server)
		require.NoError(t, err)

		// Create a session for alice.
		_, err = aliceClt.CreateAppSession(ctx, types.CreateAppSessionRequest{
			Username:    "alice",
			PublicAddr:  application.publicAddr,
			ClusterName: "localhost",
		})
		require.NoError(t, err)

		// Create a session for bob.
		_, err = bobClt.CreateAppSession(ctx, types.CreateAppSessionRequest{
			Username:    "bob",
			PublicAddr:  application.publicAddr,
			ClusterName: "localhost",
		})
		require.NoError(t, err)
	}

	// Ensure the correct number of sessions.
	sessions, nextKey, err := srv.Auth().ListAppSessions(ctx, 10, "", "")
	require.NoError(t, err)
	require.Empty(t, nextKey)
	require.Len(t, sessions, 6)

	// Try to delete other user app sessions.
	err = aliceClt.DeleteUserAppSessions(ctx, &proto.DeleteUserAppSessionsRequest{Username: "bob"})
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))

	err = bobClt.DeleteUserAppSessions(ctx, &proto.DeleteUserAppSessionsRequest{Username: "alice"})
	require.Error(t, err)
	require.True(t, trace.IsAccessDenied(err))

	// Delete alice sessions.
	err = aliceClt.DeleteUserAppSessions(ctx, &proto.DeleteUserAppSessionsRequest{Username: "alice"})
	require.NoError(t, err)

	// Check if only bob's sessions are left.
	sessions, nextKey, err = srv.Auth().ListAppSessions(ctx, 10, "", "bob")
	require.NoError(t, err)
	require.Empty(t, nextKey)
	require.Len(t, sessions, 3)
	for _, session := range sessions {
		require.Equal(t, "bob", session.GetUser())
	}

	sessions, nextKey, err = srv.Auth().ListAppSessions(ctx, 10, "", "alice")
	require.NoError(t, err)
	require.Empty(t, sessions)
	require.Empty(t, nextKey)

	// Delete bob sessions.
	err = bobClt.DeleteUserAppSessions(ctx, &proto.DeleteUserAppSessionsRequest{Username: "bob"})
	require.NoError(t, err)

	// No sessions left.
	sessions, nextKey, err = srv.Auth().ListAppSessions(ctx, 10, "", "")
	require.NoError(t, err)
	require.Len(t, sessions, 0)
	require.Empty(t, nextKey)
}

func TestListResources_SortAndDeduplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	// Create user, role, and client.
	username := "user"
	user, role, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
	require.NoError(t, err)
	identity := TestUser(user.GetName())
	clt, err := srv.NewClient(identity)
	require.NoError(t, err)

	// Permit user to get all resources.
	role.SetWindowsDesktopLabels(types.Allow, types.Labels{types.Wildcard: {types.Wildcard}})
	require.NoError(t, srv.Auth().UpsertRole(ctx, role))

	// Define some resource names for testing.
	names := []string{"d", "b", "d", "a", "a", "b"}
	uniqueNames := []string{"a", "b", "d"}

	tests := []struct {
		name            string
		kind            string
		insertResources func()
		wantNames       []string
	}{
		{
			name: "KindDatabaseServer",
			kind: types.KindDatabaseServer,
			insertResources: func() {
				for i := 0; i < len(names); i++ {
					db, err := types.NewDatabaseServerV3(types.Metadata{
						Name: fmt.Sprintf("name-%v", i),
					}, types.DatabaseServerSpecV3{
						HostID:   "_",
						Hostname: "_",
						Database: &types.DatabaseV3{
							Metadata: types.Metadata{
								Name: names[i],
							},
							Spec: types.DatabaseSpecV3{
								Protocol: "_",
								URI:      "_",
							},
						},
					})
					require.NoError(t, err)
					_, err = srv.Auth().UpsertDatabaseServer(ctx, db)
					require.NoError(t, err)
				}
			},
		},
		{
			name: "KindAppServer",
			kind: types.KindAppServer,
			insertResources: func() {
				for i := 0; i < len(names); i++ {
					server, err := types.NewAppServerV3(types.Metadata{
						Name: fmt.Sprintf("name-%v", i),
					}, types.AppServerSpecV3{
						HostID: "_",
						App:    &types.AppV3{Metadata: types.Metadata{Name: names[i]}, Spec: types.AppSpecV3{URI: "_"}},
					})
					require.NoError(t, err)
					_, err = srv.Auth().UpsertApplicationServer(ctx, server)
					require.NoError(t, err)
				}
			},
		},
		{
			name: "KindWindowsDesktop",
			kind: types.KindWindowsDesktop,
			insertResources: func() {
				for i := 0; i < len(names); i++ {
					desktop, err := types.NewWindowsDesktopV3(names[i], nil, types.WindowsDesktopSpecV3{
						Addr:   "_",
						HostID: fmt.Sprintf("name-%v", i),
					})
					require.NoError(t, err)
					require.NoError(t, srv.Auth().UpsertWindowsDesktop(ctx, desktop))
				}
			},
		},
		{
			name: "KindKubernetesCluster",
			kind: types.KindKubernetesCluster,
			insertResources: func() {
				for i := 0; i < len(names); i++ {

					kube, err := types.NewKubernetesClusterV3(types.Metadata{
						Name: names[i],
					}, types.KubernetesClusterSpecV3{})
					require.NoError(t, err)
					server, err := types.NewKubernetesServerV3FromCluster(kube, fmt.Sprintf("name-%v", i), fmt.Sprintf("name-%v", i))
					require.NoError(t, err)
					_, err = srv.Auth().UpsertKubernetesServer(ctx, server)
					require.NoError(t, err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.insertResources()

			// Fetch all resources
			fetchedResources := make([]types.ResourceWithLabels, 0, len(uniqueNames))
			resp, err := clt.ListResources(ctx, proto.ListResourcesRequest{
				ResourceType:   tc.kind,
				NeedTotalCount: true,
				Limit:          2,
				SortBy:         types.SortBy{Field: types.ResourceMetadataName, IsDesc: true},
			})
			require.NoError(t, err)
			require.Len(t, resp.Resources, 2)
			require.Equal(t, len(uniqueNames), resp.TotalCount)
			fetchedResources = append(fetchedResources, resp.Resources...)

			resp, err = clt.ListResources(ctx, proto.ListResourcesRequest{
				ResourceType:   tc.kind,
				NeedTotalCount: true,
				StartKey:       resp.NextKey,
				Limit:          2,
				SortBy:         types.SortBy{Field: types.ResourceMetadataName, IsDesc: true},
			})
			require.NoError(t, err)
			require.Len(t, resp.Resources, 1)
			require.Equal(t, len(uniqueNames), resp.TotalCount)
			fetchedResources = append(fetchedResources, resp.Resources...)

			r := types.ResourcesWithLabels(fetchedResources)
			var extractedErr error
			var extractedNames []string

			switch tc.kind {
			case types.KindDatabaseServer:
				s, err := r.AsDatabaseServers()
				require.NoError(t, err)
				extractedNames, extractedErr = types.DatabaseServers(s).GetFieldVals(types.ResourceMetadataName)

			case types.KindAppServer:
				s, err := r.AsAppServers()
				require.NoError(t, err)
				extractedNames, extractedErr = types.AppServers(s).GetFieldVals(types.ResourceMetadataName)

			case types.KindWindowsDesktop:
				s, err := r.AsWindowsDesktops()
				require.NoError(t, err)
				extractedNames, extractedErr = types.WindowsDesktops(s).GetFieldVals(types.ResourceMetadataName)

			default:
				s, err := r.AsKubeClusters()
				require.NoError(t, err)
				require.Len(t, s, 3)
				extractedNames, extractedErr = types.KubeClusters(s).GetFieldVals(types.ResourceMetadataName)
			}

			require.NoError(t, extractedErr)
			require.ElementsMatch(t, uniqueNames, extractedNames)
			require.IsDecreasing(t, extractedNames)
		})
	}
}

func TestListResources_WithRoles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)
	const nodePerPool = 3

	// inserts a pool nodes with different labels
	insertNodes := func(ctx context.Context, t *testing.T, srv *Server, nodeCount int, labels map[string]string) {
		for i := 0; i < nodeCount; i++ {
			name := uuid.NewString()
			addr := fmt.Sprintf("node-%s.example.com", name)

			node := &types.ServerV2{
				Kind:    types.KindNode,
				Version: types.V2,
				Metadata: types.Metadata{
					Name:      name,
					Namespace: defaults.Namespace,
					Labels:    labels,
				},
				Spec: types.ServerSpecV2{
					Addr: addr,
				},
			}

			_, err := srv.UpsertNode(ctx, node)
			require.NoError(t, err)
		}
	}

	// creates roles that deny the given labels
	createRole := func(ctx context.Context, t *testing.T, srv *Server, name string, labels map[string]apiutils.Strings) {
		role, err := types.NewRole(name, types.RoleSpecV6{
			Allow: types.RoleConditions{
				NodeLabels: types.Labels{
					"*": []string{types.Wildcard},
				},
			},
			Deny: types.RoleConditions{
				NodeLabels: labels,
			},
		})
		require.NoError(t, err)
		err = srv.UpsertRole(ctx, role)
		require.NoError(t, err)
	}

	// the pool from which nodes and roles are created from
	pool := map[string]struct {
		denied map[string]apiutils.Strings
		labels map[string]string
	}{
		"other": {
			denied: nil,
			labels: map[string]string{
				"other": "123",
			},
		},
		"a": {
			denied: map[string]apiutils.Strings{
				"pool": {"a"},
			},
			labels: map[string]string{
				"pool": "a",
			},
		},
		"b": {
			denied: map[string]apiutils.Strings{
				"pool": {"b"},
			},
			labels: map[string]string{
				"pool": "b",
			},
		},
		"c": {
			denied: map[string]apiutils.Strings{
				"pool": {"c"},
			},
			labels: map[string]string{
				"pool": "c",
			},
		},
		"d": {
			denied: map[string]apiutils.Strings{
				"pool": {"d"},
			},
			labels: map[string]string{
				"pool": "d",
			},
		},
		"e": {
			denied: map[string]apiutils.Strings{
				"pool": {"e"},
			},
			labels: map[string]string{
				"pool": "e",
			},
		},
	}

	// create the nodes and role
	for name, data := range pool {
		insertNodes(ctx, t, srv.Auth(), nodePerPool, data.labels)
		createRole(ctx, t, srv.Auth(), name, data.denied)
	}

	nodeCount := len(pool) * nodePerPool

	cases := []struct {
		name     string
		roles    []string
		expected int
	}{
		{
			name:     "all allowed",
			roles:    []string{"other"},
			expected: nodeCount,
		},
		{
			name:     "role a",
			roles:    []string{"a"},
			expected: nodeCount - nodePerPool,
		},
		{
			name:     "role a,b",
			roles:    []string{"a", "b"},
			expected: nodeCount - (2 * nodePerPool),
		},
		{
			name:     "role a,b,c",
			roles:    []string{"a", "b", "c"},
			expected: nodeCount - (3 * nodePerPool),
		},
		{
			name:     "role a,b,c,d",
			roles:    []string{"a", "b", "c", "d"},
			expected: nodeCount - (4 * nodePerPool),
		},
		{
			name:     "role a,b,c,d,e",
			roles:    []string{"a", "b", "c", "d", "e"},
			expected: nodeCount - (5 * nodePerPool),
		},
	}

	// ensure that a user can see the correct number of resources for their role(s)
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			user, err := types.NewUser(uuid.NewString())
			require.NoError(t, err)

			for _, role := range tt.roles {
				user.AddRole(role)
			}

			err = srv.Auth().UpsertUser(user)
			require.NoError(t, err)

			for _, needTotal := range []bool{true, false} {
				total := needTotal
				t.Run(fmt.Sprintf("needTotal=%t", total), func(t *testing.T) {
					t.Parallel()

					clt, err := srv.NewClient(TestUser(user.GetName()))
					require.NoError(t, err)

					var resp *types.ListResourcesResponse
					var nodes []types.ResourceWithLabels
					for {
						key := ""
						if resp != nil {
							key = resp.NextKey
						}

						resp, err = clt.ListResources(ctx, proto.ListResourcesRequest{
							ResourceType:   types.KindNode,
							StartKey:       key,
							Limit:          nodePerPool,
							NeedTotalCount: total,
						})
						require.NoError(t, err)

						nodes = append(nodes, resp.Resources...)

						if resp.NextKey == "" {
							break
						}
					}

					require.Len(t, nodes, tt.expected)
				})
			}
		})
	}
}

// TestGenerateHostCert attempts to generate host certificates using various
// RBAC rules
func TestGenerateHostCert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := newTestTLSServer(t)

	clusterName := srv.ClusterName()

	_, pub, err := testauthority.New().GenerateKeyPair()
	require.NoError(t, err)

	noError := func(err error) bool {
		return err == nil
	}

	for _, test := range []struct {
		desc       string
		principals []string
		skipRule   bool
		where      string
		deny       bool
		denyWhere  string
		expect     func(error) bool
	}{
		{
			desc:       "disallowed",
			skipRule:   true,
			principals: []string{"foo.example.com"},
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "denied",
			deny:       true,
			principals: []string{"foo.example.com"},
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "allowed",
			principals: []string{"foo.example.com"},
			expect:     noError,
		},
		{
			desc:       "allowed-subset",
			principals: []string{"foo.example.com"},
			where:      `is_subset(host_cert.principals, "foo.example.com", "bar.example.com")`,
			expect:     noError,
		},
		{
			desc:       "disallowed-subset",
			principals: []string{"baz.example.com"},
			where:      `is_subset(host_cert.principals, "foo.example.com", "bar.example.com")`,
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "allowed-all-equal",
			principals: []string{"foo.example.com"},
			where:      `all_equal(host_cert.principals, "foo.example.com")`,
			expect:     noError,
		},
		{
			desc:       "disallowed-all-equal",
			principals: []string{"bar.example.com"},
			where:      `all_equal(host_cert.principals, "foo.example.com")`,
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "allowed-all-end-with",
			principals: []string{"node.foo.example.com"},
			where:      `all_end_with(host_cert.principals, ".foo.example.com")`,
			expect:     noError,
		},
		{
			desc:       "disallowed-all-end-with",
			principals: []string{"node.bar.example.com"},
			where:      `all_end_with(host_cert.principals, ".foo.example.com")`,
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "allowed-complex",
			principals: []string{"foo.example.com"},
			where:      `all_end_with(host_cert.principals, ".example.com")`,
			denyWhere:  `is_subset(host_cert.principals, "bar.example.com", "baz.example.com")`,
			expect:     noError,
		},
		{
			desc:       "disallowed-complex",
			principals: []string{"bar.example.com"},
			where:      `all_end_with(host_cert.principals, ".example.com")`,
			denyWhere:  `is_subset(host_cert.principals, "bar.example.com", "baz.example.com")`,
			expect:     trace.IsAccessDenied,
		},
		{
			desc:       "allowed-multiple",
			principals: []string{"bar.example.com", "foo.example.com"},
			where:      `is_subset(host_cert.principals, "foo.example.com", "bar.example.com")`,
			expect:     noError,
		},
		{
			desc:       "disallowed-multiple",
			principals: []string{"foo.example.com", "bar.example.com", "baz.example.com"},
			where:      `is_subset(host_cert.principals, "foo.example.com", "bar.example.com")`,
			expect:     trace.IsAccessDenied,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			rules := []types.Rule{}
			if !test.skipRule {
				rules = append(rules, types.Rule{
					Resources: []string{types.KindHostCert},
					Verbs:     []string{types.VerbCreate},
					Where:     test.where,
				})
			}

			denyRules := []types.Rule{}
			if test.deny || test.denyWhere != "" {
				denyRules = append(denyRules, types.Rule{
					Resources: []string{types.KindHostCert},
					Verbs:     []string{types.VerbCreate},
					Where:     test.denyWhere,
				})
			}

			role, err := CreateRole(ctx, srv.Auth(), test.desc, types.RoleSpecV6{
				Allow: types.RoleConditions{Rules: rules},
				Deny:  types.RoleConditions{Rules: denyRules},
			})
			require.NoError(t, err)

			user, err := CreateUser(srv.Auth(), test.desc, role)
			require.NoError(t, err)

			client, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			_, err = client.GenerateHostCert(ctx, pub, "", "", test.principals, clusterName, types.RoleNode, 0)
			require.True(t, test.expect(err))
		})
	}
}

// TestLocalServiceRolesHavePermissionsForUploaderService verifies that all of Teleport's
// builtin roles have permissions to execute the calls required by the uploader service.
// This is because only one uploader service runs per Teleport process, and it will use
// the first available identity.
func TestLocalServiceRolesHavePermissionsForUploaderService(t *testing.T) {
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	// Test all local service roles, plus RoleInstance.
	// The latter may also be used to run the uploader.
	roles := append(types.LocalServiceMappings(), types.RoleInstance)
	for _, role := range roles {
		// RoleMDM services don't create events by themselves, instead they rely on
		// Auth to issue events.
		if role == types.RoleAuth || role == types.RoleMDM {
			continue
		}
		t.Run(role.String(), func(t *testing.T) {
			ctx := context.Background()

			var identity TestIdentity
			if role == types.RoleInstance {
				// RoleInstance needs AdditionalSystemRoles, otherwise the setup is the
				// same.
				identity = TestIdentity{
					I: authz.BuiltinRole{
						Role: role,
						AdditionalSystemRoles: []types.SystemRole{
							types.RoleNode, // Arbitrary, could be any role.
						},
						Username: string(role),
					},
				}
			} else {
				identity = TestBuiltin(role)
			}

			authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, identity.I))
			require.NoError(t, err)

			s := &ServerWithRoles{
				authServer: srv.AuthServer,
				alog:       srv.AuditLog,
				context:    *authContext,
			}

			t.Run("GetSessionTracker", func(t *testing.T) {
				sid := session.ID("foo/" + role.String())
				tracker, err := s.CreateSessionTracker(ctx, &types.SessionTrackerV1{
					ResourceHeader: types.ResourceHeader{
						Metadata: types.Metadata{
							Name: sid.String(),
						},
					},
					Spec: types.SessionTrackerSpecV1{
						SessionID: sid.String(),
					},
				})
				require.NoError(t, err)

				_, err = s.GetSessionTracker(ctx, tracker.GetSessionID())
				require.NoError(t, err)
			})

			t.Run("EmitAuditEvent", func(t *testing.T) {
				err := s.EmitAuditEvent(ctx, &apievents.UserLogin{
					Metadata: apievents.Metadata{
						Type: events.UserLoginEvent,
						Code: events.UserLocalLoginFailureCode,
					},
					Method: events.LoginMethodClientCert,
					Status: apievents.Status{Success: true},
				})
				require.NoError(t, err)
			})

			t.Run("StreamSessionEvents", func(t *testing.T) {
				// swap out the audit log with a discard log because we don't care if
				// the streaming actually succeeds, we just want to make sure RBAC checks
				// pass and allow us to enter the audit log code
				originalLog := s.alog
				t.Cleanup(func() { s.alog = originalLog })
				s.alog = events.NewDiscardAuditLog()

				eventC, errC := s.StreamSessionEvents(ctx, "foo", 0)
				select {
				case err := <-errC:
					require.NoError(t, err)
				default:
					// drain eventC to prevent goroutine leak
					for range eventC {
					}
				}
			})

			t.Run("CreateAuditStream", func(t *testing.T) {
				stream, err := s.CreateAuditStream(ctx, session.ID("streamer"))
				require.NoError(t, err)
				require.NoError(t, stream.Close(ctx))
			})

			t.Run("ResumeAuditStream", func(t *testing.T) {
				stream, err := s.ResumeAuditStream(ctx, session.ID("streamer"), "upload")
				require.NoError(t, err)
				require.NoError(t, stream.Close(ctx))
			})
		})
	}
}

type getActiveSessionsTestCase struct {
	name      string
	tracker   types.SessionTracker
	role      types.Role
	hasAccess bool
}

func TestGetActiveSessionTrackers(t *testing.T) {
	t.Parallel()

	testCases := []getActiveSessionsTestCase{func() getActiveSessionsTestCase {
		tracker, err := types.NewSessionTracker(types.SessionTrackerSpecV1{
			SessionID: "1",
			Kind:      string(types.SSHSessionKind),
		})
		require.NoError(t, err)

		role, err := types.NewRole("foo", types.RoleSpecV6{
			Allow: types.RoleConditions{
				Rules: []types.Rule{{
					Resources: []string{types.KindSessionTracker},
					Verbs:     []string{types.VerbList, types.VerbRead},
				}},
			},
		})
		require.NoError(t, err)

		return getActiveSessionsTestCase{"with access simple", tracker, role, true}
	}(), func() getActiveSessionsTestCase {
		tracker, err := types.NewSessionTracker(types.SessionTrackerSpecV1{
			SessionID: "1",
			Kind:      string(types.SSHSessionKind),
		})
		require.NoError(t, err)

		role, err := types.NewRole("foo", types.RoleSpecV6{})
		require.NoError(t, err)

		return getActiveSessionsTestCase{"with no access rule", tracker, role, false}
	}(), func() getActiveSessionsTestCase {
		tracker, err := types.NewSessionTracker(types.SessionTrackerSpecV1{
			SessionID: "1",
			Kind:      string(types.KubernetesSessionKind),
		})
		require.NoError(t, err)

		role, err := types.NewRole("foo", types.RoleSpecV6{
			Allow: types.RoleConditions{
				Rules: []types.Rule{{
					Resources: []string{types.KindSessionTracker},
					Verbs:     []string{types.VerbList, types.VerbRead},
					Where:     "equals(session_tracker.session_id, \"1\")",
				}},
			},
		})
		require.NoError(t, err)

		return getActiveSessionsTestCase{"access with match expression", tracker, role, true}
	}(), func() getActiveSessionsTestCase {
		tracker, err := types.NewSessionTracker(types.SessionTrackerSpecV1{
			SessionID: "2",
			Kind:      string(types.KubernetesSessionKind),
		})
		require.NoError(t, err)

		role, err := types.NewRole("foo", types.RoleSpecV6{
			Allow: types.RoleConditions{
				Rules: []types.Rule{{
					Resources: []string{types.KindSessionTracker},
					Verbs:     []string{types.VerbList, types.VerbRead},
					Where:     "equals(session_tracker.session_id, \"1\")",
				}},
			},
		})
		require.NoError(t, err)

		return getActiveSessionsTestCase{"no access with match expression", tracker, role, false}
	}()}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			srv := newTestTLSServer(t)
			err := srv.Auth().CreateRole(ctx, testCase.role)
			require.NoError(t, err)

			_, err = srv.Auth().CreateSessionTracker(ctx, testCase.tracker)
			require.NoError(t, err)

			user, err := types.NewUser(uuid.NewString())
			require.NoError(t, err)

			user.AddRole(testCase.role.GetName())
			err = srv.Auth().UpsertUser(user)
			require.NoError(t, err)

			clt, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			found, err := clt.GetActiveSessionTrackers(ctx)
			require.NoError(t, err)
			require.Equal(t, testCase.hasAccess, len(found) != 0)
		})
	}
}

func TestListReleasesPermissions(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	tt := []struct {
		Name         string
		Role         types.RoleSpecV6
		ErrAssertion require.BoolAssertionFunc
	}{
		{
			Name: "no permission error if user has allow rule to list downloads",
			Role: types.RoleSpecV6{
				Allow: types.RoleConditions{Rules: []types.Rule{{
					Resources: []string{types.KindDownload},
					Verbs:     []string{types.VerbList},
				}}},
			},
			ErrAssertion: require.False,
		},
		{
			Name: "permission error if user deny allow rule to list downloads",
			Role: types.RoleSpecV6{
				Deny: types.RoleConditions{Rules: []types.Rule{{
					Resources: []string{types.KindDownload},
					Verbs:     []string{types.VerbList},
				}}},
			},
			ErrAssertion: require.True,
		},
		{
			Name: "permission error if user has no rules regarding downloads",
			Role: types.RoleSpecV6{
				Allow: types.RoleConditions{Rules: []types.Rule{}},
			},
			ErrAssertion: require.True,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			role, err := CreateRole(ctx, srv.Auth(), "test-role", tc.Role)
			require.Nil(t, err)

			user, err := CreateUser(srv.Auth(), "test-user", role)
			require.NoError(t, err)

			client, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			_, err = client.ListReleases(ctx)
			tc.ErrAssertion(t, trace.IsAccessDenied(err))
		})
	}
}

func TestGetLicensePermissions(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	tt := []struct {
		Name         string
		Role         types.RoleSpecV6
		ErrAssertion require.BoolAssertionFunc
	}{
		{
			Name: "no permission error if user has allow rule to read license",
			Role: types.RoleSpecV6{
				Allow: types.RoleConditions{Rules: []types.Rule{{
					Resources: []string{types.KindLicense},
					Verbs:     []string{types.VerbRead},
				}}},
			},
			ErrAssertion: require.False,
		},
		{
			Name: "permission error if user deny allow rule to read license",
			Role: types.RoleSpecV6{
				Deny: types.RoleConditions{Rules: []types.Rule{{
					Resources: []string{types.KindLicense},
					Verbs:     []string{types.VerbRead},
				}}},
			},
			ErrAssertion: require.True,
		},
		{
			Name: "permission error if user has no rules regarding license",
			Role: types.RoleSpecV6{
				Allow: types.RoleConditions{Rules: []types.Rule{}},
			},
			ErrAssertion: require.True,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			role, err := CreateRole(ctx, srv.Auth(), "test-role", tc.Role)
			require.Nil(t, err)

			user, err := CreateUser(srv.Auth(), "test-user", role)
			require.NoError(t, err)

			client, err := srv.NewClient(TestUser(user.GetName()))
			require.NoError(t, err)

			_, err = client.GetLicense(ctx)
			tc.ErrAssertion(t, trace.IsAccessDenied(err))
		})
	}
}

func TestCreateSAMLIdPServiceProvider(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	user, noAccessUser := createSAMLIdPTestUsers(t, srv.Auth())

	tt := []struct {
		Name         string
		User         string
		SP           types.SAMLIdPServiceProvider
		EventCode    string
		ErrAssertion require.ErrorAssertionFunc
	}{
		{
			Name: "create service provider",
			User: user,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: newEntityDescriptor("IAMShowcase"),
					EntityID:         "IAMShowcase",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderCreateCode,
			ErrAssertion: require.NoError,
		},
		{
			Name: "fail creation",
			User: user,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: "non-null",
					EntityID:         "invalid",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderCreateFailureCode,
			ErrAssertion: require.Error,
		},
		{
			Name: "no permissions",
			User: noAccessUser,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test-new",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: newEntityDescriptor("no-permissions"),
					EntityID:         "no-permissions",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderCreateFailureCode,
			ErrAssertion: require.Error,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			client, err := srv.NewClient(TestUser(tc.User))
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, client.Close())
			})

			modifyAndWaitForEvent(t, tc.ErrAssertion, client, srv, tc.EventCode, func() error {
				return client.CreateSAMLIdPServiceProvider(ctx, tc.SP)
			})
		})
	}
}

func TestUpdateSAMLIdPServiceProvider(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	user, noAccessUser := createSAMLIdPTestUsers(t, srv.Auth())

	sp := &types.SAMLIdPServiceProviderV1{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name: "test",
			},
		},
		Spec: types.SAMLIdPServiceProviderSpecV1{
			EntityDescriptor: newEntityDescriptor("IAMShowcase"),
			EntityID:         "IAMShowcase",
		},
	}
	require.NoError(t, srv.Auth().CreateSAMLIdPServiceProvider(ctx, sp))

	tt := []struct {
		Name         string
		User         string
		SP           types.SAMLIdPServiceProvider
		EventCode    string
		ErrAssertion require.ErrorAssertionFunc
	}{
		{
			Name: "update service provider",
			User: user,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: newEntityDescriptor("new-entity-id"),
					EntityID:         "new-entity-id",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderUpdateCode,
			ErrAssertion: require.NoError,
		},
		{
			Name: "fail update",
			User: user,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: "non-null",
					EntityID:         "invalid",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderUpdateFailureCode,
			ErrAssertion: require.Error,
		},
		{
			Name: "no permissions",
			User: noAccessUser,
			SP: &types.SAMLIdPServiceProviderV1{
				ResourceHeader: types.ResourceHeader{
					Metadata: types.Metadata{
						Name: "test",
					},
				},
				Spec: types.SAMLIdPServiceProviderSpecV1{
					EntityDescriptor: "non-null",
					EntityID:         "invalid",
				},
			},
			EventCode:    events.SAMLIdPServiceProviderUpdateFailureCode,
			ErrAssertion: require.Error,
		},
	}

	for _, tc := range tt {
		t.Run(tc.Name, func(t *testing.T) {
			client, err := srv.NewClient(TestUser(tc.User))
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, client.Close())
			})

			modifyAndWaitForEvent(t, tc.ErrAssertion, client, srv, tc.EventCode, func() error {
				return client.UpdateSAMLIdPServiceProvider(ctx, tc.SP)
			})
		})
	}
}

func TestDeleteSAMLIdPServiceProvider(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	user, noAccessUser := createSAMLIdPTestUsers(t, srv.Auth())

	sp := &types.SAMLIdPServiceProviderV1{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name: "test",
			},
		},
		Spec: types.SAMLIdPServiceProviderSpecV1{
			EntityDescriptor: newEntityDescriptor("IAMShowcase"),
			EntityID:         "IAMShowcase",
		},
	}
	require.NoError(t, srv.Auth().CreateSAMLIdPServiceProvider(ctx, sp))

	// No permissions delete
	client, err := srv.NewClient(TestUser(noAccessUser))
	require.NoError(t, err)
	modifyAndWaitForEvent(t, require.Error, client, srv, events.SAMLIdPServiceProviderDeleteFailureCode, func() error {
		return client.DeleteSAMLIdPServiceProvider(ctx, sp.GetName())
	})

	// Successful delete
	client, err = srv.NewClient(TestUser(user))
	require.NoError(t, err)

	modifyAndWaitForEvent(t, require.NoError, client, srv, events.SAMLIdPServiceProviderDeleteCode, func() error {
		return client.DeleteSAMLIdPServiceProvider(ctx, sp.GetName())
	})

	require.NoError(t, client.CreateSAMLIdPServiceProvider(ctx, sp))

	// Non-existent delete
	modifyAndWaitForEvent(t, require.Error, client, srv, events.SAMLIdPServiceProviderDeleteFailureCode, func() error {
		return client.DeleteSAMLIdPServiceProvider(ctx, "nonexistent")
	})
}

func TestDeleteAllSAMLIdPServiceProviders(t *testing.T) {
	ctx := context.Background()
	srv := newTestTLSServer(t)

	user, noAccessUser := createSAMLIdPTestUsers(t, srv.Auth())

	sp1 := &types.SAMLIdPServiceProviderV1{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name: "test",
			},
		},
		Spec: types.SAMLIdPServiceProviderSpecV1{
			EntityDescriptor: newEntityDescriptor("ed1"),
			EntityID:         "ed1",
		},
	}
	require.NoError(t, srv.Auth().CreateSAMLIdPServiceProvider(ctx, sp1))

	sp2 := &types.SAMLIdPServiceProviderV1{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name: "test2",
			},
		},
		Spec: types.SAMLIdPServiceProviderSpecV1{
			EntityDescriptor: newEntityDescriptor("ed2"),
			EntityID:         "ed2",
		},
	}
	require.NoError(t, srv.Auth().CreateSAMLIdPServiceProvider(ctx, sp2))

	// Failed delete
	client, err := srv.NewClient(TestUser(noAccessUser))
	require.NoError(t, err)

	modifyAndWaitForEvent(t, require.Error, client, srv, events.SAMLIdPServiceProviderDeleteAllFailureCode, func() error {
		return client.DeleteAllSAMLIdPServiceProviders(ctx)
	})

	// Successful delete
	client, err = srv.NewClient(TestUser(user))
	require.NoError(t, err)

	modifyAndWaitForEvent(t, require.NoError, client, srv, events.SAMLIdPServiceProviderDeleteAllCode, func() error {
		return client.DeleteAllSAMLIdPServiceProviders(ctx)
	})
}

// Create the test users for SAML IdP service provider tests.
//
//nolint:revive // Because we want this to be IdP
func createSAMLIdPTestUsers(t *testing.T, server *Server) (string, string) {
	ctx := context.Background()

	role, err := CreateRole(ctx, server, "test-empty", types.RoleSpecV6{
		Allow: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindSAMLIdPServiceProvider},
					Verbs:     []string{types.VerbRead, types.VerbUpdate, types.VerbCreate, types.VerbDelete},
				},
			},
		},
	})
	require.NoError(t, err)

	user, err := CreateUser(server, "test-user", role)
	require.NoError(t, err)

	noAccessRole, err := CreateRole(ctx, server, "no-access-role", types.RoleSpecV6{
		Deny: types.RoleConditions{
			Rules: []types.Rule{
				{
					Resources: []string{types.KindSAMLIdPServiceProvider},
					Verbs:     []string{types.VerbRead, types.VerbCreate, types.VerbDelete},
				},
			},
		},
	})
	require.NoError(t, err)

	noAccessUser, err := CreateUser(server, "noaccess-user", noAccessRole)
	require.NoError(t, err)

	return user.GetName(), noAccessUser.GetName()
}

// modifyAndWaitForEvent performs the function fn() and then waits for the given event.
func modifyAndWaitForEvent(t *testing.T, errFn require.ErrorAssertionFunc, client *Client, srv *TestTLSServer, eventCode string, fn func() error) apievents.AuditEvent {
	// Make sure we ignore events after consuming this one.
	defer func() {
		srv.AuthServer.AuthServer.emitter = events.NewDiscardEmitter()
	}()
	chanEmitter := eventstest.NewChannelEmitter(1)
	srv.AuthServer.AuthServer.emitter = chanEmitter
	err := fn()
	errFn(t, err)
	select {
	case event := <-chanEmitter.C():
		require.Equal(t, eventCode, event.GetCode())
		return event
	case <-time.After(5 * time.Second):
		require.Fail(t, "timeout waiting for update event")
	}
	return nil
}

func TestUnimplementedClients(t *testing.T) {
	ctx := context.Background()
	testAuth, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	server := &ServerWithRoles{
		authServer: testAuth.AuthServer,
	}

	require.NoError(t, err)

	t.Run("DevicesClient", func(t *testing.T) {
		_, err := server.DevicesClient().ListDevices(ctx, nil)
		require.Error(t, err)
		require.True(t, trace.IsNotImplemented(err), err)
	})

	t.Run("LoginRuleClient", func(t *testing.T) {
		_, err := server.LoginRuleClient().ListLoginRules(ctx, nil)
		require.Error(t, err)
		require.True(t, trace.IsNotImplemented(err), err)
	})

	t.Run("PluginClient", func(t *testing.T) {
		_, err := server.PluginsClient().ListPlugins(ctx, nil)
		require.Error(t, err)
		require.True(t, trace.IsNotImplemented(err), err)
	})

	t.Run("SAMLIdPClient", func(t *testing.T) {
		_, err := server.SAMLIdPClient().ProcessSAMLIdPRequest(ctx, nil)
		require.Error(t, err)
		require.True(t, trace.IsNotImplemented(err), err)
	})
}

// getTestHeadlessAuthenticationID returns the headless authentication resource
// used across headless authentication tests.
func getTestHeadlessAuthn(t *testing.T, user string) *types.HeadlessAuthentication {
	headlessID := services.NewHeadlessAuthenticationID([]byte(sshPubKey))
	headlessAuthn := &types.HeadlessAuthentication{
		ResourceHeader: types.ResourceHeader{
			Metadata: types.Metadata{
				Name: headlessID,
			},
		},
		User:            user,
		PublicKey:       []byte(sshPubKey),
		ClientIpAddress: "0.0.0.0",
	}
	headlessAuthn.SetExpiry(time.Now().Add(time.Minute))

	err := headlessAuthn.CheckAndSetDefaults()
	require.NoError(t, err)

	return headlessAuthn
}

func TestGetHeadlessAuthentication(t *testing.T) {
	ctx := context.Background()
	username := "teleport-user"
	otherUsername := "other-user"

	for _, tc := range []struct {
		name                  string
		headlessID            string
		identity              TestIdentity
		assertError           require.ErrorAssertionFunc
		expectedHeadlessAuthn *types.HeadlessAuthentication
	}{
		{
			name:        "OK same user",
			identity:    TestUser(username),
			assertError: require.NoError,
		}, {
			name:       "NOK not found",
			headlessID: uuid.NewString(),
			identity:   TestUser(username),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK different user",
			identity: TestUser(otherUsername),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK admin",
			identity: TestAdmin(),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			t.Parallel()

			srv := newTestTLSServer(t)
			_, _, err := CreateUserAndRole(srv.Auth(), username, nil, nil)
			require.NoError(t, err)
			_, _, err = CreateUserAndRole(srv.Auth(), otherUsername, nil, nil)
			require.NoError(t, err)

			// create headless authn
			headlessAuthn := getTestHeadlessAuthn(t, username)
			stub, err := srv.Auth().CreateHeadlessAuthenticationStub(ctx, headlessAuthn.GetName())
			require.NoError(t, err)
			_, err = srv.Auth().CompareAndSwapHeadlessAuthentication(ctx, stub, headlessAuthn)
			require.NoError(t, err)

			client, err := srv.NewClient(tc.identity)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()

			// default to same headlessAuthn
			if tc.headlessID == "" {
				tc.headlessID = headlessAuthn.GetName()
			}

			retrievedHeadlessAuthn, err := client.GetHeadlessAuthentication(ctx, tc.headlessID)
			tc.assertError(t, err)
			if err == nil {
				require.Equal(t, headlessAuthn, retrievedHeadlessAuthn)
			}
		})
	}
}

func TestUpdateHeadlessAuthenticationState(t *testing.T) {
	ctx := context.Background()
	otherUsername := "other-user"

	for _, tc := range []struct {
		name string
		// defaults to the mfa identity tied to the headless authentication created
		identity TestIdentity
		// defaults to id of the headless authentication created
		headlessID  string
		state       types.HeadlessAuthenticationState
		withMFA     bool
		assertError require.ErrorAssertionFunc
	}{
		{
			name:        "OK same user denied",
			state:       types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_DENIED,
			assertError: require.NoError,
		}, {
			name:        "OK same user approved with mfa",
			state:       types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED,
			withMFA:     true,
			assertError: require.NoError,
		}, {
			name:    "NOK same user approved without mfa",
			state:   types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED,
			withMFA: false,
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.True(t, trace.IsAccessDenied(err), "expected access denied error but got: %v", err)
			},
		}, {
			name:       "NOK not found",
			headlessID: uuid.NewString(),
			state:      types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_DENIED,
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK different user denied",
			state:    types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_DENIED,
			identity: TestUser(otherUsername),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK different user approved",
			state:    types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED,
			identity: TestUser(otherUsername),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK admin denied",
			state:    types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_DENIED,
			identity: TestAdmin(),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		}, {
			name:     "NOK admin approved",
			state:    types.HeadlessAuthenticationState_HEADLESS_AUTHENTICATION_STATE_APPROVED,
			identity: TestAdmin(),
			assertError: func(t require.TestingT, err error, i ...interface{}) {
				require.Error(t, err)
				require.ErrorContains(t, err, context.DeadlineExceeded.Error(), "expected context deadline error but got: %v", err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			t.Parallel()

			srv := newTestTLSServer(t)
			mfa := configureForMFA(t, srv)

			_, _, err := CreateUserAndRole(srv.Auth(), otherUsername, nil, nil)
			require.NoError(t, err)

			// create headless authn
			headlessAuthn := getTestHeadlessAuthn(t, mfa.User)
			stub, err := srv.Auth().CreateHeadlessAuthenticationStub(ctx, headlessAuthn.GetName())
			require.NoError(t, err)
			_, err = srv.Auth().CompareAndSwapHeadlessAuthentication(ctx, stub, headlessAuthn)
			require.NoError(t, err)

			// default to mfa user
			if tc.identity.I == nil {
				tc.identity = TestUser(mfa.User)
			}

			client, err := srv.NewClient(tc.identity)
			require.NoError(t, err)

			resp := &proto.MFAAuthenticateResponse{
				Response: &proto.MFAAuthenticateResponse_Webauthn{
					Webauthn: &webauthn.CredentialAssertionResponse{
						Type: "bad response",
					},
				},
			}
			if tc.withMFA {
				client, err := srv.NewClient(TestUser(mfa.User))
				require.NoError(t, err)

				challenge, err := client.CreateAuthenticateChallenge(ctx, &proto.CreateAuthenticateChallengeRequest{
					Request: &proto.CreateAuthenticateChallengeRequest_ContextUser{},
				})
				require.NoError(t, err)

				resp, err = mfa.WebDev.SolveAuthn(challenge)
				require.NoError(t, err)
			}

			// default to same headlessAuthn
			if tc.headlessID == "" {
				tc.headlessID = headlessAuthn.GetName()
			}

			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()

			err = client.UpdateHeadlessAuthenticationState(ctx, tc.headlessID, tc.state, resp)
			tc.assertError(t, err)
		})
	}
}

func TestGenerateCertAuthorityCRL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv, err := NewTestAuthServer(TestAuthServerConfig{Dir: t.TempDir()})
	require.NoError(t, err)

	// Server used to create users and roles.
	setupAuthContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, TestAdmin().I))
	require.NoError(t, err)
	setupServer := &ServerWithRoles{
		authServer: srv.AuthServer,
		alog:       srv.AuditLog,
		context:    *setupAuthContext,
	}

	// Create a test user.
	_, err = CreateUser(setupServer, "username")
	require.NoError(t, err)

	for _, tc := range []struct {
		desc      string
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		{
			desc:      "AdminRole",
			identity:  TestAdmin(),
			assertErr: require.NoError,
		},
		{
			desc:      "User",
			identity:  TestUser("username"),
			assertErr: require.NoError,
		},
		{
			desc:      "WindowsDesktopService",
			identity:  TestBuiltin(types.RoleWindowsDesktop),
			assertErr: require.NoError,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			authContext, err := srv.Authorizer.Authorize(authz.ContextWithUser(ctx, tc.identity.I))
			require.NoError(t, err)

			s := &ServerWithRoles{
				authServer: srv.AuthServer,
				alog:       srv.AuditLog,
				context:    *authContext,
			}

			_, err = s.GenerateCertAuthorityCRL(ctx, types.UserCA)
			tc.assertErr(t, err)
		})
	}
}

func TestCreateSnowflakeSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as db service": {
			identity:  TestBuiltin(types.RoleDatabase),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}
	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			_, err = client.CreateSnowflakeSession(ctx, types.CreateSnowflakeSessionRequest{
				Username:     alice,
				TokenTTL:     time.Minute * 15,
				SessionToken: "test-token-123",
			})
			test.assertErr(t, err)
		})
	}
}

func TestGetSnowflakeSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())
	dbClient, err := srv.NewClient(TestBuiltin(types.RoleDatabase))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// setup a session to get, for user "alice".
	sess, err := dbClient.CreateSnowflakeSession(ctx, types.CreateSnowflakeSessionRequest{
		Username:     alice,
		TokenTTL:     time.Minute * 15,
		SessionToken: "abc123",
	})
	require.NoError(t, err)

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as db service": {
			identity:  TestBuiltin(types.RoleDatabase),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			_, err = client.GetSnowflakeSession(ctx, types.GetSnowflakeSessionRequest{
				SessionID: sess.GetName(),
			})
			test.assertErr(t, err)
		})
	}
}

func TestGetSnowflakeSessions(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, _, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as db service": {
			identity:  TestBuiltin(types.RoleDatabase),
			assertErr: require.NoError,
		},
		"as user": {
			identity:  TestUser(alice),
			assertErr: require.Error,
		},
		"as admin": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			_, err = client.GetSnowflakeSessions(ctx)
			test.assertErr(t, err)
		})
	}
}

func TestDeleteSnowflakeSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())
	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as db service": {
			identity:  TestBuiltin(types.RoleDatabase),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	dbClient, err := srv.NewClient(TestBuiltin(types.RoleDatabase))
	require.NoError(t, err)
	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			sess, err := dbClient.CreateSnowflakeSession(ctx, types.CreateSnowflakeSessionRequest{
				Username:     alice,
				TokenTTL:     time.Minute * 15,
				SessionToken: "abc123",
			})
			require.NoError(t, err)
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			err = client.DeleteSnowflakeSession(ctx, types.DeleteSnowflakeSessionRequest{
				SessionID: sess.GetName(),
			})
			test.assertErr(t, err)
		})
	}
}

func TestDeleteAllSnowflakeSessions(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, _, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as db service": {
			identity:  TestBuiltin(types.RoleDatabase),
			assertErr: require.NoError,
		},
		"as user": {
			identity:  TestUser(alice),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			err = client.DeleteAllSnowflakeSessions(ctx)
			test.assertErr(t, err)
		})
	}
}

func TestCreateSAMLIdPSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as proxy user": {
			identity:  TestBuiltin(types.RoleProxy),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}
	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			_, err = client.CreateSAMLIdPSession(ctx, types.CreateSAMLIdPSessionRequest{
				SessionID:   "test",
				Username:    alice,
				SAMLSession: &types.SAMLSessionData{},
			})
			test.assertErr(t, err)
		})
	}
}

func TestGetSAMLIdPSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// setup a session to get, for user "alice".
	aliceClient, err := srv.NewClient(TestUser(alice))
	require.NoError(t, err)

	sess, err := aliceClient.CreateSAMLIdPSession(ctx, types.CreateSAMLIdPSessionRequest{
		SessionID:   "test",
		Username:    alice,
		SAMLSession: &types.SAMLSessionData{},
	})
	require.NoError(t, err)

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as proxy service": {
			identity:  TestBuiltin(types.RoleProxy),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			_, err = client.GetSAMLIdPSession(ctx, types.GetSAMLIdPSessionRequest{
				SessionID: sess.GetName(),
			})
			test.assertErr(t, err)
		})
	}
}

func TestListSAMLIdPSessions(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, _, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as proxy service": {
			identity:  TestBuiltin(types.RoleProxy),
			assertErr: require.NoError,
		},
		"as user": {
			identity:  TestUser(alice),
			assertErr: require.Error,
		},
		"as admin": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			_, _, err = client.ListSAMLIdPSessions(ctx, 0, "", "")
			test.assertErr(t, err)
		})
	}
}

func TestDeleteSAMLIdPSession(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, bob, admin := createSessionTestUsers(t, srv.Auth())
	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as proxy service": {
			identity:  TestBuiltin(types.RoleProxy),
			assertErr: require.NoError,
		},
		"as session user": {
			identity:  TestUser(alice),
			assertErr: require.NoError,
		},
		"as other user": {
			identity:  TestUser(bob),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	aliceClient, err := srv.NewClient(TestUser(alice))
	require.NoError(t, err)
	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sess, err := aliceClient.CreateSAMLIdPSession(ctx, types.CreateSAMLIdPSessionRequest{
				SessionID:   uuid.NewString(),
				Username:    alice,
				SAMLSession: &types.SAMLSessionData{},
			})
			require.NoError(t, err)
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			err = client.DeleteSAMLIdPSession(ctx, types.DeleteSAMLIdPSessionRequest{
				SessionID: sess.GetName(),
			})
			test.assertErr(t, err)
		})
	}
}

func TestDeleteAllSAMLIdPSessions(t *testing.T) {
	t.Parallel()
	srv := newTestTLSServer(t)
	alice, _, admin := createSessionTestUsers(t, srv.Auth())

	tests := map[string]struct {
		identity  TestIdentity
		assertErr require.ErrorAssertionFunc
	}{
		"as proxy service": {
			identity:  TestBuiltin(types.RoleProxy),
			assertErr: require.NoError,
		},
		"as user": {
			identity:  TestUser(alice),
			assertErr: require.Error,
		},
		"as admin user": {
			identity:  TestUser(admin),
			assertErr: require.NoError,
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, err := srv.NewClient(test.identity)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			err = client.DeleteAllSAMLIdPSessions(ctx)
			test.assertErr(t, err)
		})
	}
}

// Create test users for web session CRUD authz tests.
func createSessionTestUsers(t *testing.T, authServer *Server) (string, string, string) {
	t.Helper()
	// create alice and bob who have no permissions.
	_, _, err := CreateUserAndRole(authServer, "alice", nil, []types.Rule{})
	require.NoError(t, err)
	_, _, err = CreateUserAndRole(authServer, "bob", nil, []types.Rule{})
	require.NoError(t, err)
	// create "admin" who has read/write on users and web sessions.
	_, _, err = CreateUserAndRole(authServer, "admin", nil, []types.Rule{
		types.NewRule(types.KindUser, services.RW()),
		types.NewRule(types.KindWebSession, services.RW()),
	})
	require.NoError(t, err)
	return "alice", "bob", "admin"
}

func TestCheckInventorySupportsRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	emptyRole, err := types.NewRole("empty", types.RoleSpecV6{})
	require.NoError(t, err)

	roleWithLabelExpressions, err := types.NewRole("expressions", types.RoleSpecV6{
		Allow: types.RoleConditions{
			NodeLabelsExpression: `contains(user.spec.traits["allow-env"], labels["env"])`,
		},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		desc                   string
		role                   types.Role
		authVersion            string
		inventoryVersions      []string
		assertErr              require.ErrorAssertionFunc
		expectNoInventoryCheck bool
	}{
		{
			desc:                   "basic",
			role:                   emptyRole,
			authVersion:            api.Version,
			inventoryVersions:      []string{"12.1.2", "13.0.0", api.Version},
			assertErr:              require.NoError,
			expectNoInventoryCheck: true,
		},
		{
			desc:              "label expressions supported",
			role:              roleWithLabelExpressions,
			authVersion:       "14.0.0-dev",
			inventoryVersions: []string{minSupportedLabelExpressionVersion.String(), "13.2.3"},
			assertErr:         require.NoError,
		},
		{
			desc:              "unparseable server version doesn't break UpsertRole",
			role:              roleWithLabelExpressions,
			authVersion:       "14.0.0-dev",
			inventoryVersions: []string{"Not a version"},
			assertErr:         require.NoError,
		},
		{
			desc:              "block upsert with unsupported nodes in v13",
			role:              roleWithLabelExpressions,
			authVersion:       "13.2.3",
			inventoryVersions: []string{minSupportedLabelExpressionVersion.String(), "13.0.0-unsupported", "13.2.3"},
			assertErr: func(t require.TestingT, err error, args ...any) {
				require.Error(t, err)
				require.True(t, trace.IsBadParameter(err), "expected bad parameter error, got %v", err)
				require.ErrorContains(t, err, "does not support the label expressions used in this role")
			},
		},
		{
			desc:              "block upsert with unsupported nodes in v14",
			role:              roleWithLabelExpressions,
			authVersion:       "14.1.2",
			inventoryVersions: []string{minSupportedLabelExpressionVersion.String(), "13.0.0-unsupported", "13.2.3"},
			assertErr: func(t require.TestingT, err error, args ...any) {
				require.Error(t, err)
				require.True(t, trace.IsBadParameter(err), "expected bad parameter error, got %v", err)
				require.ErrorContains(t, err, "does not support the label expressions used in this role")
			},
		},
		{
			desc:                   "skip inventory check in 15",
			role:                   roleWithLabelExpressions,
			authVersion:            "15.0.0-dev",
			inventoryVersions:      []string{"14.0.0", "15.0.0"},
			assertErr:              require.NoError,
			expectNoInventoryCheck: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			getInventory := func(context.Context, proto.InventoryStatusRequest) proto.InventoryStatusSummary {
				if tc.expectNoInventoryCheck {
					require.Fail(t, "getInventory called when the inventory check should have been skipped")
				}

				hellos := make([]proto.UpstreamInventoryHello, 0, len(tc.inventoryVersions))
				for _, v := range tc.inventoryVersions {
					hellos = append(hellos, proto.UpstreamInventoryHello{
						Version: v,
					})
				}
				return proto.InventoryStatusSummary{
					Connected: hellos,
				}
			}

			err := checkInventorySupportsRole(ctx, tc.role, tc.authVersion, getInventory)
			tc.assertErr(t, err)
		})
	}
}

func TestSafeToSkipInventoryCheck(t *testing.T) {
	for _, tc := range []struct {
		desc               string
		authVersion        string
		minRequiredVersion string
		safeToSkip         bool
	}{
		{
			desc:               "auth two majors ahead of required minor release",
			authVersion:        "15.0.0",
			minRequiredVersion: "13.1.0",
			safeToSkip:         true,
		},
		{
			desc:               "auth within one major of required minor release",
			authVersion:        "14.0.0",
			minRequiredVersion: "13.1.0",
			safeToSkip:         false,
		},
		{
			desc:               "auth one major greater than required major release",
			authVersion:        "14.0.0",
			minRequiredVersion: "13.0.0",
			safeToSkip:         true, // anything older than 13.0.0 is >1 major behind 14.0.0
		},
		{
			desc:               "auth within one major of required major release",
			authVersion:        "13.3.12",
			minRequiredVersion: "13.0.0",
			safeToSkip:         false,
		},
		{
			desc:               "matching releases",
			authVersion:        "13.2.0",
			minRequiredVersion: "13.2.0",
			safeToSkip:         false,
		},
		{
			desc:               "skip for zero version",
			authVersion:        "13.2.0",
			minRequiredVersion: "0.0.0",
			safeToSkip:         true,
		},
	} {
		require.Equal(t, tc.safeToSkip,
			safeToSkipInventoryCheck(*semver.New(tc.authVersion), *semver.New(tc.minRequiredVersion)))
	}
}
