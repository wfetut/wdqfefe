// Copyright 2022 Gravitational, Inc
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

use crate::errors::invalid_data_error;
use crate::vchan::ChannelPDUFlags;
use crate::{vchan, Payload};
use bitflags::bitflags;
use byteorder::{LittleEndian, ReadBytesExt, WriteBytesExt};
use num_traits::FromPrimitive;
use rdp::core::{mcs, tpkt};
use rdp::model::error::*;
use rdp::try_let;
use std::collections::HashMap;
use std::io::{Cursor, Read, Seek, SeekFrom, Write};

pub const CHANNEL_NAME: &str = "cliprdr";

struct PendingData {
    data: Vec<u8>,
    total_length: u32,
    clipboard_header: Option<ClipboardPDUHeader>,
}

impl PendingData {
    fn reset(&mut self, length: u32) {
        self.data.clear();
        self.total_length = length;
        self.clipboard_header = None;
    }
}

bitflags! {
    /// see 2.2.5.2.3.1 File Descriptor (CLIPRDR_FILEDESCRIPTOR)
    struct FileDescriptorFlags: u32 {
        /// The fileAttributes field contains valid data.
        const FD_ATTRIBUTES = 0x00000004;

        /// The fileSizeHigh and fileSizeLow fields contain valid data.
        const FD_FILESIZE = 0x00000040;

        /// The lastWriteTime field contains valid data.
        const FD_WRITESTIME = 0x00000020;

        /// A progress indicator SHOULD be shown when copying the file.
        const FD_SHOWPROGRESSUI = 0x00004000;
    }
}

bitflags! {
    /// see 2.2.5.2.3.1 File Descriptor (CLIPRDR_FILEDESCRIPTOR)
    struct FileAttributesFlags: u32 {
        /// A file that is read-only. Applications can read the file, but cannot write to
        /// it or delete it.
        const FILE_ATTRIBUTE_READONLY = 0x00000001;
        /// The file or directory is hidden. It is not included in an ordinary directory
        /// listing.
        const FILE_ATTRIBUTE_HIDDEN = 0x00000002;
        /// A file or directory that the operating system uses a part of, or uses
        /// exclusively.
        const FILE_ATTRIBUTE_SYSTEM = 0x00000004;
        /// Identifies a directory.
        const FILE_ATTRIBUTE_DIRECTORY = 0x00000010;
        /// A file or directory that is an archive file or directory. Applications typically
        /// use this attribute to mark files for backup or removal.
        const FILE_ATTRIBUTE_ARCHIVE = 0x00000020;
        /// A file that does not have other attributes set. This attribute is valid only
        /// when used alone.
        const FILE_ATTRIBUTE_NORMAL = 0x00000080;
    }
}

/// see 2.2.5.2.3.1 File Descriptor (CLIPRDR_FILEDESCRIPTOR)
#[derive(Debug)]
struct FileDescriptor {
    ///  An unsigned 32-bit integer that specifies which fields contain valid data and the
    /// usage of progress UI during a copy operation.
    flags: FileDescriptorFlags,
    /// An unsigned 32-bit integer that specifies file attribute flags.
    file_attributes: FileAttributesFlags,
    /// An unsigned 64-bit integer that specifies the number of 100-nanoseconds
    /// intervals that have elapsed since 1 January 1601 to the time of the last write operation on the file.
    last_write_time: u64,
    /// the file size in bytes
    file_size: u64,
    /// the name of the file
    file_name: String,
}

/// FileListManager manages the global state necessary to handle
/// transferring files via the clipboard channel.
#[derive(Debug)]
struct FileListManager {
    // is_expecting_file_list is set to true when we receive a Format List PDU (CB_FORMAT_LIST,
    // handled by handle_format_list) with format name == CLIPBOARD_FORMAT_NAME_FILE_LIST (meaning
    // a file or list of files was cut/copied on the remote Windows machine), and set to false when
    // we receive another supported type of Format List PDU (meaning text was cut/copied on the
    // remote Windows machine).
    //
    // In either case, we immediately send a Format Data Request PDU (CB_FORMAT_DATA_REQUEST), which
    // is responded to with a Format Data Response PDU (CB_FORMAT_DATA_RESPONSE),
    // which is handled by handle_format_data_response, which uses the value of is_expecting_file_list
    // to decide whether to try to parse a file list, or just send the client the copied text.
    is_expecting_file_list: bool,
    file_list: Vec<FileDescriptor>,
}

/// Client implements a client for the clipboard virtual channel
/// (CLIPRDR) extension, as defined in:
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpeclip/fb9b7e0b-6db4-41c2-b83c-f889c1ee7688
pub struct Client {
    clipboard: HashMap<u32, Vec<u8>>,
    pending: PendingData,
    on_remote_copy: Box<dyn Fn(Vec<u8>)>,
    file_list_manager: FileListManager,
}

impl Default for Client {
    fn default() -> Self {
        Self::new(Box::new(|_| {}))
    }
}

impl Client {
    pub fn new(on_remote_copy: Box<dyn Fn(Vec<u8>)>) -> Self {
        Client {
            clipboard: HashMap::new(),
            pending: PendingData {
                data: Vec::new(),
                total_length: 0,
                clipboard_header: None,
            },
            on_remote_copy,
            file_list_manager: FileListManager {
                is_expecting_file_list: false,
                file_list: Vec::new(),
            },
        }
    }

    pub fn read<S: Read + Write>(
        &mut self,
        payload: tpkt::Payload,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        let mut payload = try_let!(tpkt::Payload::Raw, payload)?;
        debug!("received payload: {:?}", payload); // TODO(isaiah): remove this
        let pdu_header = vchan::ChannelPDUHeader::decode(&mut payload)?;

        // TODO(zmb3): this logic is the same for all virtual channels, and should
        // be moved to vchan.rs and reused for the rdpdr client as well
        if pdu_header
            .flags
            .contains(ChannelPDUFlags::CHANNEL_FLAG_FIRST)
        {
            self.pending.reset(pdu_header.length);
            self.pending.clipboard_header = Some(ClipboardPDUHeader::decode(&mut payload)?);
        }

        payload.read_to_end(&mut self.pending.data)?;

        if pdu_header
            .flags
            .contains(ChannelPDUFlags::CHANNEL_FLAG_LAST)
            && self.pending.clipboard_header.is_some()
        {
            let full_msg = self.pending.data.split_off(0);
            let mut payload = Cursor::new(full_msg);
            let header = self.pending.clipboard_header.take().unwrap();
            return self.handle_message(header, &mut payload, mcs);
        }

        Ok(())
    }

    fn handle_message<S: Read + Write>(
        &mut self,
        header: ClipboardPDUHeader,
        payload: &mut Payload,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received {:?}", header.msg_type);

        let responses = match header.msg_type {
            ClipboardPDUType::CB_CLIP_CAPS => self.handle_server_caps(payload)?,
            ClipboardPDUType::CB_MONITOR_READY => self.handle_monitor_ready(payload)?,
            ClipboardPDUType::CB_FORMAT_LIST => {
                self.handle_format_list(payload, header.data_len)?
            }
            ClipboardPDUType::CB_FORMAT_LIST_RESPONSE => {
                self.handle_format_list_response(header.msg_flags)?
            }
            ClipboardPDUType::CB_FORMAT_DATA_REQUEST => self.handle_format_data_request(payload)?,
            ClipboardPDUType::CB_FORMAT_DATA_RESPONSE => {
                if header
                    .msg_flags
                    .contains(ClipboardHeaderFlags::CB_RESPONSE_OK)
                {
                    self.handle_format_data_response(payload, header.data_len)?
                } else {
                    warn!("RDP server failed to process format data request");
                    vec![]
                }
            }
            _ => {
                warn!(
                    "CLIPRDR message {:?} not implemented, ignoring",
                    header.msg_type
                );
                vec![]
            }
        };

        let chan = &CHANNEL_NAME.to_string();
        for resp in responses {
            mcs.write(chan, resp)?;
        }

        Ok(())
    }

    /// update_clipboard is invoked from Go.
    /// It updates the local clipboard cache and returns the encoded message
    /// that should be sent to the RDP server.
    pub fn update_clipboard(&mut self, data: Vec<u8>) -> RdpResult<Vec<Vec<u8>>> {
        const CR: u8 = 13;
        const LF: u8 = 10;

        // convert LF to CRLF, as required by CF_OEMTEXT
        let len_orig = data.len();
        let mut converted = Vec::with_capacity(len_orig);
        for i in 0..len_orig {
            match data[i] {
                LF => {
                    // convert LF to CRLF, so long as the previous character
                    // wasn't CR (in which case there's no conversion necessary)
                    if i == 0 || (data[i - 1] != CR) {
                        converted.push(CR);
                    }
                    converted.push(LF);
                }
                _ => converted.push(data[i]),
            }
        }
        // Windows requires a null terminator, so add one if necessary
        if !converted.is_empty() && converted[converted.len() - 1] != 0x00 {
            converted.push(0x00);
        }

        self.clipboard
            .insert(ClipboardFormatId::CF_OEMTEXT as u32, converted);

        encode_message(
            ClipboardPDUType::CB_FORMAT_LIST,
            FormatListPDU {
                format_names: vec![LongFormatName::id(ClipboardFormatId::CF_OEMTEXT as u32)],
            }
            .encode()?,
        )
    }

    /// Handles the server capabilities message, which is the first message sent from the server
    /// to the client during the initialization sequence. Described in section 1.3.2.1.
    fn handle_server_caps(&self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let caps = ClipboardCapabilitiesPDU::decode(payload)?;
        if let Some(general) = caps.general {
            // our capabilities are minimal, so we log the server
            // capabilities for debug purposes, but don't otherwise care
            // (the server will be forced into working with us)
            info!("RDP server clipboard capabilities: {:?}", general);
        }

        // we don't send our capabilities here, they get sent as a response
        // to the monitor ready PDU below
        Ok(vec![])
    }

    /// Handles the monitor ready PDU, which is sent from the server to the client during
    /// the initialization phase. Upon receiving this message, the client should respond
    /// with its capabilities, an optional temporary directory PDU, and a format list PDU.
    fn handle_monitor_ready(&self, _payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        // There's nothing additional to decode here, the monitor ready PDU is just a header.
        // In response, we need to:
        // 1. Send our clipboard capabilities
        // 2. Mimic a "copy" operation by sending a format list PDU
        // This completes the initialization process.
        let mut result = encode_message(
            ClipboardPDUType::CB_CLIP_CAPS,
            ClipboardCapabilitiesPDU {
                general: Some(GeneralClipboardCapabilitySet {
                    version: CB_CAPS_VERSION_2,
                    flags: ClipboardGeneralCapabilityFlags::CB_USE_LONG_FORMAT_NAMES
                        | ClipboardGeneralCapabilityFlags::CB_STREAM_FILECLIP_ENABLED,
                }),
            }
            .encode()?,
        )?;
        result.extend(encode_message(
            ClipboardPDUType::CB_FORMAT_LIST,
            FormatListPDU::<LongFormatName> {
                format_names: vec![LongFormatName::id(0)],
            }
            .encode()?,
        )?);

        Ok(result)
    }

    /// Handles the format list PDU, which is a notification from the server
    /// that some data was copied and can be requested at a later date.
    fn handle_format_list(
        &mut self,
        payload: &mut Payload,
        length: u32,
    ) -> RdpResult<Vec<Vec<u8>>> {
        let list = FormatListPDU::<LongFormatName>::decode(payload, length)?;
        debug!(
            "{:?} data was copied on the RDP server",
            list.format_names
                .iter()
                .map(|n| n.format_id)
                .collect::<Vec<u32>>()
        );

        // if we want to support a variety of formats, we should clear
        // and re-initialize some local state (Clipboard Format ID Map)
        //
        // we're only supporting standard (text) formats right now, so
        // we don't need to maintain a local/remote mapping
        //
        // see section 3.1.1.1 for details

        let mut result = encode_message(ClipboardPDUType::CB_FORMAT_LIST_RESPONSE, vec![])?;

        let mut request_data = |format_id: u32| -> RdpResult<()> {
            result.extend(encode_message(
                ClipboardPDUType::CB_FORMAT_DATA_REQUEST,
                FormatDataRequestPDU::for_id(format_id).encode()?,
            )?);

            Ok(())
        };

        for name in list.format_names {
            // TODO(isaiah): this match mess can probably be cleaned up somehow.
            // Check for supported, standard clipboard formats.
            match FromPrimitive::from_u32(name.format_id) {
                // TODO(zmb3): support CF_TEXT, CF_UNICODETEXT, ...
                Some(ClipboardFormatId::CF_OEMTEXT) => {
                    self.file_list_manager.is_expecting_file_list = false;
                    // request the data by imitating a paste event
                    request_data(name.format_id)?;
                }
                _ => match name.format_name {
                    // No supported, standard clipboard format was found,
                    // check for the File List format name.
                    Some(format_name) => match format_name.as_str() {
                        CLIPBOARD_FORMAT_NAME_FILE_LIST => {
                            self.file_list_manager.is_expecting_file_list = true;
                            // Request the File List by sending a Format Data Request
                            // with the system-dependent format id that was sent to us
                            request_data(name.format_id)?;
                        }
                        _ => {
                            info!("detected unsupported format name: {:?}", format_name);
                        }
                    },
                    None => {
                        info!("detected unsupported format id: {:?}", name.format_id);
                    }
                },
            }
        }

        Ok(result)
    }

    /// Handle the format list response, which is the server acknowledging that
    /// it recieved a notification that the client has updated clipboard data
    /// that may be requested in the future.
    fn handle_format_list_response(&self, flags: ClipboardHeaderFlags) -> RdpResult<Vec<Vec<u8>>> {
        if !flags.contains(ClipboardHeaderFlags::CB_RESPONSE_OK) {
            warn!("RDP server did not process our copy operation");
        }
        Ok(vec![])
    }

    /// Handles a request from the RDP server for clipboard data.
    /// This message is received when a user executes a paste in the remote desktop.
    ///
    /// The RDP server on the remote desktop is smart enough to know to only send this
    /// message on a paste if the most recent clipboard action on the remote desktop was
    /// caused by the receipt of a CB_FORMAT_LIST PDU sent by us. IOW, it will only be sent
    /// if the latest cut/copy was done on the client side (and is therefore held by us in
    /// client.clipboard)
    fn handle_format_data_request(&self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let req = FormatDataRequestPDU::decode(payload)?;
        let data = match self.clipboard.get(&req.format_id) {
            Some(d) => d.clone(),
            // TODO(zmb3): send empty FORMAT_DATA_RESPONSE with RESPONSE_FAIL flag set in header
            None => {
                return Err(invalid_data_error(
                    format!(
                        "clipboard does not contain data for format {}",
                        req.format_id
                    )
                    .as_str(),
                ))
            }
        };

        encode_message(
            ClipboardPDUType::CB_FORMAT_DATA_RESPONSE,
            FormatDataResponsePDU { data }.encode()?,
        )
    }

    /// Receives clipboard data from the remote desktop. This is the server responding
    /// to our format data request.
    fn handle_format_data_response(
        &mut self,
        payload: &mut Payload,
        length: u32,
    ) -> RdpResult<Vec<Vec<u8>>> {
        let resp = FormatDataResponsePDU::decode(payload, length)?;
        let data_len = resp.data.len();
        debug!(
            "recieved {} bytes of copied data from Windows Desktop: {:?}", // TODO(isaiah): remove the full data print out
            data_len, resp.data
        );

        let mut text_for_client_clipboard = if self.file_list_manager.is_expecting_file_list {
            // TODO(isaiah): write a function that parses file list and returns the [first] file name,
            // and updates Client.
            self.handle_file_list(resp)?
        } else {
            resp.data
        };

        // trim the null-terminator, if it exists
        // (but don't worry about CRLF conversion, most non-Windows systems can handle CRLF well enough)
        if let Some(0x00) = text_for_client_clipboard.last() {
            text_for_client_clipboard.truncate(data_len - 1);
        }

        (self.on_remote_copy)(text_for_client_clipboard);

        Ok(vec![])
    }

    /// see 2.2.5.2.3.1 File Descriptor (CLIPRDR_FILEDESCRIPTOR)
    fn handle_file_list(&mut self, data: FormatDataResponsePDU) -> RdpResult<Vec<u8>> {
        let mut data = Cursor::new(data.data);
        let file_list_len = data.read_u32::<LittleEndian>()?;

        for _ in 0..file_list_len {
            let orig_pos = data.position();

            // We use from_bits_truncate here rather than from_bits, because emperically the server
            // can send back values here with bits not prescribed by the FileDescriptorFlags spec.
            let flags = FileDescriptorFlags::from_bits_truncate(data.read_u32::<LittleEndian>()?);

            data.seek(SeekFrom::Current(32))?; // reserved1 (32 bytes)

            // Using from_bits_truncate here as well out of an abundance of caution.
            let file_attributes =
                FileAttributesFlags::from_bits_truncate(data.read_u32::<LittleEndian>()?);

            data.seek(SeekFrom::Current(16))?; // reserved2 (16 bytes)

            let last_write_time = data.read_u64::<LittleEndian>()?;

            // An unsigned 32-bit integer that contains the most significant 4 bytes of the file size.
            let file_size_high = data.read_u32::<LittleEndian>()?;
            // An unsigned 32-bit integer that contains the least significant 4 bytes of the file size.
            let file_size_low = data.read_u32::<LittleEndian>()?;
            // (Why would RDP do this to us? Just make it a little endian u64 instead!)
            let file_size = (u64::from(file_size_high) << 32) + u64::from(file_size_low);

            // A null-terminated 260 character Unicode string that contains the name of the file.
            // read_unicode_to_string will return upon finding the null terminator, so won't
            // necessarily eat all 260 bytes.
            let file_name = read_unicode_to_string(&mut data);
            debug!("file_name: {:?}", file_name);

            self.file_list_manager.file_list.push(FileDescriptor {
                flags,
                file_attributes,
                last_write_time,
                file_size,
                file_name,
            });

            // Ensure we eat the entire file descriptor
            data.set_position(orig_pos + 592);
        }

        debug!("file list updated: {:?}", self.file_list_manager.file_list);

        Ok(vec![])
    }
}

bitflags! {
    struct ClipboardHeaderFlags: u16 {
        /// Indicates that the assocated request was processed successfully.
        const CB_RESPONSE_OK = 0x0001;

        /// Indicates that the associated request was not procesed successfully.
        const CB_RESPONSE_FAIL = 0x0002;

        /// Used by the short format name variant to indicate that the format
        /// names are in ASCII 8.
        const CB_ASCII_NAMES = 0x0004;
    }
}

/// This header (CLIPRDR_HEADER) is present in all clipboard PDUs.
#[derive(Debug, PartialEq, Eq)]
struct ClipboardPDUHeader {
    /// Specifies the type of clipboard PDU that follows the dataLen field.
    msg_type: ClipboardPDUType,
    msg_flags: ClipboardHeaderFlags,
    /// Specifies the size, in bytes, of the data which follows this header.
    data_len: u32,
}

impl ClipboardPDUHeader {
    fn new(msg_type: ClipboardPDUType, msg_flags: ClipboardHeaderFlags, data_len: u32) -> Self {
        ClipboardPDUHeader {
            msg_type,
            msg_flags,
            data_len,
        }
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let typ = payload.read_u16::<LittleEndian>()?;
        Ok(Self {
            msg_type: ClipboardPDUType::from_u16(typ)
                .ok_or_else(|| invalid_data_error(&format!("invalid message type {:#04x}", typ)))?,
            msg_flags: ClipboardHeaderFlags::from_bits(payload.read_u16::<LittleEndian>()?)
                .ok_or_else(|| invalid_data_error("invalid flags in clipboard header"))?,
            data_len: payload.read_u32::<LittleEndian>()?,
        })
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u16::<LittleEndian>(self.msg_type as u16)?;
        w.write_u16::<LittleEndian>(self.msg_flags.bits())?;
        w.write_u32::<LittleEndian>(self.data_len)?;
        Ok(w)
    }
}
#[derive(Clone, Copy, Debug, Eq, PartialEq, FromPrimitive, ToPrimitive)]
#[allow(non_camel_case_types)]
enum ClipboardPDUType {
    CB_MONITOR_READY = 0x0001,
    CB_FORMAT_LIST = 0x0002,
    CB_FORMAT_LIST_RESPONSE = 0x0003,
    CB_FORMAT_DATA_REQUEST = 0x0004,
    CB_FORMAT_DATA_RESPONSE = 0x0005,
    CB_TEMP_DIRECTORY = 0x0006,
    CB_CLIP_CAPS = 0x0007,
    CB_FILECONTENTS_REQUEST = 0x0008,
    CB_FILECONTENTS_RESPONSE = 0x0009,
    CB_LOCK_CLIPDATA = 0x000A,
    CB_UNLOCK_CLIPDATA = 0x000B,
}

/// An optional PDU (CLIPRDR_CAPS) used to exchange capability information.
/// If this PDU is not sent, it is assumed that the endpoint which did not
/// send capabilities is using the default values for each field.
#[derive(Debug)]
struct ClipboardCapabilitiesPDU {
    // The protocol is written in such a way that there can be
    // a variable number of capability sets in this PDU. However,
    // the spec only defines one type of capability set (general),
    // so we'll just use an Option.
    general: Option<GeneralClipboardCapabilitySet>,
}

const CB_CAPS_VERSION_2: u32 = 0x0002;

impl ClipboardCapabilitiesPDU {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        // there's either 0 or 1 capability sets included here
        w.write_u16::<LittleEndian>(self.general.is_some() as u16)?;
        w.write_u16::<LittleEndian>(0)?; // pad

        if let Some(set) = &self.general {
            w.write_u16::<LittleEndian>(ClipboardCapabilitySetType::General as u16)?;
            w.write_u16::<LittleEndian>(12)?; // length
            w.write_u32::<LittleEndian>(CB_CAPS_VERSION_2)?;
            w.write_u32::<LittleEndian>(set.flags.bits)?;
        }

        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let count = payload.read_u16::<LittleEndian>()?;
        payload.read_u16::<LittleEndian>()?; // pad

        match count {
            0 => Ok(Self { general: None }),
            1 => Ok(Self {
                general: Some(GeneralClipboardCapabilitySet::decode(payload)?),
            }),
            _ => Err(invalid_data_error("expected 0 or 1 capabilities")),
        }
    }
}

impl GeneralClipboardCapabilitySet {
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let set_type = payload.read_u16::<LittleEndian>()?;
        if set_type != ClipboardCapabilitySetType::General as u16 {
            return Err(invalid_data_error(&format!(
                "expected general capability set (1), got {}",
                set_type
            )));
        }

        let length = payload.read_u16::<LittleEndian>()?;
        if length != 12u16 {
            return Err(invalid_data_error(
                "expected 12 bytes for the general capability set",
            ));
        }

        Ok(Self {
            version: payload.read_u32::<LittleEndian>()?,
            flags: ClipboardGeneralCapabilityFlags::from_bits(payload.read_u32::<LittleEndian>()?)
                .ok_or_else(|| invalid_data_error("invalid flags in general capability set"))?,
        })
    }
}

enum ClipboardCapabilitySetType {
    General = 0x0001,
}

/// The general capability set (CLIPRDR_GENERAL_CAPABILITY) is used to
/// advertise general clipboard settings.
#[derive(Debug)]
#[allow(dead_code)]
struct GeneralClipboardCapabilitySet {
    /// Specifies the RDP Clipboard Virtual Extension version number.
    /// Used for informational purposes only, and MUST NOT be used to
    /// make protocol capability decisions.
    version: u32,
    flags: ClipboardGeneralCapabilityFlags,
}

bitflags! {
    struct ClipboardGeneralCapabilityFlags: u32 {
        /// Indicates that long format names will be used in the format list PDU.
        /// If this flag is not set, then the short format names MUST be used.
        const CB_USE_LONG_FORMAT_NAMES = 0x0002;

        /// File copy and paste using stream-based operations are supported.
        const CB_STREAM_FILECLIP_ENABLED = 0x0004;

        /// Indicates that any description of files to copy and paste MUST NOT
        /// include the source path of the files.
        const CB_FILECLIP_NO_FILE_PATHS = 0x0008;

        /// Indicates that locking and unlocking of file stream data
        /// on the clipboard is supported.
        const CB_CAN_LOCK_CLIPDATA = 0x0010;

        /// Indicates support for transferring files greater than 4GB.
        const CB_HUGE_FILE_SUPPORT_ENABLED = 0x0020;
    }
}

/// The format list PDU is sent by either the client or server when
/// its local system clipboard is updated with new clipboard data.
///
/// It contains 0 or more format names, which are either all short
/// format or all long format depending on the server/client capabilities.
#[derive(Debug)]
struct FormatListPDU<T: FormatName> {
    format_names: Vec<T>,
}

trait FormatName: Sized {
    fn encode(&self) -> RdpResult<Vec<u8>>;
    fn decode(payload: &mut Payload) -> RdpResult<Self>;
}

impl<T: FormatName> FormatListPDU<T> {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = Vec::new();
        for name in &self.format_names {
            w.extend(name.encode()?);
        }

        Ok(w)
    }

    fn decode(payload: &mut Payload, length: u32) -> RdpResult<Self> {
        let mut format_names: Vec<T> = Vec::new();

        let startpos = payload.position();
        while payload.position() - startpos < length as u64 {
            format_names.push(T::decode(payload)?);
        }

        Ok(Self { format_names })
    }
}

/// Represents the CLIPRDR_SHORT_FORMAT_NAME structure.
#[derive(Debug)]
struct ShortFormatName {
    format_id: u32,
    format_name: [u8; 32],
}

#[allow(dead_code)]
impl ShortFormatName {
    fn id(id: u32) -> Self {
        Self {
            format_id: id,
            format_name: [0u8; 32],
        }
    }

    fn from_str(id: u32, name: &str) -> RdpResult<Self> {
        if name.len() > 32 {
            return Err(invalid_data_error(
                format!("{} is too long for short format name", name).as_str(),
            ));
        }
        let mut dest = [0u8; 32];
        dest[..name.len()].copy_from_slice(name.as_bytes());
        Ok(Self {
            format_id: id,
            format_name: dest,
        })
    }
}

impl FormatName for ShortFormatName {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = Vec::new();
        w.write_u32::<LittleEndian>(self.format_id)?;
        w.write_all(&self.format_name)?;

        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let format_id = payload.read_u32::<LittleEndian>()?;
        let mut format_name = [0u8; 32];
        payload.read_exact(&mut format_name)?;

        Ok(Self {
            format_id,
            format_name,
        })
    }
}

/// Represents the CLIPRDR_LONG_FORMAT_NAMES structure.
#[derive(Debug)]
struct LongFormatName {
    format_id: u32,
    format_name: Option<String>,
}

impl LongFormatName {
    fn id(id: u32) -> Self {
        Self {
            format_id: id,
            format_name: None,
        }
    }
}

impl FormatName for LongFormatName {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = Vec::new();
        w.write_u32::<LittleEndian>(self.format_id)?;
        match &self.format_name {
            // not all clipboard formats have a name; in such cases, the name
            // must be encoded as a single Unicode null character (two zero bytes)
            None => w.write_u16::<LittleEndian>(0)?,
            Some(name) => {
                for c in str::encode_utf16(name) {
                    w.write_u16::<LittleEndian>(c)?;
                }
                w.write_u16::<LittleEndian>(0)?; // terminating null
            }
        };

        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let format_id = payload.read_u32::<LittleEndian>()?;

        let name = read_unicode_to_string(payload);

        Ok(Self {
            format_id,
            format_name: if name.is_empty() { None } else { Some(name) },
        })
    }
}

// read_unicode_to_string causes data to consume and return a null terminated unicode string.
fn read_unicode_to_string(data: &mut Payload) -> String {
    let mut consumed = 0;
    let string = std::char::decode_utf16(
        data.get_ref()
            .chunks_exact(2)
            .skip(data.position() as usize / 2) // skip over previously consumed bytes
            .take_while(|c| {
                consumed += 2;
                !matches!(c, [0x00, 0x00])
            })
            .map(|c| u16::from_le_bytes([c[0], c[1]])),
    )
    .map(|c| c.unwrap_or(std::char::REPLACEMENT_CHARACTER))
    .collect();

    data.set_position(data.position() + consumed);

    string
}

/// All data copied to a system clipboard has to conform to a format
/// specification. These formats are identified by unique numeric IDs,
/// which are OS-specific.
///
/// See section 1.3.1.2.
///
/// Standard clipboard formats are listed here: https://docs.microsoft.com/en-us/windows/win32/dataxchg/standard-clipboard-formats
///
/// Applications can define their own clipboard formats as well.
#[allow(dead_code, non_camel_case_types)]
#[repr(u32)]
#[derive(Clone, Copy, Debug, Eq, PartialEq, FromPrimitive, ToPrimitive)]
enum ClipboardFormatId {
    CF_TEXT = 1,         // CRLF line endings, null-terminated
    CF_BITMAP = 2,       // HBITMAP handle
    CF_METAFILEPICT = 3, // 1.3.1.1.3
    CF_SYLK = 4,         // Microsoft symbolik link format
    CF_DIF = 5,          // Software Arts' Data Interchange Format
    CF_TIFF = 6,         // tagged-image file format
    CF_OEMTEXT = 7,      // OEM charset, CRLF line endings, null-terminated
    CF_DIB = 8,          // BITMAPINFO
    CF_PALETTE = 9,      // 1.3.1.1.2
    CF_PENDATA = 10,     // Microsoft Windows for Pen Computing
    CF_RIFF = 11,        // audio data more complex than CF_WAVE
    CF_WAVE = 12,        // audio data in standard wav format
    CF_UNICODETEXT = 13, // unicode text, lines end with CRLF, null-terminated
    CF_ENHMETAFILE = 14, // handle to an enhanced metafile
    CF_HDROP = 15,       // identifies a list of files
    CF_LOCALE = 16,      // locale identifier, so application can lookup charset when pasting

    CF_PRIVATEFIRST = 0x0200, // range for private clipboard formats
    CF_PRIVATELAST = 0x02FF, // https://docs.microsoft.com/en-us/windows/win32/dataxchg/clipboard-formats#private-clipboard-formats

    CF_GDIOBJFIRST = 0x0300, // range for application-defined GDI object formats
    CF_GDIOBJLAST = 0x03FF, // https://docs.microsoft.com/en-us/windows/win32/dataxchg/clipboard-formats#private-clipboard-formats
}

/// There's no specified unique numeric ID for the File List clipboard format,
/// however within the context of the Remote Desktop Protocol: Clipboard Virtual Channel Extension,
/// the File List format type uses the following hard-coded Clipboard Format name.
///
/// See section 1.3.1.2.
const CLIPBOARD_FORMAT_NAME_FILE_LIST: &str = "FileGroupDescriptorW";

/// Sent as a reply to the format list PDU - used to indicate whether
/// the format list PDU was processed succesfully.
#[derive(Debug)]
struct FormatListResponsePDU {
    // empty, the only information needed is the flags in the header
}

/// Sent by the recipient of the format list PDU  in order to request
/// the data for one of the clipboard formats that was listed in the
/// format list PDU.
///
/// See section 2.2.5.1: CLIPRDR_FORMAT_DATA_REQUEST
#[derive(Debug)]
struct FormatDataRequestPDU {
    format_id: u32,
}

impl FormatDataRequestPDU {
    fn for_id(format_id: u32) -> Self {
        Self { format_id }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = Vec::with_capacity(4);
        w.write_u32::<LittleEndian>(self.format_id)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        Ok(Self {
            format_id: payload.read_u32::<LittleEndian>()?,
        })
    }
}

/// Sent as a reply to the format data request PDU, and is used for both:
/// 1. Indicating that the processing of the request was succesful, and
/// 2. Sending the contents of the requested clipboard data
#[derive(Debug)]
struct FormatDataResponsePDU {
    data: Vec<u8>,
}

impl FormatDataResponsePDU {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        Ok(self.data.clone())
    }

    fn decode(payload: &mut Payload, length: u32) -> RdpResult<Self> {
        let mut data = vec![0; length as usize];
        payload.read_exact(data.as_mut_slice())?;

        Ok(Self { data })
    }
}

/// encode_message encodes a message by wrapping it in the appropriate
/// channel header. If the payload exceeds the maximum size, the message
/// is split into multiple messages.
fn encode_message(msg_type: ClipboardPDUType, payload: Vec<u8>) -> RdpResult<Vec<Vec<u8>>> {
    let msg_flags = match msg_type {
        // the spec requires 0 for these messages
        ClipboardPDUType::CB_CLIP_CAPS => ClipboardHeaderFlags::from_bits_truncate(0),
        ClipboardPDUType::CB_TEMP_DIRECTORY => ClipboardHeaderFlags::from_bits_truncate(0),
        ClipboardPDUType::CB_LOCK_CLIPDATA => ClipboardHeaderFlags::from_bits_truncate(0),
        ClipboardPDUType::CB_UNLOCK_CLIPDATA => ClipboardHeaderFlags::from_bits_truncate(0),
        ClipboardPDUType::CB_FORMAT_DATA_REQUEST => ClipboardHeaderFlags::from_bits_truncate(0),

        // assume success for now
        ClipboardPDUType::CB_FORMAT_DATA_RESPONSE => ClipboardHeaderFlags::CB_RESPONSE_OK,
        ClipboardPDUType::CB_FORMAT_LIST_RESPONSE => ClipboardHeaderFlags::CB_RESPONSE_OK,

        // we don't advertise support for file transfers, so the server should never send this,
        // but if it does, ensure the response indicates a failure
        ClipboardPDUType::CB_FILECONTENTS_RESPONSE => ClipboardHeaderFlags::CB_RESPONSE_FAIL,

        _ => ClipboardHeaderFlags::from_bits_truncate(0),
    };
    let mut inner = ClipboardPDUHeader::new(msg_type, msg_flags, payload.len() as u32).encode()?;
    inner.extend(payload);
    let total_len = inner.len() as u32;

    let mut result = Vec::new();
    let mut first = true;
    while !inner.is_empty() {
        let i = std::cmp::min(inner.len(), vchan::CHANNEL_CHUNK_LEGNTH);
        let leftover = inner.split_off(i);

        let mut channel_flags = match msg_type {
            ClipboardPDUType::CB_FORMAT_LIST
            | ClipboardPDUType::CB_CLIP_CAPS
            | ClipboardPDUType::CB_FORMAT_DATA_REQUEST
            | ClipboardPDUType::CB_FORMAT_DATA_RESPONSE => {
                vchan::ChannelPDUFlags::CHANNEL_FLAG_SHOW_PROTOCOL
            }
            _ => vchan::ChannelPDUFlags::from_bits_truncate(0),
        };

        if first {
            channel_flags.set(vchan::ChannelPDUFlags::CHANNEL_FLAG_FIRST, true);
            first = false;
        }
        if leftover.is_empty() {
            channel_flags.set(vchan::ChannelPDUFlags::CHANNEL_FLAG_LAST, true);
        }

        // the Channel PDU Header always specifies the *total length* of the PDU,
        // even if it has to be split into multpile chunks:
        // https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/a542bf19-1c86-4c80-ab3e-61449653abf6
        let mut outer = vchan::ChannelPDUHeader::new(total_len, channel_flags).encode()?;
        outer.extend(inner);
        result.push(outer);

        inner = leftover;
    }

    Ok(result)
}

#[cfg(test)]
mod tests {
    use crate::vchan::ChannelPDUFlags;

    use super::*;
    use std::io::Cursor;
    use std::sync::mpsc::channel;

    #[test]
    fn encode_format_list_short() {
        let msg = encode_message(
            ClipboardPDUType::CB_FORMAT_LIST,
            FormatListPDU {
                format_names: vec![ShortFormatName::id(ClipboardFormatId::CF_TEXT as u32)],
            }
            .encode()
            .unwrap(),
        )
        .unwrap();

        assert_eq!(
            msg[0],
            vec![
                // virtual channel header
                0x2C, 0x00, 0x00, 0x00, // length (44 bytes)
                0x13, 0x00, 0x00, 0x00, // flags (first + last + show protocol)
                // Clipboard PDU Header
                0x02, 0x00, // message type
                0x00, 0x00, // message flags (CB_ASCII_NAMES not set)
                0x24, 0x00, 0x00, 0x00, // message length (36 bytes after header)
                // Format List PDU starts here
                0x01, 0x00, 0x00, 0x00, // format ID (CF_TEXT)
                0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // format name (bytes 1-8)
                0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // format name (bytes 9-16)
                0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // format name (bytes 17-24)
                0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // format name (bytes 25-32)
            ]
        );
    }

    #[test]
    fn encode_format_list_long() {
        let empty = FormatListPDU::<LongFormatName> {
            format_names: vec![LongFormatName::id(0)],
        };

        let encoded =
            encode_message(ClipboardPDUType::CB_FORMAT_LIST, empty.encode().unwrap()).unwrap();

        assert_eq!(
            encoded[0],
            vec![
                0x0e, 0x00, 0x00, 0x00, // message length (14 bytes)
                0x13, 0x00, 0x00, 0x00, // flags (first + last + show protocol)
                0x02, 0x00, 0x00, 0x00, // message type (format list), and flags (0)
                0x06, 0x00, 0x00, 0x00, // message length (6 bytes)
                0x00, 0x00, 0x00, 0x00, // format id 0
                0x00, 0x00 // null terminator
            ]
        );
    }

    #[test]
    fn encode_clipboard_capabilities() {
        let msg = ClipboardCapabilitiesPDU {
            general: Some(GeneralClipboardCapabilitySet {
                version: CB_CAPS_VERSION_2,
                flags: ClipboardGeneralCapabilityFlags::from_bits_truncate(0),
            }),
        }
        .encode()
        .unwrap();

        assert_eq!(
            msg,
            vec![
                0x01, 0x00, 0x00, 0x00, // count, pad
                0x01, 0x00, 0x0C, 0x00, // type, length
                0x02, 0x00, 0x00, 0x00, // version (2)
                0x00, 0x00, 0x00, 0x00, // flags (0)
            ]
        )
    }

    #[test]
    fn decode_clipboard_capabilities() {
        let msg = ClipboardCapabilitiesPDU::decode(&mut Cursor::new(vec![
            0x01, 0x00, 0x00, 0x00, // count, pad
            0x01, 0x00, 0x0C, 0x00, // type, length
            0x02, 0x00, 0x00, 0x00, // version (2)
            0x00, 0x00, 0x00, 0x00, // flags (0)
        ]))
        .unwrap();

        let general_set = msg.general.unwrap();
        assert_eq!(general_set.flags.bits(), 0);
        assert_eq!(general_set.version, CB_CAPS_VERSION_2);
    }

    #[test]
    fn decode_format_list_long() {
        let no_name = vec![0x01, 0x00, 0x00, 0x00, 0x00, 0x00];
        let l = no_name.len();
        let decoded =
            FormatListPDU::<LongFormatName>::decode(&mut Cursor::new(no_name), l as u32).unwrap();
        assert_eq!(decoded.format_names.len(), 1);
        assert_eq!(
            decoded.format_names[0].format_id,
            ClipboardFormatId::CF_TEXT as u32
        );
        assert_eq!(decoded.format_names[0].format_name, None);

        let one_name = vec![
            0x01, 0x00, 0x00, 0x00, // CF_TEXT
            0x74, 0x00, 0x65, 0x00, 0x73, 0x00, 0x74, 0x00, // "test"
            0x00, 0x00, // null terminator
        ];
        let l = one_name.len();
        let decoded =
            FormatListPDU::<LongFormatName>::decode(&mut Cursor::new(one_name), l as u32).unwrap();
        assert_eq!(decoded.format_names.len(), 1);
        assert_eq!(
            decoded.format_names[0].format_id,
            ClipboardFormatId::CF_TEXT as u32
        );
        assert_eq!(
            decoded.format_names[0].format_name,
            Some(String::from("test"))
        );

        let two_names = vec![
            0x01, 0x00, 0x00, 0x00, // CF_TEXT
            0x74, 0x00, 0x65, 0x00, 0x73, 0x00, 0x74, 0x00, // "test"
            0x00, 0x00, // null terminator
            0x01, 0x00, 0x00, 0x00, // CF_TEXT
            0x74, 0x00, 0x65, 0x00, 0x6c, 0x00, 0x65, 0x00, // "tele"
            0x70, 0x00, 0x6f, 0x00, 0x72, 0x00, 0x74, 0x00, // "port"
            0x00, 0x00, // null terminator
        ];
        let l = two_names.len();
        let decoded =
            FormatListPDU::<LongFormatName>::decode(&mut Cursor::new(two_names), l as u32).unwrap();
        assert_eq!(decoded.format_names.len(), 2);
        assert_eq!(
            decoded.format_names[0].format_id,
            ClipboardFormatId::CF_TEXT as u32
        );
        assert_eq!(
            decoded.format_names[0].format_name,
            Some(String::from("test"))
        );
        assert_eq!(
            decoded.format_names[1].format_id,
            ClipboardFormatId::CF_TEXT as u32
        );
        assert_eq!(
            decoded.format_names[1].format_name,
            Some(String::from("teleport"))
        );
    }

    #[test]
    fn responds_to_monitor_ready() {
        let c: Client = Default::default();
        let responses = c
            .handle_monitor_ready(&mut Cursor::new(Vec::new()))
            .unwrap();
        assert_eq!(2, responses.len());

        // First response - our client capabilities:
        let mut payload = Cursor::new(responses[0].clone());
        let _pdu_header = vchan::ChannelPDUHeader::decode(&mut payload).unwrap();
        let header = ClipboardPDUHeader::decode(&mut payload).unwrap();
        assert_eq!(header.msg_type, ClipboardPDUType::CB_CLIP_CAPS);

        let capabilities = ClipboardCapabilitiesPDU::decode(&mut payload).unwrap();
        let general = capabilities.general.unwrap();
        assert_eq!(
            general.flags,
            ClipboardGeneralCapabilityFlags::CB_USE_LONG_FORMAT_NAMES
        );

        // Second response - the format list PDU:
        let mut payload = Cursor::new(responses[1].clone());
        let _pdu_header = vchan::ChannelPDUHeader::decode(&mut payload).unwrap();
        let header = ClipboardPDUHeader::decode(&mut payload).unwrap();
        assert_eq!(header.msg_type, ClipboardPDUType::CB_FORMAT_LIST);
        assert_eq!(header.msg_flags.bits(), 0);
        assert_eq!(header.data_len, 6);

        let format_list =
            FormatListPDU::<LongFormatName>::decode(&mut payload, header.data_len).unwrap();
        assert_eq!(format_list.format_names.len(), 1);
        assert_eq!(format_list.format_names[0].format_id, 0);
        assert_eq!(format_list.format_names[0].format_name, None);
    }

    #[test]
    fn encodes_large_format_data_response() {
        let mut data = Vec::new();
        data.resize(vchan::CHANNEL_CHUNK_LEGNTH + 2, 0);
        for (i, item) in data.iter_mut().enumerate() {
            *item = (i % 256) as u8;
        }
        let pdu = FormatDataResponsePDU { data };
        let encoded = pdu.encode().unwrap();
        let messages = encode_message(ClipboardPDUType::CB_FORMAT_DATA_RESPONSE, encoded).unwrap();
        assert_eq!(2, messages.len());

        let header0 =
            vchan::ChannelPDUHeader::decode(&mut Cursor::new(messages[0].clone())).unwrap();
        assert_eq!(
            ChannelPDUFlags::CHANNEL_FLAG_FIRST | ChannelPDUFlags::CHANNEL_FLAG_SHOW_PROTOCOL,
            header0.flags
        );
        let header1 =
            vchan::ChannelPDUHeader::decode(&mut Cursor::new(messages[1].clone())).unwrap();
        assert_eq!(
            ChannelPDUFlags::CHANNEL_FLAG_LAST | ChannelPDUFlags::CHANNEL_FLAG_SHOW_PROTOCOL,
            header1.flags
        );
    }

    #[test]
    fn responds_to_format_data_request_hasdata() {
        // a null-terminated utf-16 string, represented as a Vec<u8>
        let test_data: Vec<u8> = "test\0"
            .encode_utf16()
            .flat_map(|v| v.to_le_bytes())
            .collect();

        let mut c: Client = Default::default();
        c.clipboard
            .insert(ClipboardFormatId::CF_OEMTEXT as u32, test_data.clone());

        let req = FormatDataRequestPDU::for_id(ClipboardFormatId::CF_OEMTEXT as u32);
        let responses = c
            .handle_format_data_request(&mut Cursor::new(req.encode().unwrap()))
            .unwrap();

        // expect one FormatDataResponsePDU
        assert_eq!(responses.len(), 1);
        let mut payload = Cursor::new(responses[0].clone());
        let _pdu_header = vchan::ChannelPDUHeader::decode(&mut payload).unwrap();
        let header = ClipboardPDUHeader::decode(&mut payload).unwrap();
        assert_eq!(header.msg_type, ClipboardPDUType::CB_FORMAT_DATA_RESPONSE);
        assert_eq!(header.msg_flags, ClipboardHeaderFlags::CB_RESPONSE_OK);
        assert_eq!(header.data_len, 10);
        let resp = FormatDataResponsePDU::decode(&mut payload, header.data_len).unwrap();
        assert_eq!(resp.data, test_data);
    }

    #[test]
    fn invokes_callback_with_clipboard_data() {
        let (send, recv) = channel();

        let mut c = Client::new(Box::new(move |vec| {
            send.send(vec).unwrap();
        }));

        let data_resp = FormatDataResponsePDU {
            data: String::from("abc\0").into_bytes(),
        }
        .encode()
        .unwrap();

        let len = data_resp.len();

        c.handle_format_data_response(&mut Cursor::new(data_resp), len as u32)
            .unwrap();

        // ensure that the null terminator was trimmed
        let received = recv.try_recv().unwrap();
        assert_eq!(received, String::from("abc").into_bytes());
    }

    #[test]
    fn update_clipboard_returns_format_list_pdu() {
        let mut c: Client = Default::default();
        let messages = c
            .update_clipboard(String::from("abc").into_bytes())
            .unwrap();
        let bytes = messages[0].clone();

        // verify that it returns a properly encoded format list PDU
        let mut payload = Cursor::new(bytes);
        let _pdu_header = vchan::ChannelPDUHeader::decode(&mut payload).unwrap();
        let header = ClipboardPDUHeader::decode(&mut payload).unwrap();
        let format_list =
            FormatListPDU::<LongFormatName>::decode(&mut payload, header.data_len as u32).unwrap();
        assert_eq!(ClipboardPDUType::CB_FORMAT_LIST, header.msg_type);
        assert_eq!(1, format_list.format_names.len());
        assert_eq!(
            ClipboardFormatId::CF_OEMTEXT as u32,
            format_list.format_names[0].format_id
        );

        // verify that the clipboard data is now cached
        // (with a null-terminating character)
        assert_eq!(
            String::from("abc\0").into_bytes(),
            *c.clipboard
                .get(&(ClipboardFormatId::CF_OEMTEXT as u32))
                .unwrap()
        );
    }

    #[test]
    fn update_clipboard_conversion() {
        for (input, expected) in &[
            ("abc\0", "abc\0"),       // already null-terminated, no conversion necessary
            ("\n123", "\r\n123\0"),   // starts with LF
            ("def\r\n", "def\r\n\0"), // already CRLF, no conversion necessary
            ("gh\r\nij\nk", "gh\r\nij\r\nk\0"), // mixture of both
        ] {
            let mut c: Client = Default::default();
            c.update_clipboard(String::from(*input).into_bytes())
                .unwrap();
            assert_eq!(
                String::from(*expected).into_bytes(),
                *c.clipboard
                    .get(&(ClipboardFormatId::CF_OEMTEXT as u32))
                    .unwrap(),
                "testing {}",
                input
            );
        }
    }
}
