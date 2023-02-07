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
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/gravitational/trace"
	"github.com/julienschmidt/httprouter"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"

	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/reversetunnel"
	// "github.com/gravitational/teleport/lib/sshutils/scp"
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
}

type FileTransferHandler struct {
	// ctx is a web session context for the currently logged in user.
	ctx           *SessionContext
	authClient    auth.ClientI
	proxyHostPort string
	// stream manages sending and receiving [Envelope] to the UI
	// for the duration of the session
	stream *FileTransferStream
}

type FileTransferStream struct {
	// encoder is used to encode UTF-8 strings.
	encoder *encoding.Encoder
	// decoder is used to decode UTF-8 strings.
	decoder *encoding.Decoder

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

func (h *Handler) fileTransferhandle(
	w http.ResponseWriter,
	r *http.Request,
	p httprouter.Params,
	sctx *SessionContext,
	site reversetunnel.RemoteSite,
) (interface{}, error) {
	// desktopName := p.ByName("desktopName")
	// if desktopName == "" {
	// 	return nil, trace.BadParameter("missing desktopName in request URL")
	// }

	h.fileTransferConnection(w, r, p, sctx, site)
	return nil, nil
}

func (h *Handler) fileTransferConnection(w http.ResponseWriter, r *http.Request, p httprouter.Params, sctx *SessionContext, site reversetunnel.RemoteSite) {
	// query := r.URL.Query()
	// req := fileTransferRequest{
	// 	cluster:        site.GetName(),
	// 	login:          p.ByName("login"),
	// 	server:         p.ByName("server"),
	// 	remoteLocation: query.Get("location"),
	// 	filename:       query.Get("filename"),
	// 	namespace:      defaults.Namespace,
	// }

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		errMsg := "Error upgrading to websocket"
		http.Error(w, errMsg, http.StatusInternalServerError)
		return
	}

	h.handler(ws, r, p, sctx, site)
}

func (h *Handler) handler(ws *websocket.Conn, r *http.Request, p httprouter.Params, sctx *SessionContext, site reversetunnel.RemoteSite) {
	defer ws.Close()
	// defer ws.Close()
	//
	stream, err := NewFileTransferStream(ws)
	if err != nil {
		//
		return
	}

	fmt.Println("---")
	fmt.Printf("stream: %+v\n", stream)
	fmt.Println("---")

	// tc, err := h.createClient(r, ws, p, sctx, site)
	// if err != nil {
	// 	fmt.Printf("err: %+v\n", err)
	// 	return
	// }
	// fmt.Println("-----")
	// fmt.Printf("made client: %+v\n", tc)
	// fmt.Println("-----")
}

func (h *Handler) createClient(r *http.Request, ws *websocket.Conn, p httprouter.Params, sctx *SessionContext, site reversetunnel.RemoteSite) (*client.TeleportClient, error) {
	ctx := r.Context()
	// query := r.URL.Query()
	// req := fileTransferRequest{
	// 	cluster:        site.GetName(),
	// 	login:          p.ByName("login"),
	// 	server:         p.ByName("server"),
	// 	remoteLocation: query.Get("location"),
	// 	filename:       query.Get("filename"),
	// 	namespace:      defaults.Namespace,
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

func (f *FileTransferHandler) download(req fileTransferRequest, httpReq *http.Request, w http.ResponseWriter) error {
	// cmd, err := scp.CreateHTTPDownload(scp.HTTPTransferRequest{
	// 	RemoteLocation: req.remoteLocation,
	// 	HTTPResponse:   w,
	// 	User:           f.ctx.GetUser(),
	// })
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	//
	// tc, err := f.createClient(req, httpReq)
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	//
	// err = tc.ExecuteSCP(httpReq.Context(), cmd)
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	//
	return nil
}

func (f *FileTransferHandler) upload(req fileTransferRequest, httpReq *http.Request) error {
	// cmd, err := scp.CreateHTTPUpload(scp.HTTPTransferRequest{
	// 	RemoteLocation: req.remoteLocation,
	// 	FileName:       req.filename,
	// 	HTTPRequest:    httpReq,
	// 	User:           f.ctx.GetUser(),
	// })
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	//
	// tc, err := f.createClient(req, httpReq)
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	//
	// err = tc.ExecuteSCP(httpReq.Context(), cmd)
	// if err != nil {
	// 	return trace.Wrap(err)
	// }

	return nil
}

// func (f *FileTransferHandler) createClient(req fileTransferRequest, httpReq *http.Request) (*client.TeleportClient, error) {

// }

func NewFileTransferStream(ws *websocket.Conn) (*FileTransferStream, error) {
	switch {
	case ws == nil:
		return nil, trace.BadParameter("required parameter ws not provided")
	}

	t := &FileTransferStream{
		ws:      ws,
		encoder: unicode.UTF8.NewEncoder(),
		decoder: unicode.UTF8.NewDecoder(),
	}

	return t, nil
}
