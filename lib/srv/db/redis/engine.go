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

package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/srv/db/common"
	"github.com/gravitational/teleport/lib/srv/db/common/role"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/sirupsen/logrus"
	"github.com/smallnest/resp3"
)

// Engine implements common.Engine.
type Engine struct {
	// Auth handles database access authentication.
	Auth common.Auth
	// Audit emits database access audit events.
	Audit common.Audit
	// Context is the database server close context.
	Context context.Context
	// Clock is the clock interface.
	Clock clockwork.Clock
	// Log is used for logging.
	Log logrus.FieldLogger
	// proxyConn is a client connection.
	proxyConn net.Conn

	clientReader *resp3.Reader
	clientWriter *resp3.Writer
}

func (e *Engine) InitializeConnection(clientConn net.Conn, _ *common.Session) error {
	e.proxyConn = clientConn
	e.clientReader = resp3.NewReader(clientConn)
	e.clientWriter = resp3.NewWriter(clientConn)

	return nil
}

// authorizeConnection does authorization check for MongoDB connection about
// to be established.
func (e *Engine) authorizeConnection(ctx context.Context, sessionCtx *common.Session) error {
	ap, err := e.Auth.GetAuthPreference(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	mfaParams := services.AccessMFAParams{
		Verified:       sessionCtx.Identity.MFAVerified != "",
		AlwaysRequired: ap.GetRequireSessionMFA(),
	}

	dbRoleMatchers := role.DatabaseRoleMatchers(
		sessionCtx.Database.GetProtocol(),
		sessionCtx.DatabaseUser,
		sessionCtx.DatabaseName,
	)
	err = sessionCtx.Checker.CheckAccess(
		sessionCtx.Database,
		mfaParams,
		dbRoleMatchers...,
	)
	if err != nil {
		e.Audit.OnSessionStart(e.Context, sessionCtx, err)
		return trace.Wrap(err)
	}
	return nil
}

func (e *Engine) SendError(redisErr error) {
	if redisErr == nil || utils.IsOKNetworkError(redisErr) {
		return
	}

	//TODO(jakub): We can send errors only after reading command from the connected client.
	e.Log.Debugf("sending error to Redis client: %v", redisErr)

	if err := e.sendToClient(redisErr.Error()); err != nil {
		e.Log.Errorf("Failed to send message to the client: %v", err)
		return
	}
}

func (e *Engine) sendToClient(data string) error {
	if _, err := e.clientWriter.WriteString(data); err != nil {
		return trace.Wrap(err)
	}

	if err := e.clientWriter.Flush(); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (e *Engine) HandleConnection(ctx context.Context, sessionCtx *common.Session) error {
	// Check that the user has access to the database.
	err := e.authorizeConnection(ctx, sessionCtx)
	if err != nil {
		return trace.Wrap(err, "error authorized database access")
	}

	tlsConfig, err := e.Auth.GetTLSConfig(ctx, sessionCtx)
	if err != nil {
		return trace.Wrap(err)
	}

	connectionOptions, err := ParseRedisURI(sessionCtx.Database.GetURI())
	if err != nil {
		return trace.BadParameter("Redis connection string is incorrect: %v", err)
	}

	var (
		redisConn      net.Conn
		connectionAddr = fmt.Sprintf("%s:%s", connectionOptions.address, connectionOptions.port)
	)

	// TODO(jakub): Use system CA bundle if connecting to AWS.
	if connectionOptions.cluster {
		redisConn, err = tls.Dial("tcp", connectionAddr, tlsConfig)
	} else {
		redisConn, err = tls.Dial("tcp", connectionAddr, tlsConfig)
	}
	if err != nil {
		return trace.Wrap(err)
	}
	defer redisConn.Close()

	e.Log.Debug("created a new Redis client, sending ping to test the connection")

	e.Audit.OnSessionStart(e.Context, sessionCtx, nil)
	defer e.Audit.OnSessionEnd(e.Context, sessionCtx)

	if err := e.process(ctx, redisConn); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (e *Engine) readClientCmd(ctx context.Context) (*resp3.Value, []byte, error) {
	val, data, err := e.clientReader.ReadValue()
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}

	return val, data, nil
}

func (e *Engine) process(ctx context.Context, redisConn net.Conn) error {
	redisReader := resp3.NewReader(redisConn)
	redisWriter := resp3.NewWriter(redisConn)

	for {
		val, _, err := e.readClientCmd(ctx)
		if err != nil {
			return trace.Wrap(err)
		}

		e.Log.Debugf("redis cmd: %s", val.ToRESP3String())

		// Here the command is sent to the DB.
		_, err = redisWriter.WriteString(val.ToRESP3String())
		if err != nil {
			return trace.Wrap(err)
		}

		if err := redisWriter.Flush(); err != nil {
			return trace.Wrap(err)
		}

		respVal, _, err := redisReader.ReadValue()
		if err != nil {
			return trace.Wrap(err)
		}

		if err := e.sendToClient(respVal.ToRESP3String()); err != nil {
			return trace.Wrap(err)
		}
	}
}
