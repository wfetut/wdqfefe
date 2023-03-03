/*
Copyright 2018 Gravitational, Inc.

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

package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/gravitational/trace"
	"github.com/julienschmidt/httprouter"
	"golang.org/x/crypto/ssh"

	authproto "github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils/keys"
	wanlib "github.com/gravitational/teleport/lib/auth/webauthn"
	"github.com/gravitational/teleport/lib/client"

	"github.com/gravitational/teleport/lib/reversetunnel"
	"github.com/gravitational/teleport/lib/sshutils/scp"
)

// fileTransferRequest describes HTTP file transfer request
type fileTransferRequest struct {
	// Server describes a server to connect to (serverId|hostname[:port]).
	server string
	// Login is Linux username to connect as.
	login string
	// Namespace is node namespace.
	namespace string
	// Cluster is the name of the remote cluster to connect to.
	cluster string
	// remoteLocation is file remote location
	remoteLocation string
	// filename is a file name
	filename string
	// upload determines if the request is an upload. Empty string means download
	upload string
}

func (h *Handler) fileTransferhandler(
	w http.ResponseWriter,
	r *http.Request,
	p httprouter.Params,
	sctx *SessionContext,
	site reversetunnel.RemoteSite,
) (interface{}, error) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		errMsg := "Error upgrading to websocket"
		http.Error(w, errMsg, http.StatusInternalServerError)
	}
	defer ws.Close()

	query := r.URL.Query()
	req := fileTransferRequest{
		cluster:        site.GetName(),
		login:          p.ByName("login"),
		server:         p.ByName("server"),
		remoteLocation: query.Get("location"),
		filename:       query.Get("filename"),
		upload:         p.ByName("upload"),
		namespace:      defaults.Namespace,
	}

	tc, err := h.createClient(ws, r, p, sctx, site)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	nodeClient, err := connectToNode(r.Context(), tc, ws, r, p, sctx, site)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	fmt.Printf("%+v\n", nodeClient)

	h.handler(r.Context(), ws, sctx, req, nodeClient)
	// return nil response here because we upgraded to wss
	return nil, nil
}

const (
	WebsocketVersion      = "1"
	WebsocketRawData      = "r"
	WebsocketMfaChallenge = "n"
	WebsocketMetadata     = "m"
)

func (h *Handler) handler(ctx context.Context, ws *websocket.Conn, sctx *SessionContext, req fileTransferRequest, nc *client.NodeClient) {
	stream, err := NewFileTransferStream(ws)
	if err != nil {
		return
	}

	if req.upload == "" {
		stream.download(req, ctx, ws, sctx, nc)
	}
}

func (h *Handler) createClient(ws *websocket.Conn, r *http.Request, p httprouter.Params, sctx *SessionContext, site reversetunnel.RemoteSite) (*client.TeleportClient, error) {
	ctx := r.Context()
	clientConfig, err := makeTeleportClientConfig(ctx, sctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	clientConfig.Namespace = defaults.Namespace
	if !types.IsValidNamespace(defaults.Namespace) {
		return nil, trace.BadParameter("invalid namespace %q", clientConfig.Namespace)
	}

	clientConfig.HostLogin = p.ByName("login")
	if clientConfig.HostLogin == "" {
		return nil, trace.BadParameter("missing login")
	}

	servers, err := sctx.cfg.RootClient.GetNodes(ctx, clientConfig.Namespace)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	server := p.ByName("server")
	hostName, hostPort, err := resolveServerHostPort(server, servers)
	if err != nil {
		return nil, trace.BadParameter("invalid server name %q: %v", server, err)
	}

	clientConfig.SiteName = site.GetName()
	if err := clientConfig.ParseProxyHost(h.ProxyHostPort()); err != nil {
		return nil, trace.BadParameter("failed to parse proxy address: %v", err)
	}
	clientConfig.Host = hostName
	clientConfig.HostPort = hostPort
	clientConfig.ClientAddr = r.RemoteAddr

	tc, err := client.NewClient(clientConfig)
	if err != nil {
		return nil, trace.BadParameter("failed to create client: %v", err)
	}
	return tc, nil
}

func connectToNode(ctx context.Context, tc *client.TeleportClient, ws *websocket.Conn, r *http.Request, p httprouter.Params, sctx *SessionContext, site reversetunnel.RemoteSite) (*client.NodeClient, error) {
	stream, err := NewFileTransferStream(ws)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	pk, err := keys.ParsePrivateKey(sctx.cfg.Session.GetPriv())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	pc, err := tc.ConnectToProxy(ctx)

	details, err := pc.ClusterDetails(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	nodeAddrs, err := tc.GetTargetNodes(ctx, pc)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if len(nodeAddrs) == 0 {
		return nil, trace.BadParameter("no target host specified")
	}

	key, err := pc.IssueUserCertsWithMFA(ctx, client.ReissueParams{
		NodeName:       nodeAddrs[0],
		RouteToCluster: site.GetName(),
		ExistingCreds: &client.Key{
			PrivateKey: pk,
			Cert:       sctx.cfg.Session.GetPub(),
			TLSCert:    sctx.cfg.Session.GetTLSCert(),
		},
	}, promptFileTransferMFAChallenge(stream, protobufMFACodec{}))

	am, err := key.AsAuthMethod()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	user := tc.Config.HostLogin

	nodeClient, err := pc.ConnectToNode(ctx,
		client.NodeDetails{Addr: nodeAddrs[0], Namespace: tc.Namespace, Cluster: tc.SiteName},
		user,
		details,
		[]ssh.AuthMethod{am})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return nodeClient, nil
}

func (f *FileTransferStream) download(req fileTransferRequest,
	ctx context.Context,
	ws *websocket.Conn,
	sctx *SessionContext,
	nc *client.NodeClient,
) {
	writer, err := ws.NextWriter(websocket.BinaryMessage)

	cmd, err := scp.CreateSocketDownload(scp.WebsocketFileRequest{
		RemoteLocation: req.remoteLocation,
		Writer:         writer,
		FileName:       req.filename,
		User:           sctx.GetUser(),
	})
	if err != nil {
		return
	}

	nc.ExecuteSCP(ctx, cmd)
}

func promptFileTransferMFAChallenge(
	stream *FileTransferStream,
	codec mfaCodec,
) client.PromptMFAChallengeHandler {
	return func(ctx context.Context, proxyAddr string, c *authproto.MFAAuthenticateChallenge) (*authproto.MFAAuthenticateResponse, error) {
		var challenge *client.MFAAuthenticateChallenge

		// Convert from proto to JSON types.
		switch {
		case c.GetWebauthnChallenge() != nil:
			challenge = &client.MFAAuthenticateChallenge{
				WebauthnChallenge: wanlib.CredentialAssertionFromProto(c.WebauthnChallenge),
			}
		default:
			return nil, trace.AccessDenied("only hardware keys are supported on the web terminal, please register a hardware device to connect to this server")
		}

		if err := stream.writeChallenge(challenge, codec); err != nil {
			return nil, trace.Wrap(err)
		}

		resp, err := stream.readChallenge(codec)
		return resp, trace.Wrap(err)
	}
}

type FileTransferStream struct {
	// buffer is a buffer used to store the remaining payload data if it did not
	// fit into the buffer provided by the callee to Read method
	buffer []byte

	// once ensures that resizeC is closed at most one time
	once sync.Once

	// mu protects writes to ws
	mu sync.Mutex
	// ws the connection to the UI
	ws *websocket.Conn
}

func NewFileTransferStream(ws *websocket.Conn, opts ...func(*FileTransferStream)) (*FileTransferStream, error) {
	switch {
	case ws == nil:
		return nil, trace.BadParameter("required parameter ws not provided")
	}

	f := &FileTransferStream{
		ws: ws,
	}

	return f, nil
}

func (f *FileTransferStream) writeChallenge(challenge *client.MFAAuthenticateChallenge, codec mfaCodec) error {
	// Send the challenge over the socket.
	msg, err := codec.encode(challenge, WebsocketMfaChallenge)
	if err != nil {
		return trace.Wrap(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return trace.Wrap(f.ws.WriteMessage(websocket.BinaryMessage, append([]byte(WebsocketMfaChallenge), msg...)))
}

// readChallenge reads and decodes the challenge response from the
// websocket in the correct format.
func (f *FileTransferStream) readChallenge(codec mfaCodec) (*authproto.MFAAuthenticateResponse, error) {
	// Read the challenge response.
	ty, bytes, err := f.ws.ReadMessage()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	if ty != websocket.BinaryMessage {
		return nil, trace.BadParameter("expected websocket.BinaryMessage, got %v", ty)
	}

	resp, err := codec.decode(bytes, WebsocketMfaChallenge)
	return resp, trace.Wrap(err)
}

func (f *FileTransferStream) Write(data []byte) (n int, err error) {
	f.mu.Lock()
	err = f.ws.WriteMessage(websocket.BinaryMessage, append([]byte(WebsocketRawData), data...))
	f.mu.Unlock()
	if err != nil {
		return 0, trace.Wrap(err)
	}

	return len(data), nil
}
