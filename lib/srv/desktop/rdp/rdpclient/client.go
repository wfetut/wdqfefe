//go:build desktop_access_rdp
// +build desktop_access_rdp

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

package rdpclient

// Some implementation details that don't belong in the public godoc:
// This package wraps a Rust library based on https://crates.io/crates/rdp-rs.
//
// The Rust library is statically-compiled and called via CGO.
// The Go code sends and receives the CGO versions of Rust RDP events
// https://docs.rs/rdp-rs/0.1.0/rdp/core/event/index.html and translates them
// to the desktop protocol versions.
//
// The flow is roughly this:
//    Go                                Rust
// ==============================================
//  rdpclient.New -----------------> connect_rdp
//                   *connected*
//
//            *register output callback*
//                -----------------> read_rdp_output
//  handleBitmap  <----------------
//  handleBitmap  <----------------
//  handleBitmap  <----------------
//           *output streaming continues...*
//
//              *user input messages*
//  ReadMessage(MouseMove) ------> write_rdp_pointer
//  ReadMessage(MouseButton) ----> write_rdp_pointer
//  ReadMessage(KeyboardButton) -> write_rdp_keyboard
//            *user input continues...*
//
//        *connection closed (client or server side)*
//    Wait       -----------------> close_rdp
//

/*
// Flags to include the static Rust library.
#cgo linux,386 LDFLAGS: -L${SRCDIR}/../../../../../target/i686-unknown-linux-gnu/release
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/../../../../../target/x86_64-unknown-linux-gnu/release
#cgo linux,arm LDFLAGS: -L${SRCDIR}/../../../../../target/arm-unknown-linux-gnueabihf/release
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../../../../target/aarch64-unknown-linux-gnu/release
#cgo linux LDFLAGS: -l:librdp_client.a -lpthread -ldl -lm
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/../../../../../target/x86_64-apple-darwin/release
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/../../../../../target/aarch64-apple-darwin/release
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security -lrdp_client -lpthread -ldl -lm
#include <librdprs.h>
*/
import "C"

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"runtime/cgo"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

func init() {
	// initialize the Rust logger by setting $RUST_LOG based
	// on the logrus log level
	// (unless RUST_LOG is already explicitly set, then we
	// assume the user knows what they want)
	if rl := os.Getenv("RUST_LOG"); rl == "" {
		var rustLogLevel string
		switch l := logrus.GetLevel(); l {
		case logrus.TraceLevel:
			rustLogLevel = "trace"
		case logrus.DebugLevel:
			rustLogLevel = "debug"
		case logrus.InfoLevel:
			rustLogLevel = "info"
		case logrus.WarnLevel:
			rustLogLevel = "warn"
		default:
			rustLogLevel = "error"
		}

		os.Setenv("RUST_LOG", rustLogLevel)
	}

	C.init()
}

// Client is the RDP client.
// Its lifecycle is:
//
// ```
// rdpc := New()         // creates client
// rdpc.Run()   // starts rdp and waits for the duration of the connection
// ```
type Client struct {
	Config

	// handle allows the rust code to call back into the client.
	handle cgo.Handle

	// RDP client on the Rust side.
	rustClient *C.Client

	// Synchronization point to prevent input messages from being forwarded
	// until the connection is established.
	// Used with sync/atomic, 0 means false, 1 means true.
	readyForInput uint32

	clientActivityMu sync.RWMutex
	clientLastActive time.Time
}

// New creates and connects a new Client based on cfg.
func New(cfg Config) (*Client, error) {
	if err := cfg.checkAndSetDefaults(); err != nil {
		return nil, err
	}
	c := &Client{
		Config:        cfg,
		readyForInput: 0,
	}

	if err := cfg.AuthorizeFn(cfg.Username); err != nil {
		return nil, trace.Wrap(err)
	}

	c.handle = cgo.NewHandle(c)

	return c, nil
}

// Run starts the rdp client and blocks until the client disconnects,
// then ensures the cleanup is run.
func (c *Client) Run(ctx context.Context) error {
	defer c.cleanup()

	if err := c.connect(ctx); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

// getNonBlockingFDFromTLSConn takes a *tls.Conn and returns the underlying file descriptor as
// a non-blocking file descriptor which can be passed to Rust and used by a tokio::net::TcpStream.
func getNonBlockingFDFromTLSConn(conn *tls.Conn) (int, error) {
	// First, obtain the underlying connection object
	netConn := conn.NetConn()
	conn.SetDeadline()
	conn.Read()

	// Cast the connection object to a net.TCPConn
	tcpConn, ok := netConn.(*net.TCPConn)
	if !ok {
		return -1, fmt.Errorf("failed to cast to *net.TCPConn")
	}

	// Obtain the file descriptor from the net.TCPConn
	file, err := tcpConn.File()
	if err != nil {
		return -1, err
	}

	// Get the file descriptor as an int
	fd := int(file.Fd())

	// Set the file descriptor to non-blocking mode (important for usage in C)
	if err := syscall.SetNonblock(fd, true); err != nil {
		return -1, err
	}

	return fd, nil
}

func (c *Client) connect(ctx context.Context) error {
	userCertDER, userKeyDER, err := c.GenerateUserCert(ctx, c.Username, c.CertTTL)
	if err != nil {
		return trace.Wrap(err)
	}

	// Addr and username strings only need to be valid for the duration of
	// C.connect_rdp. They are copied on the Rust side and can be freed here.
	addr := C.CString(c.Addr)
	defer C.free(unsafe.Pointer(addr))
	username := C.CString(c.Username)
	defer C.free(unsafe.Pointer(username))

	res := C.connect_rdp(
		C.uintptr_t(c.handle),
		C.CGOConnectParams{
			go_addr:     addr,
			go_username: username,
			// cert length and bytes.
			cert_der_len: C.uint32_t(len(userCertDER)),
			cert_der:     (*C.uint8_t)(unsafe.Pointer(&userCertDER[0])),
			// key length and bytes.
			key_der_len:             C.uint32_t(len(userKeyDER)),
			key_der:                 (*C.uint8_t)(unsafe.Pointer(&userKeyDER[0])),
			allow_clipboard:         C.bool(c.AllowClipboard),
			allow_directory_sharing: C.bool(c.AllowDirectorySharing),
			show_desktop_wallpaper:  C.bool(c.ShowDesktopWallpaper),
		},
	)
	if res.err != C.ErrCodeSuccess {
		return trace.ConnectionProblem(nil, "RDP connection failed")
	}
	c.rustClient = res.client

	return nil
}

// cleanup frees the Rust client and
// frees the memory of the cgo.Handle.
// This function should only be called
// once per Client.
func (c *Client) cleanup() {
	// Let the Rust side free its data
	if c.rustClient != nil {
		C.free_rdp(c.rustClient)
	}

	// Release the memory of the cgo.Handle
	if c.handle != 0 {
		c.handle.Delete()
	}

}

// GetClientLastActive returns the time of the last recorded activity.
// For RDP, "activity" is defined as user-input messages
// (mouse move, button press, etc.)
func (c *Client) GetClientLastActive() time.Time {
	c.clientActivityMu.RLock()
	defer c.clientActivityMu.RUnlock()
	return c.clientLastActive
}

// UpdateClientActivity updates the client activity timestamp.
func (c *Client) UpdateClientActivity() {
	c.clientActivityMu.Lock()
	c.clientLastActive = time.Now().UTC()
	c.clientActivityMu.Unlock()
}
