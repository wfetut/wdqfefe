/*
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
 */

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"

	"github.com/gravitational/teleport"
	authproto "github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/defaults"
)

// WSStream handles web socket communication with
// the frontend.
type baseWSStream struct {
	// encoder is used to encode UTF-8 strings.
	encoder *encoding.Encoder
	// decoder is used to decode UTF-8 strings.
	decoder *encoding.Decoder

	// buffer is a buffer used to store the remaining payload data if it did not
	// fit into the buffer provided by the callee to Read method
	buffer []byte

	// ws the connection to the UI
	ws WSConn

	// log holds the structured logger.
	log logrus.FieldLogger
	// handlers is a map of message types to handlers.
	handlers map[string]WSHandlerFunc
}

func newBaseWSStream(ws WSConn, log logrus.FieldLogger, handlers map[string]WSHandlerFunc) *baseWSStream {
	w := &baseWSStream{
		ws:       ws,
		log:      log,
		encoder:  unicode.UTF8.NewEncoder(),
		decoder:  unicode.UTF8.NewDecoder(),
		handlers: handlers,
	}

	return w
}

type mfaWSStream struct {
	baseWSStream

	challengeC chan Envelope
}

func newMFAWSStream(ws WSConn, log logrus.FieldLogger, handlers map[string]WSHandlerFunc) *mfaWSStream {
	w := &mfaWSStream{
		baseWSStream: *newBaseWSStream(ws, log, handlers),
		challengeC:   make(chan Envelope, 1),
	}

	localHandlers := map[string]WSHandlerFunc{
		// MFAAuthenticateChallenge is a message sent by the server to the client
		// to initiate MFA authentication.
		defaults.WebsocketWebauthnChallenge: w.processChallenge,
	}

	w.handlers = mergeMaps(w.handlers, localHandlers)

	return w
}

type WSStream struct {
	mfaWSStream
	rawC       chan Envelope
	completedC chan struct{}

	// once ensures that all channels are closed at most one time.
	once sync.Once
}

func NewWStream(ctx context.Context, ws WSConn, log logrus.FieldLogger, handlers map[string]WSHandlerFunc) *WSStream {
	w := &WSStream{
		mfaWSStream: *newMFAWSStream(ws, log, handlers),
		completedC:  make(chan struct{}),
		rawC:        make(chan Envelope, 100),
	}

	localHandlers := map[string]WSHandlerFunc{
		defaults.WebsocketRaw: w.processRawMessage,
	}
	w.handlers = mergeMaps(w.handlers, localHandlers)

	go w.processMessages(ctx)

	return w
}

func (t *WSStream) processRawMessage(ctx context.Context, envelope Envelope) {
	select {
	case <-ctx.Done():
		return
	case t.rawC <- envelope:
	default:
	}
}

type mfaChallenger interface {
	writeChallenge(challenge *client.MFAAuthenticateChallenge, codec mfaCodec) error
	readChallengeResponse(codec mfaCodec) (*authproto.MFAAuthenticateResponse, error)
	readChallenge(codec mfaCodec) (*authproto.MFAAuthenticateChallenge, error)
}

// writeChallenge encodes and writes the challenge to the
// websocket in the correct format.
func (t *mfaWSStream) writeChallenge(challenge *client.MFAAuthenticateChallenge, codec mfaCodec) error {
	// Send the challenge over the socket.
	msg, err := codec.encode(challenge, defaults.WebsocketWebauthnChallenge)
	if err != nil {
		return trace.Wrap(err)
	}

	return trace.Wrap(t.ws.WriteMessage(websocket.BinaryMessage, msg))
}

// readChallengeResponse reads and decodes the challenge response from the
// websocket in the correct format.
func (t *mfaWSStream) readChallengeResponse(codec mfaCodec) (*authproto.MFAAuthenticateResponse, error) {
	select {
	case envelope, ok := <-t.challengeC:
		if !ok {
			return nil, io.EOF
		}
		resp, err := codec.decodeResponse([]byte(envelope.Payload), defaults.WebsocketWebauthnChallenge)
		return resp, trace.Wrap(err)
	}
}

// readChallenge reads and decodes the challenge from the
// websocket in the correct format.
func (t *mfaWSStream) readChallenge(codec mfaCodec) (*authproto.MFAAuthenticateChallenge, error) {
	select {
	case envelope, ok := <-t.challengeC:
		if !ok {
			return nil, io.EOF
		}
		challenge, err := codec.decodeChallenge([]byte(envelope.Payload), defaults.WebsocketWebauthnChallenge)
		return challenge, trace.Wrap(err)
	}
}

func (t *mfaWSStream) processChallenge(ctx context.Context, envelope Envelope) {
	select {
	case <-ctx.Done():
		return
	case t.challengeC <- envelope:
	default:
	}
}

// writeAuditEvent encodes and writes the audit event to the
// websocket in the correct format.
func (t *WSStream) writeAuditEvent(event []byte) error {
	// UTF-8 encode the error message and then wrap it in a raw envelope.
	encodedPayload, err := t.encoder.String(string(event))
	if err != nil {
		return trace.Wrap(err)
	}

	envelope := &Envelope{
		Version: defaults.WebsocketVersion,
		Type:    defaults.WebsocketAudit,
		Payload: encodedPayload,
	}

	envelopeBytes, err := proto.Marshal(envelope)
	if err != nil {
		return trace.Wrap(err)
	}

	// Send bytes over the websocket to the web client.
	return trace.Wrap(t.ws.WriteMessage(websocket.BinaryMessage, envelopeBytes))
}

// Write wraps the data bytes in a raw envelope and sends.
func (t *baseWSStream) Write(data []byte) (n int, err error) {
	// UTF-8 encode data and wrap it in a raw envelope.
	encodedPayload, err := t.encoder.String(string(data))
	if err != nil {
		return 0, trace.Wrap(err)
	}
	envelope := &Envelope{
		Version: defaults.WebsocketVersion,
		Type:    defaults.WebsocketRaw,
		Payload: encodedPayload,
	}
	envelopeBytes, err := proto.Marshal(envelope)
	if err != nil {
		return 0, trace.Wrap(err)
	}

	// Send bytes over the websocket to the web client.
	if err = t.ws.WriteMessage(websocket.BinaryMessage, envelopeBytes); err != nil {
		return 0, trace.Wrap(err)
	}

	return len(data), nil
}

// Read provides data received from [defaults.WebsocketRaw] envelopes. If
// the previous envelope was not consumed in the last read any remaining data
// is returned prior to processing the next envelope.
func (t *WSStream) Read(out []byte) (n int, err error) {
	if len(t.buffer) > 0 {
		n := copy(out, t.buffer)
		if n == len(t.buffer) {
			t.buffer = []byte{}
		} else {
			t.buffer = t.buffer[n:]
		}
		return n, nil
	}

	select {
	case <-t.completedC:
		return 0, io.EOF
	case envelope := <-t.rawC:
		data, err := t.decoder.Bytes([]byte(envelope.Payload))
		if err != nil {
			return 0, trace.Wrap(err)
		}

		n := copy(out, data)
		// if the payload size is greater than [out], store the remaining
		// part in the buffer to be processed on the next Read call
		if len(data) > n {
			t.buffer = data[n:]
		}
		return n, nil
	}
}

func (t *WSStream) Close() error {
	t.once.Do(func() {
		<-t.completedC

		close(t.rawC)
		t.mfaWSStream.close()
	})

	return nil
}

func (t *mfaWSStream) close() {
	// fixme(jakule)
	close(t.challengeC)
}

// SendCloseMessage sends a close message on the web socket.
func (t *baseWSStream) SendCloseMessage() error {
	// Send close envelope to web terminal upon exit without an error.
	envelope := &Envelope{
		Version: defaults.WebsocketVersion,
		Type:    defaults.WebsocketClose,
	}
	envelopeBytes, err := proto.Marshal(envelope)
	if err != nil {
		return trace.Wrap(err)
	}

	return trace.Wrap(t.ws.WriteMessage(websocket.BinaryMessage, envelopeBytes))
}

// Replace \n with \r\n so the message is correctly aligned.
var replacer = strings.NewReplacer("\r\n", "\r\n", "\n", "\r\n")

// writeError displays an error in the terminal window.
func (t *baseWSStream) writeError(msg string) {
	if _, writeErr := replacer.WriteString(t, msg); writeErr != nil {
		t.log.WithError(writeErr).Warnf("Unable to send error to terminal: %v", msg)
	}
}

func (t *WSStream) processMessages(ctx context.Context) {
	defer func() {
		close(t.completedC)
	}()
	t.baseWSStream.processMessages(ctx)
}

func (t *baseWSStream) processMessages(ctx context.Context) {
	t.ws.SetReadLimit(teleport.MaxHTTPRequestSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			ty, bytes, err := t.ws.ReadMessage()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
					websocket.IsCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					return
				}

				msg := err.Error()
				if len(bytes) > 0 {
					msg = string(bytes)
				}
				select {
				case <-ctx.Done():
				default:
					t.writeError(msg)
					return
				}
			}

			if ty != websocket.BinaryMessage {
				t.writeError(fmt.Sprintf("Expected binary message, got %v", ty))
				return
			}

			var envelope Envelope
			if err := proto.Unmarshal(bytes, &envelope); err != nil {
				t.writeError(fmt.Sprintf("Unable to parse message payload %v", err))
				return
			}

			switch envelope.Type {
			case defaults.WebsocketClose:
				return
			default:
				if t.handlers == nil {
					continue
				}

				handler, ok := t.handlers[envelope.Type]
				if !ok {
					t.log.Warnf("Received web socket envelope with unknown type %v", envelope.Type)
					continue
				}

				go handler(ctx, envelope)
			}
		}
	}
}

func mergeMaps[K comparable, V any](v1, v2 map[K]V) map[K]V {
	res := make(map[K]V, len(v1)+len(v2))
	for k, v := range v1 {
		res[k] = v
	}
	for k, v := range v2 {
		res[k] = v
	}
	return res
}
