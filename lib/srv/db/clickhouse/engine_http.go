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

package clickhouse

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/lib/srv/db/common"
	"github.com/gravitational/teleport/lib/utils"
)

const (

	// ClubHouse HTTP headers that allows HTTP for x509 Auth.
	// Reference: https://clickhouse.com/docs/en/guides/sre/ssl-user-auth#4-testing-http
	headerClickHouseUser    = "X-ClickHouse-User"
	headerClickHouseSSLAuth = "X-ClickHouse-SSL-Certificate-Auth"
	enableVal               = "on"
)

func (e *Engine) handleHTTPConnection(ctx context.Context, sessionCtx *common.Session) error {
	tr, err := e.getTransport(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	clientConnReader := bufio.NewReader(e.clientConn)
	for {
		req, err := http.ReadRequest(clientConnReader)
		if err != nil {
			return trace.Wrap(err)
		}

		payload, err := utils.GetAndReplaceRequestBody(req)
		if err != nil {
			return trace.Wrap(err)
		}
		e.Audit.OnQuery(e.Context, sessionCtx, common.Query{Query: string(payload)})

		if err := e.handleRequest(req, sessionCtx); err != nil {
			return trace.Wrap(err)
		}

		resp, err := tr.RoundTrip(req)
		if err != nil {
			return trace.Wrap(err)
		}
		defer resp.Body.Close()

		if err := resp.Write(e.clientConn); err != nil {
			return trace.Wrap(err)
		}
	}
}

func (e *Engine) handleRequest(req *http.Request, sessionCtx *common.Session) error {
	uri, err := url.Parse(sessionCtx.Database.GetURI())
	if err != nil {
		return trace.Wrap(err)
	}

	req.URL.Scheme = "https"
	req.URL.Host = uri.Host

	// Delete Headers set by a ClickHouse client.
	req.Header.Del(headerClickHouseUser)
	req.Header.Del("Authorization")

	// Set ClickHouse Headers to enforce x509 auth for HTTP protocol.
	req.Header.Add(headerClickHouseSSLAuth, enableVal)
	req.Header.Add(headerClickHouseUser, sessionCtx.DatabaseUser)
	return nil

}

func (e *Engine) sendErrorHTTP(err error) {
	statusCode := http.StatusInternalServerError
	if trace.IsAccessDenied(err) {
		statusCode = http.StatusUnauthorized
	}

	response := &http.Response{
		ProtoMajor: 1,
		ProtoMinor: 1,
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(err.Error())),
	}
	if err := response.Write(e.clientConn); err != nil {
		return
	}
}
