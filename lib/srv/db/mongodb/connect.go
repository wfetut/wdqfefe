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

package mongodb

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv/db/common"
	"github.com/gravitational/teleport/lib/utils"

	"go.mongodb.org/mongo-driver/mongo/address"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/auth"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
	"go.mongodb.org/mongo-driver/x/mongo/driver/ocsp"
	"go.mongodb.org/mongo-driver/x/mongo/driver/topology"

	"github.com/gravitational/trace"
)

// connect returns connection to a MongoDB server.
//
// When connecting to a replica set, returns connection to the server selected
// based on the read preference connection string option. This allows users to
// configure database access to always connect to a secondary for example.
func (e *Engine) connect(ctx context.Context, sessionCtx *common.Session) (driver.Connection, func(), error) {
	options, selector, err := e.getTopologyOptions(ctx, sessionCtx)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	// Using driver's "topology" package allows to retain low-level control
	// over server connections (reading/writing wire messages) but at the
	// same time get access to logic such as picking a server to connect to
	// in a replica set.
	top, err := topology.New(options...)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	err = top.Connect()
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	server, err := top.SelectServer(ctx, selector)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	e.Log.Debugf("Cluster topology: %v, selected server %v.", top, server)
	conn, err := server.Connection(ctx)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}

	closeFn := func() {
		if err := top.Disconnect(ctx); err != nil {
			e.Log.WithError(err).Warn("Failed to close topology")
		}
		if err := conn.Close(); err != nil {
			e.Log.WithError(err).Error("Failed to close server connection.")
		}
	}
	return conn, closeFn, nil
}

// getTopologyOptions constructs topology options for connecting to a MongoDB server.
func (e *Engine) getTopologyOptions(ctx context.Context, sessionCtx *common.Session) ([]topology.Option, description.ServerSelector, error) {
	connString, err := e.getConnectionString(ctx, sessionCtx)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	selector, err := getServerSelector(connString)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	serverOptions, err := e.getServerOptions(ctx, sessionCtx, connString)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	return []topology.Option{
		topology.WithConnString(func(cs connstring.ConnString) connstring.ConnString {
			return connString
		}),
		topology.WithServerSelectionTimeout(func(time.Duration) time.Duration {
			return common.DefaultMongoDBServerSelectionTimeout
		}),
		topology.WithServerOptions(func(so ...topology.ServerOption) []topology.ServerOption {
			return serverOptions
		}),
	}, selector, nil
}

// getServerOptions constructs server options for connecting to a MongoDB server.
func (e *Engine) getServerOptions(ctx context.Context, sessionCtx *common.Session, connString connstring.ConnString) ([]topology.ServerOption, error) {
	connectionOptions, err := e.getConnectionOptions(ctx, sessionCtx, connString)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return []topology.ServerOption{
		topology.WithConnectionOptions(func(opts ...topology.ConnectionOption) []topology.ConnectionOption {
			return connectionOptions
		}),
	}, nil
}

// getConnectionOptions constructs connection options for connecting to a MongoDB server.
func (e *Engine) getConnectionOptions(ctx context.Context, sessionCtx *common.Session, connString connstring.ConnString) ([]topology.ConnectionOption, error) {
	tlsConfig, err := e.Auth.GetTLSConfig(ctx, sessionCtx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	var authenticator auth.Authenticator
	switch {
	case connString.HasAuthParameters():
		authenticator, err = auth.CreateAuthenticator(auth.SCRAMSHA256, &auth.Cred{
		//authenticator, err = auth.CreateAuthenticator("", &auth.Cred{
			Source:      connString.AuthSource,
			Username:    connString.Username,
			Password:    connString.Password,
			PasswordSet: connString.PasswordSet,
		})
	default:
		authenticator, err = auth.CreateAuthenticator(auth.MongoDBX509, &auth.Cred{
			// MongoDB uses full certificate Subject field as a username.
			Username: "CN=" + sessionCtx.DatabaseUser,
		})
	}
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return []topology.ConnectionOption{
		topology.WithTLSConfig(func(*tls.Config) *tls.Config {
			return tlsConfig
		}),
		topology.WithOCSPCache(func(ocsp.Cache) ocsp.Cache {
			return ocsp.NewCache()
		}),
		topology.WithHandshaker(func(h topology.Handshaker) topology.Handshaker {
			// TODO(gabrielcorado): need to check if not using the "dummy" handshaker will
			// cause any issues.
			// TODO(gabrielcorado): double-check why the "dummy" handshaker
			// doesn't work with basic auth.
			return auth.Handshaker(h, &auth.HandshakeOptions{
				Authenticator: authenticator,
				AppName:       connString.AppName,
				DBUser:        connString.Username,
			})
			// return auth.Handshaker(&handshaker{}, &auth.HandshakeOptions{
			// 	Authenticator: authenticator,
			// 	AppName:       connString.AppName,
			// 	DBUser:        connString.Username,
			// })

			// Auth handshaker will authenticate the client connection using
			// x509 mechanism as the database user specified above.
			// return auth.Handshaker(
			// 	// Wrap the driver's auth handshaker with our custom no-op
			// 	// handshaker to prevent the driver from sending client metadata
			// 	// to the server as a first message. Otherwise, the actual
			// 	// client connecting to Teleport will get an error when they try
			// 	// to send its own metadata since client metadata is immutable.
			// 	&handshaker{},
			// 	&auth.HandshakeOptions{Authenticator: authenticator})
		}),
	}, nil
}

// TODO(gabrielcorado): test function to use Azure token. Remove when done.
func (e *Engine) getConnectionString2(ctx context.Context, sessionCtx *common.Session) (connstring.ConnString, error) {
	if sessionCtx.Database.IsAzure() {
		password, err := e.Auth.GetAzureAccessToken(ctx, sessionCtx)
		if err != nil {
			return connstring.ConnString{}, trace.Wrap(err)
		}
		// Azure requires database login to be <user>@<server-name> e.g.
		// alice@mysql-server-name.
		user := url.QueryEscape(fmt.Sprintf("%v@%v", sessionCtx.DatabaseUser, sessionCtx.Database.GetAzure().Name))

		// return connstring.ConnString{
		// 	Username:    user,
		// 	Password:    password,
		// 	Hosts:       []string{sessionCtx.Database.GetURI()},
		// 	AuthSource:  sessionCtx.DatabaseName,
		// 	AppName:     fmt.Sprintf("@%s@", sessionCtx.DatabaseName),
		// 	SSL:         true,
		// 	ReplicaSet:  "globaldb",
		// 	RetryWrites: false,
		// }, nil

		uri, err := connstring.Parse(
			fmt.Sprintf(
				"mongodb://%v:%v@%v/%v?ssl=true&replicaSet=globaldb&retryWrites=false",
				user,
				password,
				sessionCtx.Database.GetURI(),
				sessionCtx.DatabaseName,
			),
		)
		if err != nil {
			return connstring.ConnString{}, trace.Wrap(err)
		}

		fmt.Println("-->> Connection String: ", uri)
		return uri, nil
	}

	uri, err := url.Parse(sessionCtx.Database.GetURI())
	if err != nil {
		return connstring.ConnString{}, trace.Wrap(err)
	}
	switch uri.Scheme {
	case connstring.SchemeMongoDB, connstring.SchemeMongoDBSRV:
		return connstring.ParseAndValidate(sessionCtx.Database.GetURI())
	}
	return connstring.ConnString{Hosts: []string{sessionCtx.Database.GetURI()}}, nil
}

// getConnectionString returns connection string for the server.
func (e *Engine) getConnectionString(ctx context.Context, sessionCtx *common.Session) (connstring.ConnString, error) {
	if sessionCtx.Database.IsAzure() {
		retryCtx, cancel := context.WithTimeout(ctx, defaults.DatabaseConnectTimeout)
		defer cancel()
		lease, err := services.AcquireSemaphoreWithRetry(retryCtx, e.makeAcquireSemaphoreConfig(sessionCtx))
		if err != nil {
			return connstring.ConnString{}, trace.Wrap(err)
		}
		// Only release the semaphore after the connection has been established
		// below. If the semaphore fails to release for some reason, it will
		// expire in a minute on its own.
		defer func() {
			err := e.AuthClient.CancelSemaphoreLease(ctx, *lease)
			if err != nil {
				e.Log.WithError(err).Errorf("Failed to cancel lease: %v.", lease)
			}
		}()

		azureConnString, err := e.Auth.GetCosmosDBConnString(ctx, sessionCtx)
		if err != nil {
			return connstring.ConnString{}, trace.Wrap(err)
		}

		return connstring.Parse(azureConnString)
	}

	uri, err := url.Parse(sessionCtx.Database.GetURI())
	if err != nil {
		return connstring.ConnString{}, trace.Wrap(err)
	}
	switch uri.Scheme {
	case connstring.SchemeMongoDB, connstring.SchemeMongoDBSRV:
		return connstring.ParseAndValidate(sessionCtx.Database.GetURI())
	}
	return connstring.ConnString{Hosts: []string{sessionCtx.Database.GetURI()}}, nil
}

// makeAcquireSemaphoreConfig builds parameters for acquiring a semaphore
// for connecting to a MySQL Cloud SQL instance for this session.
func (e *Engine) makeAcquireSemaphoreConfig(sessionCtx *common.Session) services.AcquireSemaphoreWithRetryConfig {
	return services.AcquireSemaphoreWithRetryConfig{
		Service: e.AuthClient,
		// The semaphore will serialize connections to the database as specific
		// user. If we fail to release the lock for some reason, it will expire
		// in a minute anyway.
		Request: types.AcquireSemaphoreRequest{
			SemaphoreKind: "cosmos-mongo-token",
			SemaphoreName: fmt.Sprintf("%v-%v", sessionCtx.Database.GetName(), sessionCtx.DatabaseUser),
			MaxLeases:     1,
			Expires:       e.Clock.Now().Add(time.Minute),
		},
		// If multiple connections are being established simultaneously to the
		// same database as the same user, retry for a few seconds.
		Retry: utils.LinearConfig{
			Step:  time.Second,
			Max:   time.Second,
			Clock: e.Clock,
		},
	}
}

// getServerSelector returns selector for picking the server to connect to,
// which is mostly useful when connecting to a MongoDB replica set.
//
// It uses readPreference connection flag. Defaults to "primary".
func getServerSelector(connString connstring.ConnString) (description.ServerSelector, error) {
	if connString.ReadPreference == "" {
		return description.ReadPrefSelector(readpref.Primary()), nil
	}
	readPrefMode, err := readpref.ModeFromString(connString.ReadPreference)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	readPref, err := readpref.New(readPrefMode)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return description.ReadPrefSelector(readPref), nil
}

// handshaker is Mongo driver no-op handshaker that doesn't send client
// metadata when connecting to server.
type handshaker struct{}

// GetHandshakeInformation overrides default auth handshaker's logic which
// would otherwise have sent client metadata request to the server which
// would break the actual client connecting to Teleport.
func (h *handshaker) GetHandshakeInformation(context.Context, address.Address, driver.Connection) (driver.HandshakeInformation, error) {
	return driver.HandshakeInformation{}, nil
}

// Finish handshake is no-op as all auth logic will be done by the driver's
// default auth handshaker.
func (h *handshaker) FinishHandshake(context.Context, driver.Connection) error {
	return nil
}
