// Copyright 2021 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! This crate contains an RDP Client with the minimum functionality required
//! for Teleport's Desktop Access feature.
//!
//! Along with core RDP functionality, it contains code for:
//! - Calling functions defined in Go (these are declared in an `extern "C"` block)
//! - Functions to be called from Go (any function prefixed with the `#[no_mangle]`
//!   macro and a `pub unsafe extern "C"`).
//! - Structs for passing between the two (those prefixed with the `#[repr(C)]` macro
//!   and whose name begins with `CGO`)
//!
//! Memory management at this interface can be tricky, given the long list of rules
//! required by CGO (https://pkg.go.dev/cmd/cgo). We can simplify our job in this
//! regard by sticking to the following design principles:
//!
//! 1) Whichever side of the Rust-Go interface allocates some memory on the heap is
//!    responsible for freeing it.
//! 2) And therefore whenever one side of the Rust-Go interface is passed some memory
//!    it didn't allocate but needs to hold on to, is responsible for copying it to its
//!    own respective heap.
//!
//! In practice, this means that all the functions called from Go (those
//! prefixed with `pub unsafe extern "C"`) MUST NOT hang on to any of the
//! pointers passed in to them after they return. All pointer data that needs to
//! persist MUST be copied into Rust-owned memory.

mod cliprdr;
mod devolutions_gateway_utils;
mod errors;
mod piv;
mod rdpdr;
mod util;
mod vchan;

#[macro_use]
extern crate log;
#[macro_use]
extern crate num_derive;

use anyhow::Context;
use anyhow::{Error as AnyhowError, Result};
use devolutions_gateway_utils::{
    read_cleanpath_pdu, CleanPathError, NegotiationWithServerTransport,
};
use errors::try_error;
use rdp::core::event::*;
use rdp::core::global;
use rdp::core::mcs;
use rdp::model::error::{Error as RdpError, RdpError as RdpProtocolError, RdpErrorKind, RdpResult};
use rdpdr::path::UnixPath;
use rdpdr::ServerCreateDriveRequest;
use std::convert::TryFrom;
use std::ffi::{CStr, CString, NulError};
use std::fmt::Debug;
use std::io::Error as IoError;
use std::io::{Cursor, Read, Write};
use std::net::ToSocketAddrs;
use std::os::raw::{c_char, c_int};
use std::os::unix::io::{FromRawFd, RawFd};
use std::{mem, ptr, slice, time};
use thiserror::Error as ThisError;
use tokio::io::AsyncReadExt as _;
use tokio::io::AsyncWriteExt;
use tokio::net::TcpStream;
use tokio::runtime::Runtime;
use tokio_util::codec::Decoder;

#[no_mangle]
pub extern "C" fn init() {
    env_logger::try_init().unwrap_or_else(|e| println!("failed to initialize Rust logger: {e}"));
}

/// todo(isaiah): update this docstring
/// Client has an unusual lifecycle:
/// - connect_rdp creates it on the heap, grabs a raw pointer and returns in to Go
/// - most other exported rdp functions take the raw pointer, convert it to a reference for use
///   without dropping the Client
/// - free_rdp takes the raw pointer and drops it
///
/// All of the exported rdp functions could run concurrently, so the rdp_client is synchronized.
/// tcp_fd is only set in connect_rdp and used as read-only afterwards, so it does not need
/// synchronization.
pub struct Client {
    proxy_tls_conn: TcpStream,
    rdp_conn: TcpStream,
    go_ref: usize,
    tokio_rt: Option<Runtime>,
}

impl Client {
    fn into_raw(self: Box<Self>) -> *mut Self {
        Box::into_raw(self)
    }
    unsafe fn from_ptr<'a>(ptr: *const Self) -> Result<&'a Client, CGOErrCode> {
        match ptr.as_ref() {
            Some(c) => Ok(c),
            None => {
                error!("invalid Rust client pointer");
                Err(CGOErrCode::ErrCodeClientPtr)
            }
        }
    }
    unsafe fn from_raw(ptr: *mut Self) -> Box<Self> {
        Box::from_raw(ptr)
    }
}

#[repr(C)]
pub struct ClientOrError {
    client: *mut Client,
    err: CGOErrCode,
}

impl From<Result<Client>> for ClientOrError {
    fn from(r: Result<Client>) -> ClientOrError {
        match r {
            Ok(client) => ClientOrError {
                client: Box::new(client).into_raw(),
                err: CGOErrCode::ErrCodeSuccess,
            },
            Err(e) => {
                error!("{:?}", e);
                ClientOrError {
                    client: ptr::null_mut(),
                    err: CGOErrCode::ErrCodeFailure,
                }
            }
        }
    }
}

impl From<Client> for ClientOrError {
    fn from(client: Client) -> ClientOrError {
        ClientOrError {
            client: Box::new(client).into_raw(),
            err: CGOErrCode::ErrCodeSuccess,
        }
    }
}

impl From<AnyhowError> for ClientOrError {
    fn from(e: AnyhowError) -> ClientOrError {
        error!("{:?}", e);
        ClientOrError {
            client: ptr::null_mut(),
            err: CGOErrCode::ErrCodeFailure,
        }
    }
}

/// connect_rdp establishes an RDP connection to go_addr with the provided credentials and screen
/// size. If succeeded, the client is internally registered under client_ref. When done with the
/// connection, the caller must call close_rdp.
///
/// # Safety
///
/// The caller mmust ensure that go_addr, go_username, cert_der, key_der point to valid buffers in respect
/// to their corresponding parameters.
#[no_mangle]
pub unsafe extern "C" fn connect_rdp(go_ref: usize, params: CGOConnectParams) -> ClientOrError {
    // Convert from C to Rust types.
    let addr = from_c_string(params.go_addr);
    let username = from_c_string(params.go_username);
    let cert_der = from_go_array(params.cert_der, params.cert_der_len);
    let key_der = from_go_array(params.key_der, params.key_der_len);

    let tokio_rt = Runtime::new().unwrap();

    match tokio_rt.block_on(async {
        connect_rdp_inner(
            go_ref,
            ConnectParams {
                addr,
                username,
                proxy_tls_conn_fd: params.proxy_tls_conn_fd,
                cert_der,
                key_der,
                allow_clipboard: params.allow_clipboard,
                allow_directory_sharing: params.allow_directory_sharing,
                show_desktop_wallpaper: params.show_desktop_wallpaper,
            },
        )
        .await
    }) {
        Ok(mut client) => {
            client.tokio_rt = Some(tokio_rt);
            client.into()
        }
        Err(err) => err.into(),
    }
}

#[derive(Debug, ThisError)]
enum ConnectError {
    #[error("TCP error")]
    Tcp(IoError),
    #[error("RDP error")]
    Rdp(RdpError),
    #[error("invalid address")]
    InvalidAddr,
}

impl From<IoError> for ConnectError {
    fn from(e: IoError) -> ConnectError {
        ConnectError::Tcp(e)
    }
}

impl From<RdpError> for ConnectError {
    fn from(e: RdpError) -> ConnectError {
        ConnectError::Rdp(e)
    }
}

const RDP_CONNECT_TIMEOUT: tokio::time::Duration = tokio::time::Duration::from_secs(10);
const RDP_HANDSHAKE_TIMEOUT: time::Duration = time::Duration::from_secs(10);
const RDPSND_CHANNEL_NAME: &str = "rdpsnd";

#[repr(C)]
pub struct CGOConnectParams {
    go_addr: *const c_char,
    go_username: *const c_char,
    proxy_tls_conn_fd: c_int,
    cert_der_len: u32,
    cert_der: *mut u8,
    key_der_len: u32,
    key_der: *mut u8,
    allow_clipboard: bool,
    allow_directory_sharing: bool,
    show_desktop_wallpaper: bool,
}

struct ConnectParams {
    addr: String,
    username: String,
    proxy_tls_conn_fd: RawFd,
    cert_der: Vec<u8>,
    key_der: Vec<u8>,
    allow_clipboard: bool,
    allow_directory_sharing: bool,
    show_desktop_wallpaper: bool,
}

fn fd_to_stream(fd: RawFd) -> Result<TcpStream> {
    let tcp_stream = unsafe { std::net::TcpStream::from_raw_fd(fd) };
    TcpStream::from_std(tcp_stream).context("could not convert to tokio TcpStream")
}

async fn connect_rdp_inner(go_ref: usize, params: ConnectParams) -> Result<Client> {
    // Convert the proxy TLS connection FD to a stream.
    let mut proxy_tls_conn = fd_to_stream(params.proxy_tls_conn_fd)?;

    debug!("Reading RDCleanPath");
    // Read the RDCleanPath PDU from the client.
    let cleanpath_pdu = read_cleanpath_pdu(&mut proxy_tls_conn)
        .await
        .context("couldn’t read clean cleanpath PDU")?;
    debug!("Read RDCleanPath: {:?}", cleanpath_pdu);

    debug!("Establishing TCP connection to RDP server: {}", params.addr);
    // Connect and authenticate.
    let addr = params
        .addr
        .to_socket_addrs()?
        .next()
        .ok_or(ConnectError::InvalidAddr)?;

    let fut = async move {
        // let socket_addr = resolve_target_to_socket_addr(dest).await?;
        TcpStream::connect(addr)
            .await
            .context("couldn't connect stream")
    };
    let mut rdp_conn = tokio::time::timeout(RDP_CONNECT_TIMEOUT, fut).await??;
    debug!("TCP connection to RDP server established");

    // Send X224 connection request
    debug!("Sending X224 connection request to RDP server");
    let x224_req = cleanpath_pdu
        .x224_connection_pdu
        .context("request is missing X224 connection PDU")
        .map_err(CleanPathError::BadRequest)?;
    rdp_conn.write_all(x224_req.as_bytes()).await?;
    debug!("X224 connection request sent to RDP server");

    // Receive server X224 connection response
    let mut buf = bytes::BytesMut::new();
    let mut decoder = NegotiationWithServerTransport;

    debug!("Receiving X224 response from RDP server");
    // todo(isaiah): check if there is code to be reused from ironrdp code base for this
    let x224_rsp = loop {
        let len = rdp_conn.read_buf(&mut buf).await?;

        if len == 0 {
            if let Some(frame) = decoder.decode_eof(&mut buf)? {
                break frame;
            }
        } else if let Some(frame) = decoder.decode(&mut buf)? {
            break frame;
        }
    };
    debug!("Received X224 response from RDP server");

    // let mut x224_rsp_buf = Vec::new();
    // ironrdp::pdu::PduParsing::to_buffer(&x224_rsp, &mut x224_rsp_buf)
    //     .context("failed to reencode x224 response from server")?;

    // let server_addr = rdp_conn
    //     .peer_addr()
    //     .context("couldn’t get server peer address")?;

    // debug!("Establishing TLS connection with server");

    // let mut rdp_conn = {
    //     // Establish TLS connection with server

    //     let dns_name = server_addr
    //         .host()
    //         .try_into()
    //         .context("Invalid DNS name in selected target")?;

    //     // TODO: optimize client config creation
    //     //
    //     // rustls doc says:
    //     //
    //     // > Making one of these can be expensive, and should be once per process rather than once per connection.
    //     //
    //     // source: https://docs.rs/rustls/latest/rustls/struct.ClientConfig.html
    //     //
    //     // In our case, this doesn’t work, so I’m creating a new ClientConfig from scratch each time (slow).
    //     // rustls issue: https://github.com/rustls/rustls/issues/1186
    //     let tls_client_config = TlsClientConfig::builder()
    //         .with_safe_defaults()
    //         .with_custom_certificate_verifier(std::sync::Arc::new(
    //             crate::utils::danger_transport::NoCertificateVerification,
    //         ))
    //         .with_no_client_auth()
    //         .pipe(Arc::new);

    //     tokio_rustls::TlsConnector::from(tls_client_config)
    //         .connect(dns_name, rdp_conn)
    //         .await
    //         .map_err(CleanPathError::TlsHandshake)?
    // };

    // // https://docs.rs/tokio-rustls/latest/tokio_rustls/#why-do-i-need-to-call-poll_flush
    // rdp_conn.flush().await?;

    Ok(Client {
        proxy_tls_conn,
        rdp_conn,
        go_ref,
        tokio_rt: None,
    })
}

/// From rdp-rs/src/core/client.rs
struct RdpClient<S> {
    mcs: mcs::Client<S>,
    global: global::Client,
    rdpdr: rdpdr::Client,

    cliprdr: Option<cliprdr::Client>,
}

impl<S: Read + Write> RdpClient<S> {
    pub fn read<T>(&mut self, callback: T) -> RdpResult<()>
    where
        T: FnMut(RdpEvent),
    {
        let (channel_name, message) = self.mcs.read()?;
        // De-multiplex static channels. Forward messages to the correct channel client based on
        // name.
        match channel_name.as_str() {
            "global" => self.global.read(message, &mut self.mcs, callback),
            rdpdr::CHANNEL_NAME => {
                let responses = self.rdpdr.read_and_create_reply(message)?;
                let chan = &rdpdr::CHANNEL_NAME.to_string();
                for resp in responses {
                    self.mcs.write(chan, resp)?;
                }
                Ok(())
            }
            cliprdr::CHANNEL_NAME => match self.cliprdr {
                Some(ref mut clip) => clip.read_and_reply(message, &mut self.mcs),
                None => Ok(()),
            },
            RDPSND_CHANNEL_NAME => {
                debug!("skipping RDPSND message, audio output not supported");
                Ok(())
            }
            _ => Err(RdpError::RdpError(RdpProtocolError::new(
                RdpErrorKind::UnexpectedType,
                &format!("Invalid channel name {channel_name:?}"),
            ))),
        }
    }

    pub fn write(&mut self, event: RdpEvent) -> RdpResult<()> {
        match event {
            RdpEvent::Pointer(pointer) => {
                self.global.write_input_event(pointer.into(), &mut self.mcs)
            }
            RdpEvent::Key(key) => self.global.write_input_event(key.into(), &mut self.mcs),
            _ => Err(RdpError::RdpError(RdpProtocolError::new(
                RdpErrorKind::UnexpectedType,
                "RDPCLIENT: This event can't be sent",
            ))),
        }
    }

    fn write_rdpdr(&mut self, messages: Messages) -> RdpResult<()> {
        let chan = &rdpdr::CHANNEL_NAME.to_string();
        for message in messages {
            self.mcs.write(chan, message)?;
        }
        Ok(())
    }

    pub fn handle_client_device_list_announce(
        &mut self,
        req: rdpdr::ClientDeviceListAnnounce,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_client_device_list_announce(req)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_info_response(
        &mut self,
        res: SharedDirectoryInfoResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_info_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_create_response(
        &mut self,
        res: SharedDirectoryCreateResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_create_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_delete_response(
        &mut self,
        res: SharedDirectoryDeleteResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_delete_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_list_response(
        &mut self,
        res: SharedDirectoryListResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_list_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_read_response(
        &mut self,
        res: SharedDirectoryReadResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_read_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_write_response(
        &mut self,
        res: SharedDirectoryWriteResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_write_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn handle_tdp_sd_move_response(
        &mut self,
        res: SharedDirectoryMoveResponse,
    ) -> RdpResult<()> {
        let messages = self.rdpdr.handle_tdp_sd_move_response(res)?;
        self.write_rdpdr(messages)
    }

    pub fn shutdown(&mut self) -> RdpResult<()> {
        self.mcs.shutdown()
    }
}

/// CGOPNG is a CGO-compatible version of PNG that we pass back to Go.
#[repr(C)]
pub struct CGOPNG {
    pub dest_left: u16,
    pub dest_top: u16,
    pub dest_right: u16,
    pub dest_bottom: u16,
    /// The memory of this field is managed by the Rust side.
    pub data_ptr: *mut u8,
    pub data_len: usize,
    pub data_cap: usize,
}

impl TryFrom<BitmapEvent> for CGOPNG {
    type Error = RdpError;

    fn try_from(e: BitmapEvent) -> Result<Self, Self::Error> {
        let mut res = CGOPNG {
            dest_left: e.dest_left,
            dest_top: e.dest_top,
            dest_right: e.dest_right,
            dest_bottom: e.dest_bottom,
            data_ptr: ptr::null_mut(),
            data_len: 0,
            data_cap: 0,
        };

        let w: u16 = e.width;
        let h: u16 = e.height;

        let mut encoded = Vec::with_capacity(8192);
        encode_png(&mut encoded, w, h, e.decompress()?).map_err(|err| {
            Self::Error::TryError(format!("failed to encode bitmap to png: {err:?}"))
        })?;

        res.data_ptr = encoded.as_mut_ptr();
        res.data_len = encoded.len();
        res.data_cap = encoded.capacity();

        // Prevent the data field from being freed while Go handles it.
        // It will be dropped once CGOPNG is dropped (see below).
        mem::forget(encoded);

        Ok(res)
    }
}

/// encodes png from the uncompressed bitmap data
///
/// # Arguments
///
/// * `dest` - buffer that will contain the png data
/// * `width` - width of the png
/// * `height` - height of the png
/// * `data` - buffer that contains uncompressed bitmap data
pub fn encode_png(
    dest: &mut Vec<u8>,
    width: u16,
    height: u16,
    mut data: Vec<u8>,
) -> Result<(), png::EncodingError> {
    convert_bgra_to_rgba(&mut data);

    let mut encoder = png::Encoder::new(dest, width as u32, height as u32);
    encoder.set_compression(png::Compression::Fast);
    encoder.set_color(png::ColorType::Rgba);

    let mut writer = encoder.write_header()?;
    writer.write_image_data(&data)?;
    writer.finish()?;
    Ok(())
}

/// Convert BGRA to RGBA. It's likely due to Windows using uint32 values for
/// pixels (ARGB) and encoding them as big endian. The image.RGBA type uses
/// a byte slice with 4-byte segments representing pixels (RGBA).
///
/// https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpegdi/8ab64b94-59cb-43f4-97ca-79613838e0bd
///
/// Also, always force Alpha value to 100% (opaque). On some Windows
/// versions (e.g. Windows 10) it's sent as 0% after decompression for some reason.
fn convert_bgra_to_rgba(data: &mut [u8]) {
    data.chunks_exact_mut(4).for_each(|chunk| {
        chunk.swap(0, 2);
        // set alpha to 100% opaque
        chunk[3] = 255
    });
}

impl Drop for CGOPNG {
    fn drop(&mut self) {
        // Reconstruct into Vec to drop the allocated buffer.
        unsafe {
            Vec::from_raw_parts(self.data_ptr, self.data_len, self.data_cap);
        }
    }
}

#[cfg(unix)]
fn wait_for_fd(fd: usize) -> RdpResult<()> {
    let fds = &mut libc::pollfd {
        fd: fd as i32,
        events: libc::POLLIN,
        revents: 0,
    };
    loop {
        let res = unsafe { libc::poll(fds, 1, -1) };

        // We only use a single fd and can't timeout, so
        // res will either be 1 for success or -1 for failure.
        if res != 1 {
            let os_err = std::io::Error::last_os_error();
            match os_err.raw_os_error() {
                Some(libc::EINTR) | Some(libc::EAGAIN) => continue,
                _ => return Err(RdpError::Io(os_err)),
            }
        }

        // res == 1
        // POLLIN means that the fd is ready to be read from,
        // POLLHUP means that the other side of the pipe was closed,
        // but we still may have data to read.
        if fds.revents & (libc::POLLIN | libc::POLLHUP) != 0 {
            return Ok(()); // ready for a read
        } else if fds.revents & libc::POLLNVAL != 0 {
            return Err(RdpError::Io(IoError::new(
                std::io::ErrorKind::InvalidInput,
                "invalid fd",
            )));
        } else {
            // fds.revents & libc::POLLERR != 0
            return Err(RdpError::Io(IoError::new(
                std::io::ErrorKind::Other,
                "error on fd",
            )));
        }
    }
}

/// CGOMousePointerEvent is a CGO-compatible version of PointerEvent that we pass back to Go.
/// PointerEvent is a mouse move or click update from the user.
#[repr(C)]
#[derive(Copy, Clone)]
pub struct CGOMousePointerEvent {
    pub x: u16,
    pub y: u16,
    pub button: CGOPointerButton,
    pub down: bool,
    pub wheel: CGOPointerWheel,
    pub wheel_delta: i16,
}

#[repr(C)]
#[derive(Copy, Clone)]
pub enum CGOPointerButton {
    PointerButtonNone,
    PointerButtonLeft,
    PointerButtonRight,
    PointerButtonMiddle,
}

#[repr(C)]
#[derive(Copy, Clone, Debug)]
pub enum CGOPointerWheel {
    PointerWheelNone,
    PointerWheelVertical,
    PointerWheelHorizontal,
}

impl From<CGOMousePointerEvent> for PointerEvent {
    fn from(p: CGOMousePointerEvent) -> PointerEvent {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        PointerEvent {
            x: p.x,
            y: p.y,
            button: match p.button {
                CGOPointerButton::PointerButtonNone => PointerButton::None,
                CGOPointerButton::PointerButtonLeft => PointerButton::Left,
                CGOPointerButton::PointerButtonRight => PointerButton::Right,
                CGOPointerButton::PointerButtonMiddle => PointerButton::Middle,
            },
            down: p.down,
            wheel: match p.wheel {
                CGOPointerWheel::PointerWheelNone => PointerWheel::None,
                CGOPointerWheel::PointerWheelVertical => PointerWheel::Vertical,
                CGOPointerWheel::PointerWheelHorizontal => PointerWheel::Horizontal,
            },
            wheel_delta: p.wheel_delta,
        }
    }
}

/// CGOKeyboardEvent is a CGO-compatible version of KeyboardEvent that we pass back to Go.
/// KeyboardEvent is a keyboard update from the user.
#[repr(C)]
#[derive(Copy, Clone)]
pub struct CGOKeyboardEvent {
    // Note: there's only one key code sent at a time. A key combo is sent as a sequence of
    // KeyboardEvent messages, one key at a time in the "down" state. The RDP server takes care of
    // interpreting those.
    pub code: u16,
    pub down: bool,
}

impl From<CGOKeyboardEvent> for KeyboardEvent {
    fn from(k: CGOKeyboardEvent) -> KeyboardEvent {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        KeyboardEvent {
            code: k.code,
            down: k.down,
        }
    }
}

#[repr(C)]
pub enum CGODisconnectCode {
    /// DisconnectCodeUnknown is for when we can't determine whether
    /// a disconnect was caused by the RDP client or server.
    DisconnectCodeUnknown = 0,
    /// DisconnectCodeClient is for when the RDP client initiated a disconnect.
    DisconnectCodeClient = 1,
    /// DisconnectCodeServer is for when the RDP server initiated a disconnect.
    DisconnectCodeServer = 2,
}

struct ReadRdpOutputReturns {
    user_message: String,
    disconnect_code: CGODisconnectCode,
    err_code: CGOErrCode,
}

#[repr(C)]
pub struct CGOReadRdpOutputReturns {
    user_message: *const c_char,
    disconnect_code: CGODisconnectCode,
    err_code: CGOErrCode,
}

impl From<ReadRdpOutputReturns> for CGOReadRdpOutputReturns {
    fn from(r: ReadRdpOutputReturns) -> CGOReadRdpOutputReturns {
        CGOReadRdpOutputReturns {
            user_message: to_c_string(&r.user_message).unwrap(),
            disconnect_code: r.disconnect_code,
            err_code: r.err_code,
        }
    }
}

/// free_rdp lets the Go side inform us when it's done with Client and it can be dropped.
///
/// # Safety
///
/// client_ptr MUST be a valid pointer.
/// (validity defined by https://doc.rust-lang.org/nightly/core/primitive.pointer.html#method.as_ref-1)
#[no_mangle]
pub unsafe extern "C" fn free_rdp(client_ptr: *mut Client) {
    drop(Client::from_raw(client_ptr))
}

/// # Safety
///
/// s must be a C-style null terminated string.
/// s is cloned here, and the caller is responsible for
/// ensuring its memory is freed.
unsafe fn from_c_string(s: *const c_char) -> String {
    // # Safety
    //
    // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
    // In other words, all pointer data that needs to persist after this function returns MUST
    // be copied into Rust-owned memory.
    CStr::from_ptr(s).to_string_lossy().into_owned()
}

/// # Safety
///
/// See https://doc.rust-lang.org/std/slice/fn.from_raw_parts_mut.html
unsafe fn from_go_array<T: Clone>(data: *mut T, len: u32) -> Vec<T> {
    // # Safety
    //
    // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
    // In other words, all pointer data that needs to persist after this function returns MUST
    // be copied into Rust-owned memory.
    slice::from_raw_parts(data, len as usize).to_vec()
}

/// to_c_string can be used to return string values over the Go boundary.
/// To avoid memory leaks, the Go function must call free_go_string once
/// it's done with the memory.
///
/// See https://doc.rust-lang.org/std/ffi/struct.CString.html#method.into_raw
fn to_c_string(s: &str) -> Result<*const c_char, NulError> {
    let c_string = CString::new(s)?;
    Ok(c_string.into_raw())
}

/// See the docstring for to_c_string.
///
/// # Safety
///
/// s must be a pointer originally created by to_c_string
#[no_mangle]
pub unsafe extern "C" fn free_c_string(s: *mut c_char) {
    // retake pointer to free memory
    let _ = CString::from_raw(s);
}

#[repr(C)]
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum CGOErrCode {
    ErrCodeSuccess = 0,
    ErrCodeFailure = 1,
    ErrCodeClientPtr = 2,
}

#[repr(C)]
pub struct CGOSharedDirectoryAnnounce {
    pub directory_id: u32,
    pub name: *const c_char,
}

/// SharedDirectoryAnnounce is sent by the TDP client to the server
/// to announce a new directory to be shared over TDP.
pub struct SharedDirectoryAnnounce {
    directory_id: u32,
    name: String,
}

impl From<CGOSharedDirectoryAnnounce> for SharedDirectoryAnnounce {
    fn from(cgo: CGOSharedDirectoryAnnounce) -> SharedDirectoryAnnounce {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        unsafe {
            SharedDirectoryAnnounce {
                directory_id: cgo.directory_id,
                name: from_c_string(cgo.name),
            }
        }
    }
}

/// SharedDirectoryAcknowledge is sent by the TDP server to the client
/// to acknowledge that a SharedDirectoryAnnounce was received.
#[derive(Debug)]
#[repr(C)]
pub struct SharedDirectoryAcknowledge {
    pub err_code: TdpErrCode,
    pub directory_id: u32,
}

pub type CGOSharedDirectoryAcknowledge = SharedDirectoryAcknowledge;

/// SharedDirectoryInfoRequest is sent from the TDP server to the client
/// to request information about a file or directory at a given path.
#[derive(Debug)]
pub struct SharedDirectoryInfoRequest {
    completion_id: u32,
    directory_id: u32,
    path: UnixPath,
}

#[repr(C)]
pub struct CGOSharedDirectoryInfoRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub path: *const c_char,
}

impl From<ServerCreateDriveRequest> for SharedDirectoryInfoRequest {
    fn from(req: ServerCreateDriveRequest) -> SharedDirectoryInfoRequest {
        SharedDirectoryInfoRequest {
            completion_id: req.device_io_request.completion_id,
            directory_id: req.device_io_request.device_id,
            path: UnixPath::from(&req.path),
        }
    }
}

/// SharedDirectoryInfoResponse is sent by the TDP client to the server
/// in response to a `Shared Directory Info Request`.
#[derive(Debug)]
pub struct SharedDirectoryInfoResponse {
    completion_id: u32,
    err_code: TdpErrCode,

    fso: FileSystemObject,
}

#[repr(C)]
pub struct CGOSharedDirectoryInfoResponse {
    pub completion_id: u32,
    pub err_code: TdpErrCode,
    pub fso: CGOFileSystemObject,
}

impl From<CGOSharedDirectoryInfoResponse> for SharedDirectoryInfoResponse {
    fn from(cgo_res: CGOSharedDirectoryInfoResponse) -> SharedDirectoryInfoResponse {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        SharedDirectoryInfoResponse {
            completion_id: cgo_res.completion_id,
            err_code: cgo_res.err_code,
            fso: FileSystemObject::from(cgo_res.fso),
        }
    }
}

#[derive(Debug, Clone)]
/// FileSystemObject is a TDP structure containing the metadata
/// of a file or directory.
pub struct FileSystemObject {
    last_modified: u64,
    size: u64,
    file_type: FileType,
    is_empty: u8,
    path: UnixPath,
}

impl FileSystemObject {
    fn name(&self) -> RdpResult<String> {
        if let Some(name) = self.path.last() {
            Ok(name.to_string())
        } else {
            Err(try_error(&format!(
                "failed to extract name from path: {:?}",
                self.path
            )))
        }
    }
}

#[repr(C)]
#[derive(Clone)]
pub struct CGOFileSystemObject {
    pub last_modified: u64,
    pub size: u64,
    pub file_type: FileType,
    pub is_empty: u8,
    pub path: *const c_char,
}

impl From<CGOFileSystemObject> for FileSystemObject {
    fn from(cgo_fso: CGOFileSystemObject) -> FileSystemObject {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        unsafe {
            FileSystemObject {
                last_modified: cgo_fso.last_modified,
                size: cgo_fso.size,
                file_type: cgo_fso.file_type,
                is_empty: cgo_fso.is_empty,
                path: UnixPath::from(from_c_string(cgo_fso.path)),
            }
        }
    }
}

#[repr(C)]
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum FileType {
    File = 0,
    Directory = 1,
}

#[repr(C)]
#[derive(Copy, Clone, PartialEq, Eq, Debug)]
pub enum TdpErrCode {
    /// nil (no error, operation succeeded)
    Nil = 0,
    /// operation failed
    Failed = 1,
    /// resource does not exist
    DoesNotExist = 2,
    /// resource already exists
    AlreadyExists = 3,
}

/// SharedDirectoryWriteRequest is sent by the TDP server to the client
/// to write to a file.
#[derive(Clone)]
pub struct SharedDirectoryWriteRequest {
    completion_id: u32,
    directory_id: u32,
    offset: u64,
    path: UnixPath,
    write_data: Vec<u8>,
}

impl std::fmt::Debug for SharedDirectoryWriteRequest {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SharedDirectoryWriteRequest")
            .field("completion_id", &self.completion_id)
            .field("directory_id", &self.directory_id)
            .field("offset", &self.offset)
            .field("path", &self.path)
            .field("write_data", &util::vec_u8_debug(&self.write_data))
            .finish()
    }
}

#[derive(Debug)]
#[repr(C)]
pub struct CGOSharedDirectoryWriteRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub offset: u64,
    pub path_length: u32,
    pub path: *const c_char,
    pub write_data_length: u32,
    pub write_data: *mut u8,
}

/// SharedDirectoryReadRequest is sent by the TDP server to the client
/// to request the contents of a file.
#[derive(Debug)]
pub struct SharedDirectoryReadRequest {
    completion_id: u32,
    directory_id: u32,
    path: UnixPath,
    offset: u64,
    length: u32,
}

#[repr(C)]
pub struct CGOSharedDirectoryReadRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub path_length: u32,
    pub path: *const c_char,
    pub offset: u64,
    pub length: u32,
}

/// SharedDirectoryReadResponse is sent by the TDP client to the server
/// with the data as requested by a SharedDirectoryReadRequest.
#[repr(C)]
pub struct SharedDirectoryReadResponse {
    pub completion_id: u32,
    pub err_code: TdpErrCode,
    pub read_data: Vec<u8>,
}

impl std::fmt::Debug for SharedDirectoryReadResponse {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SharedDirectoryReadResponse")
            .field("completion_id", &self.completion_id)
            .field("err_code", &self.err_code)
            .field("read_data", &util::vec_u8_debug(&self.read_data))
            .finish()
    }
}

impl From<CGOSharedDirectoryReadResponse> for SharedDirectoryReadResponse {
    fn from(cgo_response: CGOSharedDirectoryReadResponse) -> SharedDirectoryReadResponse {
        unsafe {
            SharedDirectoryReadResponse {
                completion_id: cgo_response.completion_id,
                err_code: cgo_response.err_code,
                read_data: from_go_array(cgo_response.read_data, cgo_response.read_data_length),
            }
        }
    }
}

#[derive(Debug)]
#[repr(C)]
pub struct CGOSharedDirectoryReadResponse {
    pub completion_id: u32,
    pub err_code: TdpErrCode,
    pub read_data_length: u32,
    pub read_data: *mut u8,
}

/// SharedDirectoryWriteResponse is sent by the TDP client to the server
/// to acknowledge the completion of a SharedDirectoryWriteRequest.
#[derive(Debug)]
#[repr(C)]
pub struct SharedDirectoryWriteResponse {
    pub completion_id: u32,
    pub err_code: TdpErrCode,
    pub bytes_written: u32,
}

pub type CGOSharedDirectoryWriteResponse = SharedDirectoryWriteResponse;

/// SharedDirectoryCreateRequest is sent by the TDP server to
/// the client to request the creation of a new file or directory.
#[derive(Debug)]
pub struct SharedDirectoryCreateRequest {
    completion_id: u32,
    directory_id: u32,
    file_type: FileType,
    path: UnixPath,
}

#[repr(C)]
pub struct CGOSharedDirectoryCreateRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub file_type: FileType,
    pub path: *const c_char,
}

/// SharedDirectoryListResponse is sent by the TDP client to the server
/// in response to a SharedDirectoryInfoRequest.
#[derive(Debug)]
pub struct SharedDirectoryListResponse {
    completion_id: u32,
    err_code: TdpErrCode,
    fso_list: Vec<FileSystemObject>,
}

impl From<CGOSharedDirectoryListResponse> for SharedDirectoryListResponse {
    fn from(cgo: CGOSharedDirectoryListResponse) -> SharedDirectoryListResponse {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        unsafe {
            let cgo_fso_list = from_go_array(cgo.fso_list, cgo.fso_list_length);
            let mut fso_list = vec![];
            for cgo_fso in cgo_fso_list.into_iter() {
                fso_list.push(FileSystemObject::from(cgo_fso));
            }

            SharedDirectoryListResponse {
                completion_id: cgo.completion_id,
                err_code: cgo.err_code,
                fso_list,
            }
        }
    }
}

#[repr(C)]
pub struct CGOSharedDirectoryListResponse {
    completion_id: u32,
    err_code: TdpErrCode,
    fso_list_length: u32,
    fso_list: *mut CGOFileSystemObject,
}

/// SharedDirectoryMoveRequest is sent from the TDP server to the client
/// to request a file at original_path be moved to new_path.
#[derive(Debug)]
pub struct SharedDirectoryMoveRequest {
    completion_id: u32,
    directory_id: u32,
    original_path: UnixPath,
    new_path: UnixPath,
}

#[repr(C)]
pub struct CGOSharedDirectoryMoveRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub original_path: *const c_char,
    pub new_path: *const c_char,
}

/// SharedDirectoryCreateResponse is sent by the TDP client to the server
/// to acknowledge a SharedDirectoryCreateRequest was received and executed.
#[derive(Debug)]
pub struct SharedDirectoryCreateResponse {
    completion_id: u32,
    err_code: TdpErrCode,
    fso: FileSystemObject,
}

#[repr(C)]
pub struct CGOSharedDirectoryCreateResponse {
    pub completion_id: u32,
    pub err_code: TdpErrCode,
    pub fso: CGOFileSystemObject,
}

impl From<CGOSharedDirectoryCreateResponse> for SharedDirectoryCreateResponse {
    fn from(cgo_res: CGOSharedDirectoryCreateResponse) -> SharedDirectoryCreateResponse {
        // # Safety
        //
        // This function MUST NOT hang on to any of the pointers passed in to it after it returns.
        // In other words, all pointer data that needs to persist after this function returns MUST
        // be copied into Rust-owned memory.
        SharedDirectoryCreateResponse {
            completion_id: cgo_res.completion_id,
            err_code: cgo_res.err_code,
            fso: FileSystemObject::from(cgo_res.fso),
        }
    }
}

/// SharedDirectoryDeleteRequest is sent by the TDP server to the client
/// to request the deletion of a file or directory at path.
#[derive(Debug)]
pub struct SharedDirectoryDeleteRequest {
    completion_id: u32,
    directory_id: u32,
    path: UnixPath,
}

#[repr(C)]
pub struct CGOSharedDirectoryDeleteRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub path: *const c_char,
}

/// SharedDirectoryDeleteResponse is sent by the TDP client to the server
/// to acknowledge a SharedDirectoryDeleteRequest was received and executed.
#[derive(Debug)]
#[repr(C)]
pub struct SharedDirectoryDeleteResponse {
    completion_id: u32,
    err_code: TdpErrCode,
}

pub type CGOSharedDirectoryDeleteResponse = SharedDirectoryDeleteResponse;

/// SharedDirectoryMoveResponse is sent by the TDP client to the server
/// to acknowledge a SharedDirectoryMoveRequest was received and expected.
#[derive(Debug)]
#[repr(C)]
pub struct SharedDirectoryMoveResponse {
    completion_id: u32,
    err_code: TdpErrCode,
}

pub type CGOSharedDirectoryMoveResponse = SharedDirectoryMoveResponse;

/// SharedDirectoryListRequest is sent by the TDP server to the client
/// to request the contents of a directory.
#[derive(Debug)]
pub struct SharedDirectoryListRequest {
    completion_id: u32,
    directory_id: u32,
    path: UnixPath,
}

#[repr(C)]
pub struct CGOSharedDirectoryListRequest {
    pub completion_id: u32,
    pub directory_id: u32,
    pub path: *const c_char,
}

// These functions are defined on the Go side. Look for functions with '//export funcname'
// comments.
extern "C" {
    // todo(isaiah)
}

/// Payload represents raw incoming RDP messages for parsing.
pub(crate) type Payload = Cursor<Vec<u8>>;
/// Message represents a raw outgoing RDP message to send to the RDP server.
pub(crate) type Message = Vec<u8>;
pub(crate) type Messages = Vec<Message>;

/// Encode is an object that can be encoded for sending to the RDP server.
pub(crate) trait Encode: std::fmt::Debug {
    fn encode(&self) -> RdpResult<Message>;
}

/// This is the maximum size of an RDP message which we will accept
/// over a virtual channel.
///
/// Note that this is not an RDP defined value, but rather one we've chosen
/// in order to harden system security.
const MAX_ALLOWED_VCHAN_MSG_SIZE: usize = 2 * 1024 * 1024; // 2MB
