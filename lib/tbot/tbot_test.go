/*
Copyright 2022 Gravitational, Inc.

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

package tbot

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/gravitational/teleport/api/types"
	apiutils "github.com/gravitational/teleport/api/utils"
	"github.com/gravitational/teleport/api/utils/sshutils"
	"github.com/gravitational/teleport/lib/auth/native"
	libconfig "github.com/gravitational/teleport/lib/config"
	apisshutils "github.com/gravitational/teleport/lib/sshutils"
	"github.com/gravitational/teleport/lib/tbot/bot"
	"github.com/gravitational/teleport/lib/tbot/config"
	"github.com/gravitational/teleport/lib/tbot/identity"
	"github.com/gravitational/teleport/lib/tbot/testhelpers"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
)

func TestMain(m *testing.M) {
	utils.InitLoggerForTests()
	native.PrecomputeTestKeys(m)
	os.Exit(m.Run())
}

// TestBot is a one-shot run of the bot that communicates with a stood up
// in memory auth server.
//
// This test suite should ensure that outputs result in credentials with the
// expected attributes. The exact format of rendered templates is a concern
// that should be tested at a lower level. Generally assume that the auth server
// has good behaviour (e.g is enforcing rbac correctly) and avoid testing cases
// such as the bot not having a role granting access to a resource.
func TestBot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := utils.NewLoggerForTests()

	// Make a new auth server.
	fc, fds := testhelpers.DefaultConfig(t)
	const appName = "test-app"
	fc.Apps = libconfig.Apps{
		Service: libconfig.Service{
			EnabledFlag: "true",
		},
		Apps: []*libconfig.App{
			{
				Name:       appName,
				PublicAddr: "test-app.example.com",
				URI:        "http://test-app.example.com:1234",
			},
		},
	}
	const (
		databaseServiceName = "test-database-service"
		databaseUsername    = "test-database-username"
		databaseName        = "test-database"
	)
	fc.Databases = libconfig.Databases{
		Service: libconfig.Service{
			EnabledFlag: "true",
		},
		Databases: []*libconfig.Database{
			{
				Name:     databaseServiceName,
				Protocol: "mysql",
				URI:      "example.com:1234",
			},
		},
	}

	clusterName := string(fc.Auth.ClusterName)
	_ = testhelpers.MakeAndRunTestAuthServer(t, log, fc, fds)
	rootClient := testhelpers.MakeDefaultAuthClient(t, log, fc)

	// Wait for the app/db to become available. Sometimes this takes a bit
	// of time in CI.
	require.Eventually(t, func() bool {
		_, err := getApp(ctx, rootClient, appName)
		if err != nil {
			return false
		}
		_, err = getDatabase(ctx, rootClient, databaseServiceName)
		return err == nil
	}, 15*time.Second, 100*time.Millisecond)

	// Register a "fake" Kubernetes cluster so tbot can request certs for it.
	kubeClusterName := "test-kube-cluster"
	kubeCluster, err := types.NewKubernetesClusterV3(
		types.Metadata{
			Name: kubeClusterName,
		},
		types.KubernetesClusterSpecV3{},
	)
	require.NoError(t, err)
	kubeServer, err := types.NewKubernetesServerV3FromCluster(kubeCluster, "test-host", "uuid")
	require.NoError(t, err)
	_, err = rootClient.UpsertKubernetesServer(ctx, kubeServer)
	require.NoError(t, err)

	// Fetch CAs from auth server to compare to artifacts later
	hostCA, err := rootClient.GetCertAuthority(ctx, types.CertAuthID{
		Type:       types.HostCA,
		DomainName: clusterName,
	}, false)
	require.NoError(t, err)
	userCA, err := rootClient.GetCertAuthority(ctx, types.CertAuthID{
		Type:       types.UserCA,
		DomainName: clusterName,
	}, false)
	require.NoError(t, err)

	const (
		roleName      = "output-role"
		hostPrincipal = "node.example.com"
	)
	hostCertRule := types.NewRule("host_cert", []string{"create"})
	hostCertRule.Where = fmt.Sprintf("is_subset(host_cert.principals, \"%s\")", hostPrincipal)
	role, err := types.NewRole(roleName, types.RoleSpecV6{
		Allow: types.RoleConditions{
			// Grant access to all apps
			AppLabels: types.Labels{
				"*": apiutils.Strings{"*"},
			},

			// Grant access to all kubernetes clusters
			KubernetesLabels: types.Labels{
				"*": apiutils.Strings{"*"},
			},
			KubeGroups: []string{"system:masters"},

			// Grant access to database
			// Note: we don't actually need a role granting us database access to
			// request it. Actual access is validated via RBAC at connection time.
			// We do need an actual database and permission to list them, however.
			DatabaseLabels: types.Labels{
				"*": apiutils.Strings{"*"},
			},
			DatabaseNames: []string{databaseName},
			DatabaseUsers: []string{databaseUsername},
			Rules: []types.Rule{
				types.NewRule("db_server", []string{"read", "list"}),
				// Grant ability to generate a host cert
				hostCertRule,
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, rootClient.UpsertRole(ctx, role))

	// Make and join a new bot instance.
	botParams := testhelpers.MakeBot(t, rootClient, "test", roleName)

	identityOutput := &config.IdentityOutput{
		Common: config.OutputCommon{
			Destination: config.WrapDestination(&config.DestinationMemory{}),
		},
	}
	appOutput := &config.ApplicationOutput{
		Common: config.OutputCommon{
			Destination: config.WrapDestination(&config.DestinationMemory{}),
		},
		AppName: appName,
	}
	dbOutput := &config.DatabaseOutput{
		Common: config.OutputCommon{
			Destination: config.WrapDestination(&config.DestinationMemory{}),
		},
		Service:  databaseServiceName,
		Database: databaseName,
		Username: databaseUsername,
	}
	kubeOutput := &config.KubernetesOutput{
		Common: config.OutputCommon{
			// DestinationDirectory required or output will fail.
			Destination: config.WrapDestination(&config.DestinationDirectory{
				Path: t.TempDir(),
			}),
		},
		ClusterName: kubeClusterName,
	}
	sshHostOutput := &config.SSHHostOutput{
		Common: config.OutputCommon{
			Destination: config.WrapDestination(&config.DestinationMemory{}),
		},
		Principals: []string{hostPrincipal},
	}
	botConfig := testhelpers.MakeMemoryBotConfig(
		t, fc, botParams, []config.Output{
			identityOutput,
			appOutput,
			dbOutput,
			sshHostOutput,
			kubeOutput,
		},
	)
	b := New(botConfig, log)
	require.NoError(t, b.Run(ctx))

	t.Run("validate bot identity", func(t *testing.T) {
		// Some rough checks to ensure the bot identity used follows our
		// expected rules for bot identities.
		botIdent := b.ident()
		tlsIdent, err := tlsca.FromSubject(botIdent.X509Cert.Subject, botIdent.X509Cert.NotAfter)
		require.NoError(t, err)
		require.True(t, tlsIdent.Renewable)
		require.False(t, tlsIdent.DisallowReissue)
		require.Equal(t, uint64(1), tlsIdent.Generation)
		require.ElementsMatch(t, []string{botParams.RoleName}, tlsIdent.Groups)
	})

	t.Run("output: identity", func(t *testing.T) {
		_ = tlsIdentFromDest(t, identityOutput.GetDestination())
	})

	t.Run("output: kubernetes", func(t *testing.T) {
		_ = tlsIdentFromDest(t, kubeOutput.GetDestination())
	})

	t.Run("output: application", func(t *testing.T) {
		route := tlsIdentFromDest(t, appOutput.GetDestination()).RouteToApp
		require.Equal(t, appName, route.Name)
		require.Equal(t, "test-app.example.com", route.PublicAddr)
		require.NotEmpty(t, route.SessionID)
	})

	t.Run("output: database", func(t *testing.T) {
		route := tlsIdentFromDest(t, dbOutput.GetDestination()).RouteToDatabase
		require.Equal(t, databaseServiceName, route.ServiceName)
		require.Equal(t, databaseName, route.Database)
		require.Equal(t, databaseUsername, route.Username)
		require.Equal(t, "mysql", route.Protocol)
	})

	t.Run("output: ssh_host", func(t *testing.T) {
		dest := sshHostOutput.GetDestination()

		// Validate ssh_host
		hostKeyBytes, err := dest.Read("ssh_host")
		require.NoError(t, err)
		hostKey, err := ssh.ParsePrivateKey(hostKeyBytes)
		require.NoError(t, err)
		testData := []byte("test-data")
		signedTestData, err := hostKey.Sign(rand.Reader, testData)
		require.NoError(t, err)

		// Validate ssh_host-cert.pub
		hostCertBytes, err := dest.Read("ssh_host-cert.pub")
		require.NoError(t, err)
		hostCert, err := sshutils.ParseCertificate(hostCertBytes)
		require.NoError(t, err)

		// Check cert is signed by host CA, and that the host key can sign things
		// which can be verified with the host cert.
		publicKeys, err := apisshutils.GetCheckers(hostCA)
		hostCertChecker := ssh.CertChecker{
			IsHostAuthority: func(v ssh.PublicKey, _ string) bool {
				for _, pk := range publicKeys {
					return bytes.Equal(v.Marshal(), pk.Marshal())
				}
				return false
			},
		}
		require.NoError(t, hostCertChecker.CheckCert(hostPrincipal, hostCert), "host cert does not pass verification")
		require.NoError(t, hostCert.Key.Verify(testData, signedTestData), "signature by host key does not verify with public key in host certificate")

		// Validate ssh_host-user-ca.pub
		userCABytes, err := dest.Read("ssh_host-user-ca.pub")
		require.NoError(t, err)
		userCAKey, _, _, _, err := ssh.ParseAuthorizedKey(userCABytes)
		require.NoError(t, err)
		matchesUserCA := false
		for _, trustedKeyPair := range userCA.GetTrustedSSHKeyPairs() {
			wantUserCAKey, _, _, _, err := ssh.ParseAuthorizedKey(trustedKeyPair.PublicKey)
			require.NoError(t, err)
			if bytes.Equal(userCAKey.Marshal(), wantUserCAKey.Marshal()) {
				matchesUserCA = true
				break
			}
		}
		require.True(t, matchesUserCA)
	})
}

func tlsIdentFromDest(t *testing.T, dest bot.Destination) *tlsca.Identity {
	t.Helper()
	keyBytes, err := dest.Read(identity.PrivateKeyKey)
	require.NoError(t, err)
	certBytes, err := dest.Read(identity.TLSCertKey)
	require.NoError(t, err)
	hostCABytes, err := dest.Read(config.HostCAPath)
	require.NoError(t, err)
	ident := &identity.Identity{}
	err = identity.ReadTLSIdentityFromKeyPair(ident, keyBytes, certBytes, [][]byte{hostCABytes})
	require.NoError(t, err)

	tlsIdent, err := tlsca.FromSubject(
		ident.X509Cert.Subject, ident.X509Cert.NotAfter,
	)
	require.NoError(t, err)
	return tlsIdent
}
