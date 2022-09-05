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

mod consts;
pub(crate) mod dir;
mod flags;
pub(crate) mod path;
pub(crate) mod scard;
use self::dir::FileCacheObject;
use self::path::WindowsPath;
use crate::errors::{invalid_data_error, not_implemented_error, try_error};
use crate::util;
use crate::vchan;
use crate::{
    FileSystemObject, FileType, Payload, SharedDirectoryCreateResponse,
    SharedDirectoryDeleteResponse, SharedDirectoryInfoResponse, SharedDirectoryListResponse,
    SharedDirectoryMoveResponse, SharedDirectoryReadResponse, SharedDirectoryWriteResponse,
};

use byteorder::{LittleEndian, ReadBytesExt, WriteBytesExt};
pub use consts::CHANNEL_NAME;
use consts::{
    CapabilityType, Component, DeviceType, FileInformationClassLevel,
    FileSystemInformationClassLevel, MajorFunction, MinorFunction, PacketId, BOOL_SIZE,
    DIRECTORY_SHARE_CLIENT_NAME, DRIVE_CAPABILITY_VERSION_02, FILE_ATTR_SIZE,
    GENERAL_CAPABILITY_VERSION_02, I64_SIZE, I8_SIZE, NTSTATUS, SCARD_DEVICE_ID,
    SMARTCARD_CAPABILITY_VERSION_01, U32_SIZE, U8_SIZE, VERSION_MAJOR, VERSION_MINOR,
};
use num_traits::{FromPrimitive, ToPrimitive};
use rdp::core::mcs;
use rdp::core::tpkt;
use rdp::model::data::Message;
use rdp::model::error::{Error as RdpError, RdpResult};
use std::convert::{TryFrom, TryInto};
use std::ffi::CString;
use std::io::{Read, Seek, SeekFrom, Write};

/// Client implements a device redirection (RDPDR) client, as defined in
/// https://winprotocoldoc.blob.core.windows.net/productionwindowsarchives/MS-RDPEFS/%5bMS-RDPEFS%5d.pdf
///
/// This client only supports a single smartcard device.
pub struct Client {
    vchan: vchan::Client,
    scard: scard::Client,
    dir: dir::Client,

    active_device_ids: Vec<u32>,
}

pub struct Config {
    pub scard_cfg: scard::Config,
    pub dir_cfg: dir::Config,
}

impl Client {
    pub fn new(cfg: Config) -> Self {
        Client {
            vchan: vchan::Client::new(),
            scard: scard::Client::new(cfg.scard_cfg),
            dir: dir::Client::new(cfg.dir_cfg),

            active_device_ids: vec![],
        }
    }
    /// Reads raw RDP messages sent on the rdpdr virtual channel and replies as necessary.
    pub fn read_and_reply<S: Read + Write>(
        &mut self,
        payload: tpkt::Payload,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        if let Some(mut payload) = self.vchan.read(payload)? {
            let header = SharedHeader::decode(&mut payload)?;
            if let Component::RDPDR_CTYP_PRN = header.component {
                warn!("got {:?} RDPDR header from RDP server, ignoring because we're not redirecting any printers", header);
                return Ok(());
            }
            let responses = match header.packet_id {
                PacketId::PAKID_CORE_SERVER_ANNOUNCE => {
                    self.handle_server_announce(&mut payload)?
                }
                PacketId::PAKID_CORE_SERVER_CAPABILITY => {
                    self.handle_server_capability(&mut payload)?
                }
                PacketId::PAKID_CORE_CLIENTID_CONFIRM => {
                    self.handle_client_id_confirm(&mut payload)?
                }
                PacketId::PAKID_CORE_DEVICE_REPLY => self.handle_device_reply(&mut payload)?,
                // Device IO request is where communication with the smartcard and shared drive actually happens.
                // Everything up to this point was negotiation (and smartcard device registration).
                PacketId::PAKID_CORE_DEVICE_IOREQUEST => {
                    self.handle_device_io_request(&mut payload)?
                }
                _ => {
                    // We don't implement the full set of messages.
                    error!(
                        "RDPDR packets {:?} are not implemented yet, ignoring",
                        header.packet_id
                    );
                    vec![]
                }
            };

            let chan = &CHANNEL_NAME.to_string();
            for resp in responses {
                mcs.write(chan, resp)?;
            }
        }
        Ok(())
    }

    fn handle_server_announce(&self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let req = ServerAnnounceRequest::decode(payload)?;
        debug!("received RDP {:?}", req);

        let resp = ClientAnnounceReply::new(req);
        debug!("sending RDP {:?}", resp);

        let mut resp =
            self.add_headers_and_chunkify(PacketId::PAKID_CORE_CLIENTID_CONFIRM, resp.encode()?)?;

        let client_name_request = ClientNameRequest::new(
            ClientNameRequestUnicodeFlag::Ascii,
            CString::new(DIRECTORY_SHARE_CLIENT_NAME.to_string()).unwrap(),
        );

        let mut client_name_response = self.add_headers_and_chunkify(
            PacketId::PAKID_CORE_CLIENT_NAME,
            client_name_request.encode()?,
        )?;
        resp.append(&mut client_name_response);

        Ok(resp)
    }

    fn handle_server_capability(&self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let req = ServerCoreCapabilityRequest::decode(payload)?;
        debug!("received RDP {:?}", req);

        let resp = ClientCoreCapabilityResponse::new_response(self.dir.allow_directory_sharing)
            .encode()?;
        debug!("sending RDP {:?}", resp);
        let resp = self.add_headers_and_chunkify(PacketId::PAKID_CORE_CLIENT_CAPABILITY, resp)?;
        Ok(resp)
    }

    fn handle_client_id_confirm(&mut self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let req = ServerClientIdConfirm::decode(payload)?;
        debug!("received RDP {:?}", req);

        // The smartcard initialization sequence that contains this message happens once at session startup,
        // and once when login succeeds. We only need to announce the smartcard once.
        let resp = if !self.active_device_ids.contains(&SCARD_DEVICE_ID) {
            self.push_active_device_id(SCARD_DEVICE_ID)?;
            let resp = ClientDeviceListAnnounceRequest::new_smartcard(SCARD_DEVICE_ID);
            debug!("sending RDP {:?}", resp);
            self.add_headers_and_chunkify(PacketId::PAKID_CORE_DEVICELIST_ANNOUNCE, resp.encode()?)?
        } else {
            let resp = ClientDeviceListAnnounceRequest::new_empty();
            debug!("sending RDP {:?}", resp);
            self.add_headers_and_chunkify(PacketId::PAKID_CORE_DEVICELIST_ANNOUNCE, resp.encode()?)?
        };
        Ok(resp)
    }

    fn handle_device_reply(&mut self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let res = ServerDeviceAnnounceResponse::decode(payload)?;
        debug!("received RDP: {:?}", res);

        if !self.active_device_ids.contains(&res.device_id) {
            return Err(invalid_data_error(&format!(
                "got ServerDeviceAnnounceResponse for unknown device_id {}",
                &res.device_id
            )));
        }

        if res.device_id != self.get_scard_device_id()? {
            // This was for a directory we're sharing over TDP
            self.dir.handle_device_reply(res)
        } else {
            // This was for the smart card
            self.scard.handle_device_reply(res)
        }
    }

    fn handle_device_io_request(&mut self, payload: &mut Payload) -> RdpResult<Vec<Vec<u8>>> {
        let device_io_request = DeviceIoRequest::decode(payload)?;
        let major_function = device_io_request.major_function.clone();

        // Smartcard control only uses IRP_MJ_DEVICE_CONTROL; directory control uses IRP_MJ_DEVICE_CONTROL along with
        // all the other MajorFunctions supported by this Client. Therefore if we receive any other major function when drive
        // redirection is not allowed, something has gone wrong. In such a case, we return an error as a security measure
        // to ensure directories are never shared when RBAC doesn't permit it.
        if major_function != MajorFunction::IRP_MJ_DEVICE_CONTROL
            && !self.dir.allow_directory_sharing
        {
            return Err(RdpError::TryError(
                "received a drive redirection major function when drive redirection was not allowed"
                    .to_string(),
            ));
        }

        let resp = match major_function {
            MajorFunction::IRP_MJ_DEVICE_CONTROL => {
                self.process_irp_device_control(device_io_request, payload)
            }
            MajorFunction::IRP_MJ_CREATE => self.dir.process_irp_create(device_io_request, payload),
            MajorFunction::IRP_MJ_QUERY_INFORMATION => self
                .dir
                .process_irp_query_information(device_io_request, payload),
            MajorFunction::IRP_MJ_CLOSE => self.dir.process_irp_close(device_io_request),
            MajorFunction::IRP_MJ_DIRECTORY_CONTROL => self
                .dir
                .process_irp_directory_control(device_io_request, payload),
            MajorFunction::IRP_MJ_QUERY_VOLUME_INFORMATION => self
                .dir
                .process_irp_query_volume_information(device_io_request, payload),
            MajorFunction::IRP_MJ_READ => self.dir.process_irp_read(device_io_request, payload),
            MajorFunction::IRP_MJ_WRITE => self.dir.process_irp_write(device_io_request, payload),
            MajorFunction::IRP_MJ_SET_INFORMATION => self
                .dir
                .process_irp_set_information(device_io_request, payload),
            _ => Err(invalid_data_error(&format!(
                // TODO(isaiah): send back a not implemented response(?)
                "got unsupported major_function in DeviceIoRequest: {:?}",
                &major_function
            ))),
        }?;

        let resp = self.add_headers_and_chunkify(PacketId::PAKID_CORE_DEVICE_IOCOMPLETION, resp)?;
        Ok(resp)
    }

    fn process_irp_device_control(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        let ioctl = DeviceControlRequest::decode(device_io_request, payload)?;
        let is_smart_card_op = ioctl.header.device_id == self.get_scard_device_id()?;
        debug!("received RDP: {:?}", ioctl);

        // IRP_MJ_DEVICE_CONTROL is the one major function used by both the smartcard controller (always enabled)
        // and shared directory controller (potentially disabled by RBAC). Here we check that directory sharing
        // is enabled here before proceeding with any shared directory controls as an additional security measure.
        if !is_smart_card_op && !self.dir.allow_directory_sharing {
            return Err(RdpError::TryError("received a drive redirection major function when drive redirection was not allowed".to_string()));
        }
        if is_smart_card_op {
            // Smart card control
            self.scard.process_irp_device_control(ioctl, payload)
        } else {
            // Drive redirection
            self.dir.process_irp_device_control(ioctl)
        }
    }

    pub fn write_client_device_list_announce<S: Read + Write>(
        &mut self,
        req: ClientDeviceListAnnounce,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.push_active_device_id(req.device_list[0].device_id)?;
        debug!("sending new drive for redirection over RDP: {:?}", req);

        let responses =
            self.add_headers_and_chunkify(PacketId::PAKID_CORE_DEVICELIST_ANNOUNCE, req.encode()?)?;
        let chan = &CHANNEL_NAME.to_string();
        for resp in responses {
            mcs.write(chan, resp)?;
        }

        Ok(())
    }

    pub fn handle_tdp_sd_info_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryInfoResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_info_response(res, mcs)
    }

    pub fn handle_tdp_sd_create_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryCreateResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_create_response(res, mcs)
    }

    pub fn handle_tdp_sd_delete_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryDeleteResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_delete_response(res, mcs)
    }

    pub fn handle_tdp_sd_list_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryListResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_list_response(res, mcs)
    }

    pub fn handle_tdp_sd_read_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryReadResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_read_response(res, mcs)
    }

    pub fn handle_tdp_sd_write_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryWriteResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_write_response(res, mcs)
    }

    pub fn handle_tdp_sd_move_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryMoveResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        self.dir.handle_tdp_sd_move_response(res, mcs)
    }

    /// add_headers_and_chunkify takes an encoded PDU ready to be sent over a virtual channel (payload),
    /// adds on the Shared Header based the passed packet_id, adds the appropriate (virtual) Channel PDU Header,
    /// and splits the entire payload into chunks if the payload exceeds the maximum size.
    fn add_headers_and_chunkify(
        &self,
        packet_id: PacketId,
        payload: Vec<u8>,
    ) -> RdpResult<Vec<Vec<u8>>> {
        let mut inner = SharedHeader::new(Component::RDPDR_CTYP_CORE, packet_id).encode()?;
        inner.extend_from_slice(&payload);
        self.vchan.add_header_and_chunkify(None, inner)
    }

    fn push_active_device_id(&mut self, device_id: u32) -> RdpResult<()> {
        if self.active_device_ids.contains(&device_id) {
            return Err(RdpError::TryError(format!(
                "attempted to add a duplicate device_id {} to active_device_ids {:?}",
                device_id, self.active_device_ids
            )));
        }
        self.active_device_ids.push(device_id);
        Ok(())
    }

    fn get_scard_device_id(&self) -> RdpResult<u32> {
        // We always push it into the list first
        if !self.active_device_ids.is_empty() {
            return Ok(self.active_device_ids[0]);
        }
        Err(RdpError::TryError("no active device ids".to_string()))
    }
}

/// 2.2.1.1 Shared Header (RDPDR_HEADER)
/// This header is present at the beginning of every message in sent over the rdpdr virtual channel.
/// The purpose of this header is to describe the type of the message.
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/29d4108f-8163-4a67-8271-e48c4b9c2a7c
#[derive(Debug)]
struct SharedHeader {
    component: Component,
    packet_id: PacketId,
}

impl SharedHeader {
    fn new(component: Component, packet_id: PacketId) -> Self {
        Self {
            component,
            packet_id,
        }
    }
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let component = payload.read_u16::<LittleEndian>()?;
        let packet_id = payload.read_u16::<LittleEndian>()?;
        Ok(Self {
            component: Component::from_u16(component).ok_or_else(|| {
                invalid_data_error(&format!("invalid component value {:#06x}", component))
            })?,
            packet_id: PacketId::from_u16(packet_id).ok_or_else(|| {
                invalid_data_error(&format!("invalid packet_id value {:#06x}", packet_id))
            })?,
        })
    }
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u16::<LittleEndian>(self.component.to_u16().unwrap())?;
        w.write_u16::<LittleEndian>(self.packet_id.to_u16().unwrap())?;
        Ok(w)
    }
}

type ServerAnnounceRequest = ClientIdMessage;
type ClientAnnounceReply = ClientIdMessage;
type ServerClientIdConfirm = ClientIdMessage;

#[derive(Debug)]
struct ClientIdMessage {
    version_major: u16,
    version_minor: u16,
    client_id: u32,
}

impl ClientIdMessage {
    fn new(req: ServerAnnounceRequest) -> Self {
        Self {
            version_major: VERSION_MAJOR,
            version_minor: VERSION_MINOR,
            client_id: req.client_id,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u16::<LittleEndian>(self.version_major)?;
        w.write_u16::<LittleEndian>(self.version_minor)?;
        w.write_u32::<LittleEndian>(self.client_id)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        Ok(Self {
            version_major: payload.read_u16::<LittleEndian>()?,
            version_minor: payload.read_u16::<LittleEndian>()?,
            client_id: payload.read_u32::<LittleEndian>()?,
        })
    }
}

#[derive(Debug)]
struct ServerCoreCapabilityRequest {
    num_capabilities: u16,
    padding: u16,
    capabilities: Vec<CapabilitySet>,
}

impl ServerCoreCapabilityRequest {
    fn new_response(allow_directory_sharing: bool) -> Self {
        // Clients are always required to send the "general" capability set.
        // In addition, we also send the optional smartcard capability (CAP_SMARTCARD_TYPE)
        // and drive capability (CAP_DRIVE_TYPE).
        let mut capabilities = vec![
            CapabilitySet {
                header: CapabilityHeader {
                    cap_type: CapabilityType::CAP_GENERAL_TYPE,
                    length: 8 + 36, // 8 byte header + 36 byte capability descriptor
                    version: GENERAL_CAPABILITY_VERSION_02,
                },
                data: Capability::General(GeneralCapabilitySet {
                    os_type: 0,
                    os_version: 0,
                    protocol_major_version: VERSION_MAJOR,
                    protocol_minor_version: VERSION_MINOR,
                    io_code_1: 0x00007fff, // Combination of all the required bits.
                    io_code_2: 0,
                    extended_pdu: 0x00000001 | 0x00000002, // RDPDR_DEVICE_REMOVE_PDUS | RDPDR_CLIENT_DISPLAY_NAME_PDU
                    extra_flags_1: 0,
                    extra_flags_2: 0,
                    special_type_device_cap: 1, // Request redirection of 1 special device - smartcard.
                }),
            },
            CapabilitySet {
                header: CapabilityHeader {
                    cap_type: CapabilityType::CAP_SMARTCARD_TYPE,
                    length: 8, // 8 byte header + empty capability descriptor
                    version: SMARTCARD_CAPABILITY_VERSION_01,
                },
                data: Capability::Smartcard,
            },
        ];

        if allow_directory_sharing {
            capabilities.push(CapabilitySet {
                header: CapabilityHeader {
                    cap_type: CapabilityType::CAP_DRIVE_TYPE,
                    length: 8, // 8 byte header + empty capability descriptor
                    version: DRIVE_CAPABILITY_VERSION_02,
                },
                data: Capability::Drive,
            });
        }

        Self {
            padding: 0,
            num_capabilities: capabilities.len() as u16,
            capabilities,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u16::<LittleEndian>(self.num_capabilities)?;
        w.write_u16::<LittleEndian>(self.padding)?;
        for cap in self.capabilities.iter() {
            w.extend_from_slice(&cap.encode()?);
        }
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let num_capabilities = payload.read_u16::<LittleEndian>()?;
        let padding = payload.read_u16::<LittleEndian>()?;
        let mut capabilities = vec![];
        for _ in 0..num_capabilities {
            capabilities.push(CapabilitySet::decode(payload)?);
        }

        Ok(Self {
            num_capabilities,
            padding,
            capabilities,
        })
    }
}

#[derive(Debug)]
struct CapabilitySet {
    header: CapabilityHeader,
    data: Capability,
}

impl CapabilitySet {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = self.header.encode()?;
        w.extend_from_slice(&self.data.encode()?);
        Ok(w)
    }
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let header = CapabilityHeader::decode(payload)?;
        let data = Capability::decode(payload, &header)?;

        Ok(Self { header, data })
    }
}

#[derive(Debug)]
struct CapabilityHeader {
    cap_type: CapabilityType,
    length: u16,
    version: u32,
}

impl CapabilityHeader {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u16::<LittleEndian>(self.cap_type.to_u16().unwrap())?;
        w.write_u16::<LittleEndian>(self.length)?;
        w.write_u32::<LittleEndian>(self.version)?;
        Ok(w)
    }
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let cap_type = payload.read_u16::<LittleEndian>()?;
        Ok(Self {
            cap_type: CapabilityType::from_u16(cap_type).ok_or_else(|| {
                invalid_data_error(&format!("invalid capability type {:#06x}", cap_type))
            })?,
            length: payload.read_u16::<LittleEndian>()?,
            version: payload.read_u32::<LittleEndian>()?,
        })
    }
}

#[derive(Debug)]
enum Capability {
    General(GeneralCapabilitySet),
    Printer,
    Port,
    Drive,
    Smartcard,
}

impl Capability {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        match self {
            Capability::General(general) => Ok(general.encode()?),
            _ => Ok(vec![]),
        }
    }

    fn decode(payload: &mut Payload, header: &CapabilityHeader) -> RdpResult<Self> {
        match header.cap_type {
            CapabilityType::CAP_GENERAL_TYPE => Ok(Capability::General(
                GeneralCapabilitySet::decode(payload, header.version)?,
            )),
            CapabilityType::CAP_PRINTER_TYPE => Ok(Capability::Printer),
            CapabilityType::CAP_PORT_TYPE => Ok(Capability::Port),
            CapabilityType::CAP_DRIVE_TYPE => Ok(Capability::Drive),
            CapabilityType::CAP_SMARTCARD_TYPE => Ok(Capability::Smartcard),
        }
    }
}

#[derive(Debug)]
struct GeneralCapabilitySet {
    os_type: u32,
    os_version: u32,
    protocol_major_version: u16,
    protocol_minor_version: u16,
    io_code_1: u32,
    io_code_2: u32,
    extended_pdu: u32,
    extra_flags_1: u32,
    extra_flags_2: u32,
    special_type_device_cap: u32,
}

impl GeneralCapabilitySet {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.os_type)?;
        w.write_u32::<LittleEndian>(self.os_version)?;
        w.write_u16::<LittleEndian>(self.protocol_major_version)?;
        w.write_u16::<LittleEndian>(self.protocol_minor_version)?;
        w.write_u32::<LittleEndian>(self.io_code_1)?;
        w.write_u32::<LittleEndian>(self.io_code_2)?;
        w.write_u32::<LittleEndian>(self.extended_pdu)?;
        w.write_u32::<LittleEndian>(self.extra_flags_1)?;
        w.write_u32::<LittleEndian>(self.extra_flags_2)?;
        w.write_u32::<LittleEndian>(self.special_type_device_cap)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload, version: u32) -> RdpResult<Self> {
        Ok(Self {
            os_type: payload.read_u32::<LittleEndian>()?,
            os_version: payload.read_u32::<LittleEndian>()?,
            protocol_major_version: payload.read_u16::<LittleEndian>()?,
            protocol_minor_version: payload.read_u16::<LittleEndian>()?,
            io_code_1: payload.read_u32::<LittleEndian>()?,
            io_code_2: payload.read_u32::<LittleEndian>()?,
            extended_pdu: payload.read_u32::<LittleEndian>()?,
            extra_flags_1: payload.read_u32::<LittleEndian>()?,
            extra_flags_2: payload.read_u32::<LittleEndian>()?,
            special_type_device_cap: if version == GENERAL_CAPABILITY_VERSION_02 {
                payload.read_u32::<LittleEndian>()?
            } else {
                0
            },
        })
    }
}

type ClientCoreCapabilityResponse = ServerCoreCapabilityRequest;

#[derive(Debug)]
pub struct ClientDeviceListAnnounceRequest {
    device_count: u32,
    device_list: Vec<DeviceAnnounceHeader>,
}

pub type ClientDeviceListAnnounce = ClientDeviceListAnnounceRequest;

impl ClientDeviceListAnnounceRequest {
    // We only need to announce the smartcard in this Client Device List Announce Request.
    // Drives (directories) can be announced at any time with a Client Drive Device List Announce.
    fn new_smartcard(device_id: u32) -> Self {
        Self {
            device_count: 1,
            device_list: vec![DeviceAnnounceHeader {
                device_type: DeviceType::RDPDR_DTYP_SMARTCARD,
                device_id,
                // This name is a constant defined by the spec.
                preferred_dos_name: "SCARD".to_string(),
                device_data_length: 0,
                device_data: vec![],
            }],
        }
    }

    /// A new drive can be announced at any time during RDP's operation. It is up to the caller
    pub fn new_drive(device_id: u32, drive_name: String) -> Self {
        // If the client supports DRIVE_CAPABILITY_VERSION_02 in the Drive Capability Set,
        // then the full name MUST also be specified in the DeviceData field, as a null-terminated
        // Unicode string. If the DeviceDataLength field is nonzero, the content of the
        // PreferredDosName field is ignored.
        //
        // In the RDP spec, Unicode typically means null-terminated UTF-16LE, however empirically it
        // appears that this field expects null-terminated UTF-8.
        let device_data = util::to_utf8(&drive_name);

        Self {
            device_count: 1,
            device_list: vec![DeviceAnnounceHeader {
                device_type: DeviceType::RDPDR_DTYP_FILESYSTEM,
                device_id,
                preferred_dos_name: drive_name,
                device_data_length: device_data.len() as u32,
                device_data,
            }],
        }
    }

    fn new_empty() -> Self {
        Self {
            device_count: 0,
            device_list: vec![],
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.device_count)?;
        for dev in self.device_list.iter() {
            w.extend_from_slice(&dev.encode()?);
        }
        Ok(w)
    }
}

/// 2.2.1.3 Device Announce Header (DEVICE_ANNOUNCE)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/32e34332-774b-4ead-8c9d-5d64720d6bf9
#[derive(Debug)]
struct DeviceAnnounceHeader {
    device_type: DeviceType,
    device_id: u32,
    preferred_dos_name: String,
    device_data_length: u32,
    device_data: Vec<u8>,
}

impl DeviceAnnounceHeader {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.device_type.to_u32().unwrap())?;
        w.write_u32::<LittleEndian>(self.device_id)?;
        let mut name: &str = &self.preferred_dos_name;
        // See "PreferredDosName" at
        // https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/32e34332-774b-4ead-8c9d-5d64720d6bf9
        if name.len() > 7 {
            name = &name[..7];
        }
        w.extend_from_slice(&format!("{:\x00<8}", name).into_bytes());
        w.write_u32::<LittleEndian>(self.device_data_length)?;
        w.extend_from_slice(&self.device_data);
        Ok(w)
    }
}

#[derive(Debug)]
pub struct ServerDeviceAnnounceResponse {
    device_id: u32,
    result_code: u32,
}

impl ServerDeviceAnnounceResponse {
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        Ok(Self {
            device_id: payload.read_u32::<LittleEndian>()?,
            result_code: payload.read_u32::<LittleEndian>()?,
        })
    }
}

#[derive(Debug, Clone, ToPrimitive)]
#[repr(u32)]
#[allow(non_camel_case_types)]
#[allow(dead_code)]
enum ClientNameRequestUnicodeFlag {
    Ascii = 0x0,
    Unicode = 0x1,
}

/// 2.2.2.4 Client Name Request (DR_CORE_CLIENT_NAME_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/902497f1-3b1c-4aee-95f8-1668f9b7b7d2
#[derive(Debug, Clone)]
pub struct ClientNameRequest {
    unicode_flag: ClientNameRequestUnicodeFlag,
    computer_name: CString,
}

impl ClientNameRequest {
    fn new(unicode_flag: ClientNameRequestUnicodeFlag, computer_name: CString) -> Self {
        Self {
            unicode_flag,
            computer_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.unicode_flag.clone() as u32)?;
        // CodePage (4 bytes): A 32-bit unsigned integer that specifies the code page of the ComputerName field; it MUST be set to 0.
        w.write_u32::<LittleEndian>(0x0)?;

        let computer_name_data = match self.unicode_flag {
            ClientNameRequestUnicodeFlag::Ascii => self.computer_name.to_bytes_with_nul().to_vec(),
            ClientNameRequestUnicodeFlag::Unicode => util::to_unicode(
                self.computer_name
                    .as_c_str()
                    .to_str()
                    .map_err(|err| RdpError::TryError(err.to_string()))?,
                true,
            ),
        };

        w.write_u32::<LittleEndian>(computer_name_data.len() as u32)?;
        w.extend_from_slice(&computer_name_data);
        Ok(w)
    }
}

/// 2.2.1.4 Device I/O Request (DR_DEVICE_IOREQUEST)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/a087ffa8-d0d5-4874-ac7b-0494f63e2d5d
#[derive(Debug, Clone)]
pub struct DeviceIoRequest {
    pub device_id: u32,
    file_id: u32,
    pub completion_id: u32,
    major_function: MajorFunction,
    minor_function: MinorFunction,
}

impl DeviceIoRequest {
    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let device_id = payload.read_u32::<LittleEndian>()?;
        let file_id = payload.read_u32::<LittleEndian>()?;
        let completion_id = payload.read_u32::<LittleEndian>()?;
        let major_function = payload.read_u32::<LittleEndian>()?;
        let major_function = MajorFunction::from_u32(major_function).ok_or_else(|| {
            invalid_data_error(&format!(
                "invalid major function value {:#010x}",
                major_function
            ))
        })?;
        let minor_function = payload.read_u32::<LittleEndian>()?;
        // From the spec (2.2.1.4 Device I/O Request (DR_DEVICE_IOREQUEST)):
        // "This field [MinorFunction] is valid only when the MajorFunction field
        // is set to IRP_MJ_DIRECTORY_CONTROL. If the MajorFunction field is set
        // to another value, the MinorFunction field value SHOULD be 0x00000000.""
        //
        // SHOULD means implementations are not guaranteed to give us 0x00000000,
        // so handle that possibility here.
        let minor_function = if major_function == MajorFunction::IRP_MJ_DIRECTORY_CONTROL {
            minor_function
        } else {
            0x00000000
        };
        let minor_function = MinorFunction::from_u32(minor_function).ok_or_else(|| {
            invalid_data_error(&format!(
                "invalid minor function value {:#010x}",
                minor_function
            ))
        })?;

        Ok(Self {
            device_id,
            file_id,
            completion_id,
            major_function,
            minor_function,
        })
    }
}

/// 2.2.1.4.5 Device Control Request (DR_CONTROL_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/30662c80-ec6e-4ed1-9004-2e6e367bb59f
#[derive(Debug)]
#[allow(dead_code)]
pub struct DeviceControlRequest {
    header: DeviceIoRequest,
    output_buffer_length: u32,
    input_buffer_length: u32,
    io_control_code: u32,
    padding: [u8; 20],
}

impl DeviceControlRequest {
    fn decode(header: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let output_buffer_length = payload.read_u32::<LittleEndian>()?;
        let input_buffer_length = payload.read_u32::<LittleEndian>()?;
        let io_control_code = payload.read_u32::<LittleEndian>()?;
        let mut padding: [u8; 20] = [0; 20];
        payload.read_exact(&mut padding)?;
        Ok(Self {
            header,
            output_buffer_length,
            input_buffer_length,
            io_control_code,
            padding,
        })
    }
}

/// 2.2.1.5 Device I/O Response (DR_DEVICE_IOCOMPLETION)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/1c412a84-0776-4984-b35c-3f0445fcae65
#[derive(Debug)]
struct DeviceIoResponse {
    device_id: u32,
    completion_id: u32,
    io_status: u32,
}

impl DeviceIoResponse {
    fn new(req: &DeviceIoRequest, io_status: u32) -> Self {
        Self {
            device_id: req.device_id,
            completion_id: req.completion_id,
            io_status,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.device_id)?;
        w.write_u32::<LittleEndian>(self.completion_id)?;
        w.write_u32::<LittleEndian>(self.io_status)?;
        Ok(w)
    }
}

#[derive(Debug)]
struct DeviceControlResponse {
    header: DeviceIoResponse,
    output_buffer_length: u32,
    output_buffer: Vec<u8>,
}

impl DeviceControlResponse {
    fn new(req: &DeviceControlRequest, io_status: u32, output: Vec<u8>) -> Self {
        Self {
            header: DeviceIoResponse::new(&req.header, io_status),
            output_buffer_length: output.length() as u32,
            output_buffer: output,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.header.encode()?);
        w.write_u32::<LittleEndian>(self.output_buffer_length)?;
        w.extend_from_slice(&self.output_buffer);
        Ok(w)
    }
}

/// 2.2.3.3.1 Server Create Drive Request (DR_DRIVE_CREATE_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/95b16fd0-d530-407c-a310-adedc85e9897
pub type ServerCreateDriveRequest = DeviceCreateRequest;

/// 2.2.1.4.1 Device Create Request (DR_CREATE_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/5f71f6d2-d9ff-40c2-bdb5-a739447d3c3e
#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct DeviceCreateRequest {
    /// The MajorFunction field in this header MUST be set to IRP_MJ_CREATE.
    pub device_io_request: DeviceIoRequest,
    desired_access: flags::DesiredAccess,
    allocation_size: u64,
    file_attributes: flags::FileAttributes,
    shared_access: flags::SharedAccess,
    create_disposition: flags::CreateDisposition,
    create_options: flags::CreateOptions,
    path_length: u32,
    pub path: WindowsPath,
}

impl DeviceCreateRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        debug!("In DeviceCreateRequest decode");
        let invalid_flags = |flag_name: &str, v: u32| {
            invalid_data_error(&format!(
                "invalid flags in Device Create Request: {} = {}",
                flag_name, v
            ))
        };

        let desired_access = payload.read_u32::<LittleEndian>()?;
        let allocation_size = payload.read_u64::<LittleEndian>()?;
        let file_attributes = payload.read_u32::<LittleEndian>()?;
        let shared_access = payload.read_u32::<LittleEndian>()?;
        let create_disposition = payload.read_u32::<LittleEndian>()?;
        let create_options = payload.read_u32::<LittleEndian>()?;
        let path_length = payload.read_u32::<LittleEndian>()?;

        let desired_access = flags::DesiredAccess::from_bits(desired_access)
            .ok_or_else(|| invalid_flags("desired_access", desired_access))?;
        let file_attributes = flags::FileAttributes::from_bits(file_attributes)
            .ok_or_else(|| invalid_flags("file_attributes", file_attributes))?;
        let shared_access = flags::SharedAccess::from_bits(shared_access)
            .ok_or_else(|| invalid_flags("shared_access", shared_access))?;
        let create_disposition = flags::CreateDisposition::from_bits(create_disposition)
            .ok_or_else(|| invalid_flags("create_disposition", create_disposition))?;
        let create_options = flags::CreateOptions::from_bits(create_options)
            .ok_or_else(|| invalid_flags("create_options", create_options))?;

        // usize is 32 bits on a 32 bit target and 64 on a 64, so we can safely say try_into().unwrap()
        // for a u32 will never panic on the machines that run teleport.
        let mut path = vec![0u8; path_length.try_into().unwrap()];
        payload.read_exact(&mut path)?;
        let path = WindowsPath::from(util::from_unicode(path)?);

        Ok(Self {
            device_io_request,
            desired_access,
            allocation_size,
            file_attributes,
            shared_access,
            create_disposition,
            create_options,
            path_length,
            path,
        })
    }
}

/// 2.2.1.5.1 Device Create Response (DR_CREATE_RSP)
/// A message with this header describes a response to a Device Create Request (section 2.2.1.4.1).
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/99e5fca5-b37a-41e4-bc69-8d7da7860f76
#[derive(Debug)]
struct DeviceCreateResponse {
    device_io_reply: DeviceIoResponse,
    file_id: u32,
    /// The values of the CreateDisposition field in the Device Create Request (section 2.2.1.4.1) that determine the value
    /// of the Information field are associated as follows:
    /// +---------------------+--------------------+
    /// | CreateDisposition   |   Information      |
    /// +---------------------+--------------------+
    /// | FILE_SUPERSEDE      |   FILE_SUPERSEDED  |
    /// | FILE_OPEN           |                    |
    /// | FILE_CREATE         |                    |
    /// | FILE_OVERWRITE      |                    |
    /// +---------------------+--------------------+
    /// | FILE_OPEN_IF        |   FILE_OPENED      |
    /// +---------------------+--------------------+
    /// | FILE_OVERWRITE_IF   |   FILE_OVERWRITTEN |
    /// +---------------------+--------------------+
    information: flags::Information,
}

impl DeviceCreateResponse {
    fn new(device_create_request: &DeviceCreateRequest, io_status: NTSTATUS, file_id: u32) -> Self {
        let device_io_request = &device_create_request.device_io_request;

        let information: flags::Information;
        if io_status != NTSTATUS::STATUS_SUCCESS
            || device_create_request.create_disposition.intersects(
                flags::CreateDisposition::FILE_SUPERSEDE
                    | flags::CreateDisposition::FILE_OPEN
                    | flags::CreateDisposition::FILE_CREATE
                    | flags::CreateDisposition::FILE_OVERWRITE,
            )
        {
            // if io_status != NTSTATUS::STATUS_SUCCESS because that's what FreeRDP sets information to in the case of failure, see
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L191
            information = flags::Information::FILE_SUPERSEDED;
        } else if device_create_request.create_disposition == flags::CreateDisposition::FILE_OPEN_IF
        {
            information = flags::Information::FILE_OPENED;
        } else if device_create_request.create_disposition
            == flags::CreateDisposition::FILE_OVERWRITE_IF
        {
            information = flags::Information::FILE_OVERWRITTEN;
        } else {
            panic!("program error, CreateDispositionFlags check should be exhaustive");
        }

        Self {
            device_io_reply: DeviceIoResponse::new(
                device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            file_id,
            information,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.file_id)?;
        w.write_u8(self.information.bits())?;
        Ok(w)
    }
}

/// 2.2.3.3.8 Server Drive Query Information Request (DR_DRIVE_QUERY_INFORMATION_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/e43dcd68-2980-40a9-9238-344b6cf94946
#[derive(Debug)]
struct ServerDriveQueryInformationRequest {
    /// A DR_DEVICE_IOREQUEST (section 2.2.1.4) header. The MajorFunction field in the DR_DEVICE_IOREQUEST header MUST be set to IRP_MJ_QUERY_INFORMATION.
    device_io_request: DeviceIoRequest,
    /// A 32-bit unsigned integer.
    /// This field MUST contain one of the following values:
    /// FileBasicInformation
    /// This information class is used to query a file for the times of creation, last access, last write, and change, in addition to file attribute information. The Reserved field of the FileBasicInformation structure ([MS-FSCC] section 2.4.7) MUST NOT be present.
    ///
    /// FileStandardInformation
    /// This information class is used to query for file information such as allocation size, end-of-file position, and number of links. The Reserved field of the FileStandardInformation structure ([MS-FSCC] section 2.4.41) MUST NOT be present.
    ///
    /// FileAttributeTagInformation
    /// This information class is used to query for file attribute and reparse tag information.
    file_info_class_lvl: FileInformationClassLevel,
    // Length, Padding, and QueryBuffer appear to be vestigial fields and can safely be ignored. Their description
    // is provided below for documentation purposes.
    //
    // Length (4 bytes): A 32-bit unsigned integer that specifies the number of bytes in the QueryBuffer field.
    //
    // Padding (24 bytes): An array of 24 bytes. This field is unused and MUST be ignored.
    //
    // QueryBuffer (variable): A variable-length array of bytes. The size of the array is specified by the Length field.
    // The content of this field is based on the value of the FileInformationClass field, which determines the different
    // structures that MUST be contained in the QueryBuffer field. For a complete list of these structures, see [MS-FSCC]
    // section 2.4. The "File information class" table defines all the possible values for the FileInformationClass field.
}

impl ServerDriveQueryInformationRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let n = payload.read_u32::<LittleEndian>()?;
        if let Some(file_info_class_lvl) = FileInformationClassLevel::from_u32(n) {
            return Ok(Self {
                device_io_request,
                file_info_class_lvl,
            });
        }

        Err(invalid_data_error(
            format!(
                "received invalid FileInformationClass in ServerDriveQueryInformationRequest: {}",
                n
            )
            .as_str(),
        ))
    }
}

/// 2.4 File Information Classes [MS-FSCC]
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/4718fc40-e539-4014-8e33-b675af74e3e1
#[derive(Debug, Clone)]
#[allow(dead_code, clippy::enum_variant_names)]
enum FileInformationClass {
    FileBasicInformation(FileBasicInformation),
    FileStandardInformation(FileStandardInformation),
    FileBothDirectoryInformation(FileBothDirectoryInformation),
    FileAttributeTagInformation(FileAttributeTagInformation),
    FileFullDirectoryInformation(FileFullDirectoryInformation),
    FileEndOfFileInformation(FileEndOfFileInformation),
    FileDispositionInformation(FileDispositionInformation),
    FileRenameInformation(FileRenameInformation),
    FileAllocationInformation(FileAllocationInformation),
    FileNamesInformation(FileNamesInformation),
    FileDirectoryInformation(FileDirectoryInformation),
}

impl FileInformationClass {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        match self {
            Self::FileBasicInformation(file_info_class) => file_info_class.encode(),
            Self::FileStandardInformation(file_info_class) => file_info_class.encode(),
            Self::FileBothDirectoryInformation(file_info_class) => file_info_class.encode(),
            Self::FileAttributeTagInformation(file_info_class) => file_info_class.encode(),
            Self::FileFullDirectoryInformation(file_info_class) => file_info_class.encode(),
            Self::FileEndOfFileInformation(file_info_class) => file_info_class.encode(),
            Self::FileDispositionInformation(file_info_class) => file_info_class.encode(),
            Self::FileRenameInformation(file_info_class) => file_info_class.encode(),
            Self::FileAllocationInformation(file_info_class) => file_info_class.encode(),
            Self::FileNamesInformation(file_info_class) => file_info_class.encode(),
            Self::FileDirectoryInformation(file_info_class) => file_info_class.encode(),
        }
    }

    fn decode(
        file_information_class_level: &FileInformationClassLevel,
        length: u32,
        payload: &mut Payload,
    ) -> RdpResult<Self> {
        match file_information_class_level {
            FileInformationClassLevel::FileBasicInformation => Ok(
                FileInformationClass::FileBasicInformation(FileBasicInformation::decode(payload)?),
            ),
            FileInformationClassLevel::FileEndOfFileInformation => {
                Ok(FileInformationClass::FileEndOfFileInformation(
                    FileEndOfFileInformation::decode(payload)?,
                ))
            }
            FileInformationClassLevel::FileDispositionInformation => {
                Ok(FileInformationClass::FileDispositionInformation(
                    FileDispositionInformation::decode(payload, length)?,
                ))
            }
            FileInformationClassLevel::FileRenameInformation => {
                Ok(FileInformationClass::FileRenameInformation(
                    FileRenameInformation::decode(payload)?,
                ))
            }
            FileInformationClassLevel::FileAllocationInformation => {
                Ok(FileInformationClass::FileAllocationInformation(
                    FileAllocationInformation::decode(payload)?,
                ))
            }
            _ => Err(invalid_data_error(&format!(
                "decode invalid FileInformationClassLevel: {:?}",
                file_information_class_level
            ))),
        }
    }

    fn size(&self) -> u32 {
        match self {
            Self::FileBasicInformation(file_info_class) => file_info_class.size(),
            Self::FileStandardInformation(file_info_class) => file_info_class.size(),
            Self::FileBothDirectoryInformation(file_info_class) => file_info_class.size(),
            Self::FileAttributeTagInformation(file_info_class) => file_info_class.size(),
            Self::FileFullDirectoryInformation(file_info_class) => file_info_class.size(),
            Self::FileEndOfFileInformation(file_info_class) => file_info_class.size(),
            Self::FileDispositionInformation(file_info_class) => file_info_class.size(),
            Self::FileRenameInformation(file_info_class) => file_info_class.size(),
            Self::FileAllocationInformation(file_info_class) => file_info_class.size(),
            Self::FileNamesInformation(file_info_class) => file_info_class.size(),
            Self::FileDirectoryInformation(file_info_class) => file_info_class.size(),
        }
    }
}

/// 2.4.7 FileBasicInformation [MS-FSCC]
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/16023025-8a78-492f-8b96-c873b042ac50
#[derive(Debug, Clone)]
struct FileBasicInformation {
    creation_time: i64,
    last_access_time: i64,
    last_write_time: i64,
    change_time: i64,
    file_attributes: flags::FileAttributes,
    // NOTE: The `reserved` field in the spec MUST not be serialized and sent over RDP, or it will break the server implementation.
    // FreeRDP does the same: https://github.com/FreeRDP/FreeRDP/blob/1adb263813ca2e76a893ef729a04db8f94b5d757/channels/drive/client/drive_file.c#L508
    //reserved: u32,
}

impl FileBasicInformation {
    const BASE_SIZE: u32 = (4 * I64_SIZE) + FILE_ATTR_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.creation_time)?;
        w.write_i64::<LittleEndian>(self.last_access_time)?;
        w.write_i64::<LittleEndian>(self.last_write_time)?;
        w.write_i64::<LittleEndian>(self.change_time)?;
        w.write_u32::<LittleEndian>(self.file_attributes.bits())?;
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let creation_time = payload.read_i64::<LittleEndian>()?;
        let last_access_time = payload.read_i64::<LittleEndian>()?;
        let last_write_time = payload.read_i64::<LittleEndian>()?;
        let change_time = payload.read_i64::<LittleEndian>()?;
        let file_attributes = flags::FileAttributes::from_bits(payload.read_u32::<LittleEndian>()?)
            .ok_or_else(|| invalid_data_error("invalid flags in FileBasicInformation decode"))?;

        Ok(Self {
            creation_time,
            last_access_time,
            last_write_time,
            change_time,
            file_attributes,
        })
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.4.41 FileStandardInformation [MS-FSCC]
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/5afa7f66-619c-48f3-955f-68c4ece704ae
#[derive(Debug, Clone)]
struct FileStandardInformation {
    /// A 64-bit signed integer that contains the file allocation size, in bytes. The value of this field MUST be an
    /// integer multiple of the cluster size.
    /// Cluster size is the size of the logical minimal unit of disk space used by the operating system. FreeRDP
    /// doesn't give the actual size here, but rather just gives the file size itself, which we will mimic.
    /// (ttps://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L518-L519).
    ///
    /// When FileStandardInformation is requested for a directory, its not entirely clear what "file size" means.
    /// FreeRDP derives this value from the st_size field of a stat struct (https://linux.die.net/man/2/lstat), which says
    /// "The st_size field gives the size of the file (if it is a regular file or a symbolic link) in bytes. The size of
    /// a symbolic link is the length of the pathname it contains, without a terminating null byte." Since it's not
    /// entirely clear what is offered here in the case of a directory, we will just use 0.
    allocation_size: i64,
    /// A 64-bit signed integer that contains the absolute end-of-file position as a byte offset from the start of the
    /// file. EndOfFile specifies the offset to the byte immediately following the last valid byte in the file. Because
    /// this value is zero-based, it actually refers to the first free byte in the file. That is, it is the offset from
    /// the beginning of the file at which new bytes appended to the file will be written. The value of this field MUST
    /// be greater than or equal to 0.
    end_of_file: i64,
    /// A 32-bit unsigned integer that contains the number of non-deleted [hard] links to this file.
    /// NOTE: this information is not available to us in the browser, so we will simply set this field to 0.
    number_of_links: u32,
    /// Set to TRUE to indicate that a file deletion has been requested; set to FALSE
    /// otherwise.
    delete_pending: Boolean,
    /// Set to TRUE to indicate that the file is a directory; set to FALSE otherwise.
    directory: Boolean,
    // NOTE: `reserved` field omitted, see NOTE in FileBasicInformation struct.
    // reserved: u16,
}

impl FileStandardInformation {
    const BASE_SIZE: u32 = (2 * I64_SIZE) + U32_SIZE + (2 * BOOL_SIZE);

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.allocation_size)?;
        w.write_i64::<LittleEndian>(self.end_of_file)?;
        w.write_u32::<LittleEndian>(self.number_of_links)?;
        w.write_u8(Boolean::to_u8(&self.delete_pending).unwrap())?;
        w.write_u8(Boolean::to_u8(&self.directory).unwrap())?;
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.4.6 FileAttributeTagInformation [MS-FSCC]
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/d295752f-ce89-4b98-8553-266d37c84f0e?redirectedfrom=MSDN
#[derive(Debug, Clone)]
struct FileAttributeTagInformation {
    file_attributes: flags::FileAttributes,
    reparse_tag: u32,
}

impl FileAttributeTagInformation {
    const BASE_SIZE: u32 = U32_SIZE + FILE_ATTR_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.file_attributes.bits())?;
        w.write_u32::<LittleEndian>(self.reparse_tag)?;
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.1.8 Boolean
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/8ce7b38c-d3cc-415d-ab39-944000ea77ff
#[derive(Debug, FromPrimitive, ToPrimitive, PartialEq, Clone)]
#[repr(u8)]
enum Boolean {
    True = 1,
    False = 0,
}

/// 2.4.8 FileBothDirectoryInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/270df317-9ba5-4ccb-ba00-8d22be139bc5
#[derive(Debug, Clone)]
struct FileBothDirectoryInformation {
    next_entry_offset: u32,
    file_index: u32,
    creation_time: i64,
    last_access_time: i64,
    last_write_time: i64,
    change_time: i64,
    end_of_file: i64,
    allocation_size: i64,
    file_attributes: flags::FileAttributes,
    file_name_length: u32,
    ea_size: u32,
    short_name_length: i8,
    // reserved: u8: MUST NOT be added,
    // see https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L907
    short_name: [u8; 24], // 24 bytes
    file_name: String,
}

impl FileBothDirectoryInformation {
    /// Base size of the FileBothDirectoryInformation, not accounting for variably sized file_name.
    /// Note that file_name's size should be calculated as if it were a Unicode string.
    const BASE_SIZE: u32 = (4 * U32_SIZE) + FILE_ATTR_SIZE + (6 * I64_SIZE) + I8_SIZE + 24; // 93

    fn new(
        creation_time: i64,
        last_access_time: i64,
        last_write_time: i64,
        change_time: i64,
        file_size: i64,
        file_attributes: flags::FileAttributes,
        file_name: String,
    ) -> Self {
        // Default field values taken from
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L871
        Self {
            next_entry_offset: 0,
            file_index: 0,
            creation_time,
            last_access_time,
            last_write_time,
            change_time,
            end_of_file: file_size,
            allocation_size: file_size,
            file_attributes,
            file_name_length: util::unicode_size(&file_name, false),
            ea_size: 0,
            short_name_length: 0,
            short_name: [0; 24],
            file_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.next_entry_offset)?;
        w.write_u32::<LittleEndian>(self.file_index)?;
        w.write_i64::<LittleEndian>(self.creation_time)?;
        w.write_i64::<LittleEndian>(self.last_access_time)?;
        w.write_i64::<LittleEndian>(self.last_write_time)?;
        w.write_i64::<LittleEndian>(self.change_time)?;
        w.write_i64::<LittleEndian>(self.end_of_file)?;
        w.write_i64::<LittleEndian>(self.allocation_size)?;
        w.write_u32::<LittleEndian>(self.file_attributes.bits())?;
        w.write_u32::<LittleEndian>(self.file_name_length)?;
        w.write_u32::<LittleEndian>(self.ea_size)?;
        w.write_i8(self.short_name_length)?;
        // reserved u8, MUST NOT be added!
        w.extend_from_slice(&self.short_name);
        // When working with this field, use file_name_length to determine the length of the file name rather
        // than assuming the presence of a trailing null delimiter. Dot directory names are valid for this field.
        w.extend_from_slice(&util::to_unicode(&self.file_name, false));
        Ok(w)
    }

    fn from(fso: FileSystemObject) -> RdpResult<Self> {
        let file_attributes = if fso.file_type == FileType::Directory {
            flags::FileAttributes::FILE_ATTRIBUTE_DIRECTORY
        } else {
            flags::FileAttributes::FILE_ATTRIBUTE_NORMAL
        };

        let last_modified = to_windows_time(fso.last_modified);

        Ok(FileBothDirectoryInformation::new(
            last_modified,
            last_modified,
            last_modified,
            last_modified,
            i64::try_from(fso.size)?,
            file_attributes,
            fso.name()?,
        ))
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_name_length
    }
}

/// 2.4.14 FileFullDirectoryInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/e8d926d1-3a22-4654-be9c-58317a85540b
#[derive(Debug, Clone)]
struct FileFullDirectoryInformation {
    next_entry_offset: u32,
    file_index: u32,
    creation_time: i64,
    last_access_time: i64,
    last_write_time: i64,
    change_time: i64,
    end_of_file: i64,
    allocation_size: i64,
    file_attributes: flags::FileAttributes,
    file_name_length: u32,
    ea_size: u32,
    file_name: String,
}

impl FileFullDirectoryInformation {
    /// Base size of the FileFullDirectoryInformation, not accounting for variably sized file_name.
    /// Note that file_name's size should be calculated as if it were a Unicode string.
    const BASE_SIZE: u32 = (4 * U32_SIZE) + FILE_ATTR_SIZE + (6 * I64_SIZE); // 68

    fn new(
        creation_time: i64,
        last_access_time: i64,
        last_write_time: i64,
        change_time: i64,
        file_size: i64,
        file_attributes: flags::FileAttributes,
        file_name: String,
    ) -> Self {
        // Default field values taken from
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L871
        Self {
            next_entry_offset: 0,
            file_index: 0,
            creation_time,
            last_access_time,
            last_write_time,
            change_time,
            end_of_file: file_size,
            allocation_size: file_size,
            file_attributes,
            file_name_length: util::unicode_size(&file_name, false),
            ea_size: 0,
            file_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.next_entry_offset)?;
        w.write_u32::<LittleEndian>(self.file_index)?;
        w.write_i64::<LittleEndian>(self.creation_time)?;
        w.write_i64::<LittleEndian>(self.last_access_time)?;
        w.write_i64::<LittleEndian>(self.last_write_time)?;
        w.write_i64::<LittleEndian>(self.change_time)?;
        w.write_i64::<LittleEndian>(self.end_of_file)?;
        w.write_i64::<LittleEndian>(self.allocation_size)?;
        w.write_u32::<LittleEndian>(self.file_attributes.bits())?;
        w.write_u32::<LittleEndian>(self.file_name_length)?;
        w.write_u32::<LittleEndian>(self.ea_size)?;
        // When working with this field, use file_name_length to determine the length of the file name rather
        // than assuming the presence of a trailing null delimiter. Dot directory names are valid for this field.
        w.extend_from_slice(&util::to_unicode(&self.file_name, false));
        Ok(w)
    }

    fn from(fso: FileSystemObject) -> RdpResult<Self> {
        let file_attributes = if fso.file_type == FileType::Directory {
            flags::FileAttributes::FILE_ATTRIBUTE_DIRECTORY
        } else {
            flags::FileAttributes::FILE_ATTRIBUTE_NORMAL
        };

        let last_modified = to_windows_time(fso.last_modified);

        Ok(Self::new(
            last_modified,
            last_modified,
            last_modified,
            last_modified,
            i64::try_from(fso.size)?,
            file_attributes,
            fso.name()?,
        ))
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_name_length
    }
}

// 2.4.13 FileEndOfFileInformation
// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/75241cca-3167-472f-8058-a52d77c6bb17
#[derive(Debug, Clone)]
struct FileEndOfFileInformation {
    end_of_file: i64,
}

impl FileEndOfFileInformation {
    const BASE_SIZE: u32 = I64_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.end_of_file)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let end_of_file = payload.read_i64::<LittleEndian>()?;
        Ok(Self { end_of_file })
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

// 2.4.11 FileDispositionInformation
// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/12c3dd1c-14f6-4229-9d29-75fb2cb392f6
#[derive(Debug, Clone)]
struct FileDispositionInformation {
    delete_pending: u8,
}

impl FileDispositionInformation {
    const BASE_SIZE: u32 = U8_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u8(self.delete_pending)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload, length: u32) -> RdpResult<Self> {
        // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_file.c#L684-L692
        let delete_pending = if length != 0 { payload.read_u8()? } else { 1 };
        Ok(Self { delete_pending })
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

// 2.4.37 FileRenameInformation
// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/1d2673a8-8fb9-4868-920a-775ccaa30cf8
#[derive(Debug, Clone)]
struct FileRenameInformation {
    replace_if_exists: Boolean,
    /// file_name is the relative path to the new location of the file
    file_name: WindowsPath,
}

impl FileRenameInformation {
    // This matches the FreeRDP implementation rather than Microsoft specification
    // see encode method
    const BASE_SIZE: u32 = (2 * U8_SIZE) + U32_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L709
        // https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/3668ae46-1df5-4656-b481-763877428bcb
        // This matches the FreeRDP implementation rather than Microsoft specification
        let mut w = vec![];
        w.write_u8(Boolean::to_u8(&self.replace_if_exists).unwrap())?;
        // RootDirectory. For network operations, this value MUST be zero.
        w.write_u8(0)?;
        w.write_u32::<LittleEndian>(self.file_name.len() as u32)?;
        w.extend_from_slice(&util::to_unicode(&self.file_name.path, false));
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let replace_if_exists = payload.read_u8()?;
        // RootDirectory
        payload.read_u8()?;

        let file_name_length = payload.read_u32::<LittleEndian>()?;
        let mut file_name = vec![0u8; file_name_length as usize];
        payload.read_exact(&mut file_name)?;
        let file_name = WindowsPath::from(util::from_unicode(file_name)?);

        Ok(Self {
            replace_if_exists: Boolean::from_u8(replace_if_exists).unwrap(),
            file_name,
        })
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_name.len() as u32
    }
}

// 2.4.4 FileAllocationInformation
// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/0201c69b-50db-412d-bab3-dd97aeede13b
#[derive(Debug, Clone)]
struct FileAllocationInformation {
    allocation_size: i64,
}

impl FileAllocationInformation {
    const BASE_SIZE: u32 = I64_SIZE;

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.allocation_size)?;
        Ok(w)
    }

    fn decode(payload: &mut Payload) -> RdpResult<Self> {
        let allocation_size = payload.read_i64::<LittleEndian>()?;

        Ok(Self { allocation_size })
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.4.28 FileNamesInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/a289f7a8-83d2-4927-8c88-b2d328dde5a5?redirectedfrom=MSDN
#[derive(Debug, Clone)]
struct FileNamesInformation {
    next_entry_offset: u32,
    file_index: u32,
    file_name_length: u32,
    file_name: String,
}

impl FileNamesInformation {
    /// Base size of the FileBothDirectoryInformation, not accounting for variably sized file_name.
    /// Note that file_name's size should be calculated as if it were a Unicode string.
    const BASE_SIZE: u32 = 3 * U32_SIZE;

    fn new(file_name: String) -> Self {
        // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_file.c#L912
        Self {
            next_entry_offset: 0,
            file_index: 0,
            file_name_length: util::unicode_size(&file_name, false),
            file_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.next_entry_offset)?;
        w.write_u32::<LittleEndian>(self.file_index)?;
        w.write_u32::<LittleEndian>(self.file_name_length)?;
        w.extend_from_slice(&util::to_unicode(&self.file_name, false));
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_name_length
    }
}

/// 2.4.10 FileDirectoryInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/b38bf518-9057-4c88-9ddd-5e2d3976a64b
#[derive(Debug, Clone)]
struct FileDirectoryInformation {
    next_entry_offset: u32,
    file_index: u32,
    creation_time: i64,
    last_access_time: i64,
    last_write_time: i64,
    change_time: i64,
    end_of_file: i64,
    allocation_size: i64,
    file_attributes: flags::FileAttributes,
    file_name_length: u32,
    file_name: String,
}

impl FileDirectoryInformation {
    /// Base size of the FileDirectoryInformation, not accounting for variably sized file_name.
    /// Note that file_name's size should be calculated as if it were a Unicode string.
    const BASE_SIZE: u32 = (3 * U32_SIZE) + FILE_ATTR_SIZE + (6 * I64_SIZE); // 64

    fn new(
        creation_time: i64,
        last_access_time: i64,
        last_write_time: i64,
        change_time: i64,
        file_size: i64,
        file_attributes: flags::FileAttributes,
        file_name: String,
    ) -> Self {
        // Default field values taken from
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L796
        Self {
            next_entry_offset: 0,
            file_index: 0,
            creation_time,
            last_access_time,
            last_write_time,
            change_time,
            end_of_file: file_size,
            allocation_size: file_size,
            file_attributes,
            file_name_length: util::unicode_size(&file_name, false),
            file_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.next_entry_offset)?;
        w.write_u32::<LittleEndian>(self.file_index)?;
        w.write_i64::<LittleEndian>(self.creation_time)?;
        w.write_i64::<LittleEndian>(self.last_access_time)?;
        w.write_i64::<LittleEndian>(self.last_write_time)?;
        w.write_i64::<LittleEndian>(self.change_time)?;
        w.write_i64::<LittleEndian>(self.end_of_file)?;
        w.write_i64::<LittleEndian>(self.allocation_size)?;
        w.write_u32::<LittleEndian>(self.file_attributes.bits())?;
        w.write_u32::<LittleEndian>(self.file_name_length)?;
        // When working with this field, use file_name_length to determine the length of the file name rather
        // than assuming the presence of a trailing null delimiter. Dot directory names are valid for this field.
        w.extend_from_slice(&util::to_unicode(&self.file_name, false));
        Ok(w)
    }

    fn from(fso: FileSystemObject) -> RdpResult<Self> {
        let file_attributes = if fso.file_type == FileType::Directory {
            flags::FileAttributes::FILE_ATTRIBUTE_DIRECTORY
        } else {
            flags::FileAttributes::FILE_ATTRIBUTE_NORMAL
        };

        let last_modified = to_windows_time(fso.last_modified);

        Ok(Self::new(
            last_modified,
            last_modified,
            last_modified,
            last_modified,
            i64::try_from(fso.size)?,
            file_attributes,
            fso.name()?,
        ))
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_name_length
    }
}

/// 2.5 File System Information Classes
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/ee12042a-9352-46e3-9f67-c094b75fe6c3
#[derive(Debug)]
#[allow(clippy::enum_variant_names)]
#[allow(dead_code)]
enum FileSystemInformationClass {
    FileFsVolumeInformation(FileFsVolumeInformation),
    FileFsSizeInformation(FileFsSizeInformation),
    FileFsAttributeInformation(FileFsAttributeInformation),
    FileFsFullSizeInformation(FileFsFullSizeInformation),
    FileFsDeviceInformation(FileFsDeviceInformation),
}

impl FileSystemInformationClass {
    fn encode(&self) -> RdpResult<Vec<u8>> {
        match self {
            Self::FileFsVolumeInformation(fs_info_class) => fs_info_class.encode(),
            Self::FileFsSizeInformation(fs_info_class) => fs_info_class.encode(),
            Self::FileFsAttributeInformation(fs_info_class) => fs_info_class.encode(),
            Self::FileFsFullSizeInformation(fs_info_class) => fs_info_class.encode(),
            Self::FileFsDeviceInformation(fs_info_class) => fs_info_class.encode(),
        }
    }
}

/// 2.5.9 FileFsVolumeInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/bf691378-c34e-4a13-976e-404ea1a87738
#[derive(Debug)]
struct FileFsVolumeInformation {
    volume_creation_time: i64,
    volume_serial_number: u32,
    volume_label_length: u32,
    supports_objects: Boolean,
    // reserved is omitted
    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L495
    volume_label: String,
}

impl FileFsVolumeInformation {
    /// Base size of the FileFsVolumeInformation, not accounting for variably sized volume_label.
    /// 1 i64, 2 u32, 1 Boolean
    const BASE_SIZE: u32 = I64_SIZE + (2 * U32_SIZE) + BOOL_SIZE; // 17

    fn new(volume_creation_time: i64) -> Self {
        // volume_label can just be something we make up
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L446
        let volume_label = "TELEPORT".to_string();

        Self {
            volume_creation_time,
            // Not sure why the `& 0xffff` is necessary, just copying FreeRDP
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L492
            // u32::MAX is given due to the fact that FreeRDP uses it as a fallback:
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/file/file.c#L1018-L1021
            volume_serial_number: u32::MAX & 0xffff,
            // The FreeRDP function they use to convert the volume_label to unicode is null-terminated
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/crt/unicode.c#L371
            volume_label_length: util::unicode_size(&volume_label, true),
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L494
            supports_objects: Boolean::False,
            volume_label,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.volume_creation_time)?;
        w.write_u32::<LittleEndian>(self.volume_serial_number)?;
        w.write_u32::<LittleEndian>(self.volume_label_length)?;
        w.write_u8(Boolean::to_u8(&self.supports_objects).unwrap())?;
        w.extend_from_slice(&util::to_unicode(&self.volume_label, true));
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.volume_label_length
    }
}

/// 2.5.8 FileFsSizeInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/e13e068c-e3a7-4dd4-94fd-3892b492e6e7
#[derive(Debug)]
struct FileFsSizeInformation {
    total_alloc_units: i64,
    available_alloc_units: i64,
    sectors_per_alloc_unit: u32,
    bytes_per_sector: u32,
}

#[allow(dead_code)]
impl FileFsSizeInformation {
    const BASE_SIZE: u32 = (2 * I64_SIZE) + (2 * U32_SIZE);

    fn new() -> Self {
        // Fill these out with the default fallback values FreeRDP uses
        // Written here: https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L510-L513
        // With default fallback values ultimately found here:
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/file/file.c#L1018-L1021
        Self {
            total_alloc_units: u32::MAX as i64,
            available_alloc_units: u32::MAX as i64,
            sectors_per_alloc_unit: u32::MAX,
            bytes_per_sector: 1,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.total_alloc_units)?;
        w.write_i64::<LittleEndian>(self.available_alloc_units)?;
        w.write_u32::<LittleEndian>(self.sectors_per_alloc_unit)?;
        w.write_u32::<LittleEndian>(self.bytes_per_sector)?;
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.5.1 FileFsAttributeInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/ebc7e6e5-4650-4e54-b17c-cf60f6fbeeaa
#[derive(Debug)]
struct FileFsAttributeInformation {
    file_system_attributes: flags::FileSystemAttributes,
    max_component_name_len: u32,
    file_system_name_len: u32,
    file_system_name: String,
}

impl FileFsAttributeInformation {
    const BASE_SIZE: u32 = (2 * U32_SIZE) + FILE_ATTR_SIZE;

    fn new() -> Self {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L447
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L519
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L538
        let file_system_name = "FAT32".to_string();

        Self {
            file_system_attributes: flags::FileSystemAttributes::FILE_CASE_SENSITIVE_SEARCH
                | flags::FileSystemAttributes::FILE_CASE_PRESERVED_NAMES
                | flags::FileSystemAttributes::FILE_UNICODE_ON_DISK,
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L536
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/include/winpr/file.h#L36
            max_component_name_len: 260,
            // The FreeRDP function they use to convert the file_system_name to unicode is null-terminated
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L519
            file_system_name_len: util::unicode_size(&file_system_name, true),
            file_system_name,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.file_system_attributes.bits())?;
        w.write_u32::<LittleEndian>(self.max_component_name_len)?;
        w.write_u32::<LittleEndian>(self.file_system_name_len)?;
        w.extend_from_slice(&util::to_unicode(&self.file_system_name, true));
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE + self.file_system_name_len
    }
}

/// 2.5.4 FileFsFullSizeInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/63768db7-9012-4209-8cca-00781e7322f5
#[derive(Debug)]
struct FileFsFullSizeInformation {
    total_alloc_units: i64,
    caller_available_alloc_units: i64,
    actual_available_alloc_units: i64,
    sectors_per_alloc_unit: u32,
    bytes_per_sector: u32,
}

#[allow(dead_code)]
impl FileFsFullSizeInformation {
    const BASE_SIZE: u32 = (3 * I64_SIZE) + (2 * U32_SIZE);

    fn new() -> Self {
        // Fill these out with the default fallback values FreeRDP uses
        // Written here: https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L552-L557
        // With default fallback values ultimately found here:
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/file/file.c#L1018-L1021
        Self {
            total_alloc_units: u32::MAX as i64,
            caller_available_alloc_units: u32::MAX as i64,
            actual_available_alloc_units: u32::MAX as i64,
            sectors_per_alloc_unit: u32::MAX,
            bytes_per_sector: 1,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_i64::<LittleEndian>(self.total_alloc_units)?;
        w.write_i64::<LittleEndian>(self.caller_available_alloc_units)?;
        w.write_i64::<LittleEndian>(self.actual_available_alloc_units)?;
        w.write_u32::<LittleEndian>(self.sectors_per_alloc_unit)?;
        w.write_u32::<LittleEndian>(self.bytes_per_sector)?;
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.5.10 FileFsDeviceInformation
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/616b66d5-b335-4e1c-8f87-b4a55e8d3e4a
// Taking a shortcut by ignoring the bitflag typing here, since we will only ever fill this out with single values:
// https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L570-L571
#[derive(Debug)]
struct FileFsDeviceInformation {
    device_type: u32,
    characteristics: u32,
}

#[allow(dead_code)]
impl FileFsDeviceInformation {
    const BASE_SIZE: u32 = 2 * U32_SIZE;

    fn new() -> Self {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L570-L571
        Self {
            device_type: 0x00000007, // FILE_DEVICE_DISK
            characteristics: 0,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.write_u32::<LittleEndian>(self.device_type)?;
        w.write_u32::<LittleEndian>(self.characteristics)?;
        Ok(w)
    }

    fn size(&self) -> u32 {
        Self::BASE_SIZE
    }
}

/// 2.2.3.4.8 Client Drive Query Information Response (DR_DRIVE_QUERY_INFORMATION_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/37ef4fb1-6a95-4200-9fbf-515464f034a4
#[derive(Debug)]
struct ClientDriveQueryInformationResponse {
    device_io_response: DeviceIoResponse,
    length: Option<u32>,
    buffer: Option<FileInformationClass>,
}

impl ClientDriveQueryInformationResponse {
    /// Constructs a ClientDriveQueryInformationResponse from a ServerDriveQueryInformationRequest and an NTSTATUS.
    fn new(
        req: &ServerDriveQueryInformationRequest,
        file: Option<&FileCacheObject>,
        io_status: NTSTATUS,
    ) -> RdpResult<Self> {
        // If io_status == NTSTATUS::STATUS_UNSUCCESSFUL, we can just fill out the
        // device_io_response and don't need to create/encode the rest.
        if io_status == NTSTATUS::STATUS_UNSUCCESSFUL {
            return Ok(Self {
                device_io_response: DeviceIoResponse::new(
                    &req.device_io_request,
                    NTSTATUS::to_u32(&io_status).unwrap(),
                ),
                length: None,
                buffer: None,
            });
        }

        if let Some(file) = file {
            // We support all the FsInformationClasses that FreeRDP does here
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L482
            let (length, buffer) = match req.file_info_class_lvl {
                FileInformationClassLevel::FileBasicInformation => (
                    Some(FileBasicInformation::BASE_SIZE),
                    Some(FileInformationClass::FileBasicInformation(
                        FileBasicInformation {
                            creation_time: to_windows_time(file.fso.last_modified),
                            last_access_time: to_windows_time(file.fso.last_modified),
                            last_write_time: to_windows_time(file.fso.last_modified),
                            change_time: to_windows_time(file.fso.last_modified),
                            file_attributes: if file.fso.file_type == FileType::File {
                                flags::FileAttributes::FILE_ATTRIBUTE_NORMAL
                            } else {
                                flags::FileAttributes::FILE_ATTRIBUTE_DIRECTORY
                            },
                        },
                    )),
                ),
                FileInformationClassLevel::FileStandardInformation => (
                    Some(FileStandardInformation::BASE_SIZE),
                    Some(FileInformationClass::FileStandardInformation(
                        FileStandardInformation {
                            allocation_size: file.fso.size as i64,
                            end_of_file: file.fso.size as i64,
                            number_of_links: 0,
                            delete_pending: if file.delete_pending {
                                Boolean::True
                            } else {
                                Boolean::False
                            },
                            directory: if file.fso.file_type == FileType::File {
                                Boolean::False
                            } else {
                                Boolean::True
                            },
                        },
                    )),
                ),
                FileInformationClassLevel::FileAttributeTagInformation => (
                    Some(FileAttributeTagInformation::BASE_SIZE),
                    Some(FileInformationClass::FileAttributeTagInformation(
                        FileAttributeTagInformation {
                            file_attributes: if file.fso.file_type == FileType::File {
                                flags::FileAttributes::FILE_ATTRIBUTE_NORMAL
                            } else {
                                flags::FileAttributes::FILE_ATTRIBUTE_DIRECTORY
                            },
                            reparse_tag: 0,
                        },
                    )),
                ),
                _ => {
                    return Err(not_implemented_error(&format!(
                        "received unsupported FileInformationClass: {:?}",
                        req.file_info_class_lvl
                    )))
                }
            };

            Ok(Self {
                device_io_response: DeviceIoResponse::new(
                    &req.device_io_request,
                    NTSTATUS::to_u32(&io_status).unwrap(),
                ),
                length,
                buffer,
            })
        } else {
            Err(try_error(
                "if io_status != NTSTATUS::STATUS_UNSUCCESSFUL a &FileCacheObject must be provided",
            ))
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_response.encode()?);
        if let Some(length) = self.length {
            w.write_u32::<LittleEndian>(length)?;
        }
        if let Some(buffer) = &self.buffer {
            w.extend_from_slice(&buffer.encode()?);
        }
        Ok(w)
    }
}

/// 2.2.1.4.2 Device Close Request (DR_CLOSE_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/3ec6627f-9e0f-4941-a828-3fc6ed63d9e7
#[derive(Debug)]
struct DeviceCloseRequest {
    device_io_request: DeviceIoRequest,
    // Padding (32 bytes):  An array of 32 bytes. Reserved. This field can be set to any value, and MUST be ignored.
}

impl DeviceCloseRequest {
    fn decode(device_io_request: DeviceIoRequest) -> Self {
        Self { device_io_request }
    }
}

/// 2.2.1.5.2 Device Close Response (DR_CLOSE_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/0dae7031-cfd8-4f14-908c-ec06e14997b5
#[derive(Debug)]
struct DeviceCloseResponse {
    /// The CompletionId field of this header MUST match a Device I/O Request (section 2.2.1.4) message that had the MajorFunction field set to IRP_MJ_CLOSE.
    device_io_response: DeviceIoResponse,
    /// This field can be set to any value and MUST be ignored.
    padding: u32,
}
impl DeviceCloseResponse {
    fn new(device_close_request: DeviceCloseRequest, io_status: NTSTATUS) -> Self {
        Self {
            device_io_response: DeviceIoResponse::new(
                &device_close_request.device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            padding: 0,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_response.encode()?);
        w.write_u32::<LittleEndian>(self.padding)?;
        Ok(w)
    }
}

/// 2.2.3.3.11 Server Drive NotifyChange Directory Request (DR_DRIVE_NOTIFY_CHANGE_DIRECTORY_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/ed05e73d-e53e-4261-a1e1-365a70ba6512
#[derive(Debug)]
#[allow(dead_code)]
struct ServerDriveNotifyChangeDirectoryRequest {
    /// The MajorFunction field in the DR_DEVICE_IOREQUEST header MUST be set to IRP_MJ_DIRECTORY_CONTROL,
    /// and the MinorFunction field MUST be set to IRP_MN_NOTIFY_CHANGE_DIRECTORY.
    device_io_request: DeviceIoRequest,
    /// If nonzero, a change anywhere within the tree MUST trigger the notification response; otherwise, only a change in the root directory will do so.
    watch_tree: u8,
    completion_filter: flags::CompletionFilter,
    // Padding (27 bytes):  An array of 27 bytes. This field is unused and MUST be ignored.
}

#[allow(dead_code)]
impl ServerDriveNotifyChangeDirectoryRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let invalid_flags =
            || invalid_data_error("invalid flags in Server Drive NotifyChange Directory Request");

        let watch_tree = payload.read_u8()?;
        let completion_filter =
            flags::CompletionFilter::from_bits(payload.read_u32::<LittleEndian>()?)
                .ok_or_else(invalid_flags)?;

        Ok(Self {
            device_io_request,
            watch_tree,
            completion_filter,
        })
    }
}

/// 2.2.1.4.3 Device Read Request (DR_READ_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/3192516d-36a6-47c5-987a-55c214aa0441
#[derive(Debug, Clone)]
pub struct DeviceReadRequest {
    /// The MajorFunction field in this header MUST be set to IRP_MJ_READ.
    pub device_io_request: DeviceIoRequest,
    /// This field specifies the maximum number of bytes to be read from the device.
    pub length: u32,
    /// This field specifies the file offset where the read operation is performed.
    pub offset: u64,
    // Padding (20 bytes):  An array of 20 bytes. Reserved. This field can be set to any value and MUST be ignored.
}

impl DeviceReadRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        Ok(Self {
            device_io_request,
            length: payload.read_u32::<LittleEndian>()?,
            offset: payload.read_u64::<LittleEndian>()?,
        })
    }
}

/// 2.2.1.5.3 Device Read Response (DR_READ_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/d35d3f91-fc5b-492b-80be-47f483ad1dc9
struct DeviceReadResponse {
    /// The CompletionId field of this header MUST match a Device I/O Request (section 2.2.1.4) message that had the MajorFunction field set to IRP_MJ_READ.
    device_io_reply: DeviceIoResponse,
    /// Specifies the number of bytes in the ReadData field.
    length: u32,
    /// A variable-length array of bytes that specifies the output data from the read request.
    read_data: Vec<u8>,
}

impl std::fmt::Debug for DeviceReadResponse {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("DeviceReadResponse")
            .field("device_io_reply", &self.device_io_reply)
            .field("length", &self.length)
            .field("read_data", &util::vec_u8_debug(&self.read_data))
            .finish()
    }
}

impl DeviceReadResponse {
    fn new(
        device_read_request: &DeviceReadRequest,
        io_status: NTSTATUS,
        read_data: Vec<u8>,
    ) -> Self {
        let device_io_request = &device_read_request.device_io_request;

        Self {
            device_io_reply: DeviceIoResponse::new(
                device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            length: u32::try_from(read_data.len()).unwrap(),
            read_data,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.length)?;
        w.extend_from_slice(&self.read_data);
        Ok(w)
    }
}

/// 2.2.1.4.4 Device Write Request (DR_WRITE_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/2e25f0aa-a4ce-4ff3-ad62-ab6098280a3a
#[derive(Clone)]
pub struct DeviceWriteRequest {
    /// The MajorFunction field in this header MUST be set to IRP_MJ_WRITE.
    pub device_io_request: DeviceIoRequest,
    /// Number of bytes in the write_data field.
    pub length: u32,
    /// File offset at which the data must be written.
    pub offset: u64,
    /// Data to be written on the target device.
    pub write_data: Vec<u8>,
}

impl std::fmt::Debug for DeviceWriteRequest {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("DeviceWriteRequest")
            .field("device_io_request", &self.device_io_request)
            .field("length", &self.length)
            .field("offset", &self.offset)
            .field("write_data", &util::vec_u8_debug(&self.write_data))
            .finish()
    }
}

impl DeviceWriteRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let length = payload.read_u32::<LittleEndian>()?;
        let offset = payload.read_u64::<LittleEndian>()?;

        // There is a padding of 20 bytes between offset and write data so we
        // must ignore it
        payload.seek(SeekFrom::Current(20))?;

        let mut write_data = vec![0; length as usize];
        payload.read_exact(&mut write_data)?;

        Ok(Self {
            device_io_request,
            length,
            offset,
            write_data,
        })
    }
}

/// 2.2.1.5.4 Device Write Response (DR_WRITE_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/58160a47-2379-4c4a-a99d-24a1a666c02a
#[derive(Debug)]
pub struct DeviceWriteResponse {
    /// The CompletionId field of this header MUST match a Device I/O Request (section 2.2.1.4) message that had the MajorFunction field set to IRP_MJ_WRITE.
    device_io_reply: DeviceIoResponse,
    /// Number of bytes written in response to the write request.
    length: u32,
}

impl DeviceWriteResponse {
    fn new(device_io_request: &DeviceIoRequest, io_status: NTSTATUS, length: u32) -> Self {
        Self {
            device_io_reply: DeviceIoResponse::new(
                device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            length,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.length)?;
        // 1 byte padding
        w.write_u32::<LittleEndian>(0)?;
        Ok(w)
    }
}

/// 2.2.3.4.9 Client Drive Set Information Response (DR_DRIVE_SET_INFORMATION_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/16b893d5-5d8b-49d1-8dcb-ee21e7612970
#[derive(Debug)]
struct ClientDriveSetInformationResponse {
    device_io_reply: DeviceIoResponse,
    /// This field MUST be equal to the Length field in the Server Drive Set Information Request (section 2.2.3.3.9).
    length: u32,
}

impl ClientDriveSetInformationResponse {
    fn new(req: &ServerDriveSetInformationRequest, io_status: NTSTATUS) -> Self {
        Self {
            device_io_reply: DeviceIoResponse::new(
                &req.device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            length: req.set_buffer.size() as u32,
        }
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.length)?;
        // 1 byte padding
        w.write_u32::<LittleEndian>(0)?;
        Ok(w)
    }
}

/// 2.2.3.3.9 Server Drive Set Information Request (DR_DRIVE_SET_INFORMATION_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/b5d3104b-0e42-4cf8-9059-e9fe86615e5c
#[derive(Debug, Clone)]
struct ServerDriveSetInformationRequest {
    /// The MajorFunction field in the DR_DEVICE_IOREQUEST header MUST be set to IRP_MJ_SET_INFORMATION.
    device_io_request: DeviceIoRequest,
    file_information_class_level: FileInformationClassLevel,
    set_buffer: FileInformationClass,
}

impl ServerDriveSetInformationRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let file_information_class_level =
            FileInformationClassLevel::from_u32(payload.read_u32::<LittleEndian>()?)
                .ok_or_else(|| invalid_data_error("failed to read FileInformationClassLevel"))?;

        match file_information_class_level {
            FileInformationClassLevel::FileBasicInformation
            | FileInformationClassLevel::FileEndOfFileInformation
            | FileInformationClassLevel::FileDispositionInformation
            | FileInformationClassLevel::FileRenameInformation
            | FileInformationClassLevel::FileAllocationInformation => {}
            _ => {
                return Err(invalid_data_error(&format!(
                    "read invalid FileInformationClassLevel: {:?}",
                    file_information_class_level
                )))
            }
        };

        let length = payload.read_u32::<LittleEndian>()?;

        // There is a padding of 24 bytes between offset and write data so we
        // must ignore it
        payload.seek(SeekFrom::Current(24))?;

        let set_buffer =
            FileInformationClass::decode(&file_information_class_level, length, payload)?;

        Ok(Self {
            device_io_request,
            file_information_class_level,
            set_buffer,
        })
    }
}

/// 2.2.3.3.10 Server Drive Query Directory Request (DR_DRIVE_QUERY_DIRECTORY_REQ)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/458019d2-5d5a-4fd4-92ef-8c05f8d7acb1
#[derive(Debug)]
#[allow(dead_code)]
struct ServerDriveQueryDirectoryRequest {
    /// The MajorFunction field in the DR_DEVICE_IOREQUEST header MUST be set to IRP_MJ_DIRECTORY_CONTROL,
    /// and the MinorFunction field MUST be set to IRP_MN_QUERY_DIRECTORY.
    device_io_request: DeviceIoRequest,
    /// Must contain one of FileDirectoryInformation, FileFullDirectoryInformation, FileBothDirectoryInformation, FileNamesInformation
    file_info_class_lvl: FileInformationClassLevel,
    /// If the value of this field is zero, the request is for the next file in the directory that was specified in a previous
    /// Server Drive Query Directory Request. If such a file does not exist, the client MUST complete this request with STATUS_NO_MORE_FILES
    /// in the IoStatus field of the Client Drive I/O Response packet (section 2.2.3.4).  If the value of this field is non-zero and such a
    /// file does not exist, the client MUST complete this request with STATUS_NO_SUCH_FILE in the IoStatus field of the Client Drive I/O Response.
    initial_query: u8,
    /// Specifies the number of bytes in the Path field, including the null-terminator.
    path_length: u32,
    // Padding (23 bytes): An array of 23 bytes. This field is unused and MUST be ignored.
    padding: [u8; 23],
    /// A variable-length array of Unicode characters (we will store this as a regular rust String) that specifies the directory
    /// on which this operation will be performed. The Path field MUST be null-terminated. If the value of the InitialQuery field
    /// is zero, then the contents of the Path field MUST be ignored, irrespective of the value specified in the PathLength field.
    path: WindowsPath,
}

impl ServerDriveQueryDirectoryRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let file_info_class_lvl =
            FileInformationClassLevel::from_u32(payload.read_u32::<LittleEndian>()?)
                .ok_or_else(|| invalid_data_error("failed to read FileInformationClassLevel"))?;

        // These are all the FileInformationClass's supported for this message by FreeRDP
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L794
        static VALID_LEVELS: [FileInformationClassLevel; 4] = [
            FileInformationClassLevel::FileDirectoryInformation,
            FileInformationClassLevel::FileFullDirectoryInformation,
            FileInformationClassLevel::FileBothDirectoryInformation,
            FileInformationClassLevel::FileNamesInformation,
        ];

        if !VALID_LEVELS.contains(&file_info_class_lvl) {
            return Err(invalid_data_error(&format!(
                "read invalid FileInformationClassLevel: {:?}, expected one of {:?}",
                file_info_class_lvl, VALID_LEVELS,
            )));
        }

        let initial_query = payload.read_u8()?;
        let mut path_length: u32 = 0;
        let mut path = WindowsPath::from("".to_string());
        let mut padding: [u8; 23] = [0; 23];
        if initial_query != 0 {
            path_length = payload.read_u32::<LittleEndian>()?;

            // TODO(isaiah): make a payload.skip(n)
            payload.read_exact(&mut padding)?;

            // TODO(isaiah): make a from_unicode_exact
            let mut path_as_vec = vec![0u8; path_length.try_into().unwrap()];
            payload.read_exact(&mut path_as_vec)?;
            path = WindowsPath::from(util::from_unicode(path_as_vec)?);
        }

        Ok(Self {
            device_io_request,
            file_info_class_lvl,
            initial_query,
            path_length,
            padding,
            path,
        })
    }
}

/// 2.2.3.4.10 Client Drive Query Directory Response (DR_DRIVE_QUERY_DIRECTORY_RSP)
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/9c929407-a833-4893-8f20-90c984756140
#[derive(Debug)]
struct ClientDriveQueryDirectoryResponse {
    /// The CompletionId field of the DR_DEVICE_IOCOMPLETION header MUST match a Device I/O Request (section 2.2.1.4) that
    /// has the MajorFunction field set to IRP_MJ_DIRECTORY_CONTROL and the MinorFunction field set to IRP_MN_QUERY_DIRECTORY.
    device_io_reply: DeviceIoResponse,
    /// Specifies the number of bytes in the Buffer field.
    length: u32,
    /// The content of this field is based on the value of the FileInformationClass field in the Server Drive Query Directory Request
    /// message, which determines the different structures that MUST be contained in the Buffer field.
    buffer: Option<FileInformationClass>,
    // Padding (1 byte): This field is unused and MUST be ignored.
}

impl ClientDriveQueryDirectoryResponse {
    fn new(
        device_io_request: &DeviceIoRequest,
        io_status: NTSTATUS,
        buffer: Option<FileInformationClass>,
    ) -> RdpResult<Self> {
        // This match block ensures that the passed parameters are in a configuration that's
        // explicitly supported by the length calculation (below) and the self.encode() method.
        match io_status {
            NTSTATUS::STATUS_SUCCESS => {
                if buffer.is_none() {
                    return Err(invalid_data_error(
                        "a ClientDriveQueryDirectoryResponse with NTSTATUS::STATUS_SUCCESS \
                        should have Some(FileInformationClass) buffer, got None",
                    ));
                }
            }
            NTSTATUS::STATUS_NOT_SUPPORTED
            | NTSTATUS::STATUS_NO_MORE_FILES
            | NTSTATUS::STATUS_UNSUCCESSFUL => {
                if buffer.is_some() {
                    return Err(invalid_data_error(&format!(
                        "a ClientDriveQueryDirectoryResponse with NTSTATUS = {:?} \
                        should have a None buffer, got {:?}",
                        io_status, buffer,
                    )));
                }
            }
            _ => {
                return Err(invalid_data_error(&format!(
                    "received unsupported io_status for ClientDriveQueryDirectoryResponse: {:?}",
                    io_status
                )))
            }
        }

        let length = match buffer {
            Some(ref fs_information_class) => match fs_information_class {
                FileInformationClass::FileBothDirectoryInformation(fs_info_class) => {
                    fs_info_class.size()
                }
                FileInformationClass::FileFullDirectoryInformation(fs_info_class) => {
                    fs_info_class.size()
                }
                FileInformationClass::FileNamesInformation(fs_info_class) => fs_info_class.size(),
                FileInformationClass::FileDirectoryInformation(fs_info_class) => {
                    fs_info_class.size()
                }
                _ => {
                    return Err(not_implemented_error(&format!("ClientDriveQueryDirectoryResponse not implemented for fs_information_class {:?}", fs_information_class)));
                }
            },
            None => 0,
        };

        Ok(Self {
            device_io_reply: DeviceIoResponse::new(
                device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            length,
            buffer,
        })
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.length)?;
        if let Some(buffer) = &self.buffer {
            w.extend_from_slice(&buffer.encode()?);
        }
        if self.device_io_reply.io_status
            == NTSTATUS::to_u32(&NTSTATUS::STATUS_NO_MORE_FILES).unwrap()
        {
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L937
            w.write_u8(0)?;
        }

        Ok(w)
    }
}

/// 2.2.3.3.6 Server Drive Query Volume Information Request
/// https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpefs/484e622d-0e2b-423c-8461-7de38878effb
///
/// We only need to read the buffer up to the FileInformationClass to get the job done, so the rest of the fields in
/// this structure are omitted. See FreeRDP:
/// https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L464
#[derive(Debug)]
struct ServerDriveQueryVolumeInformationRequest {
    device_io_request: DeviceIoRequest,
    fs_info_class_lvl: FileSystemInformationClassLevel,
}

impl ServerDriveQueryVolumeInformationRequest {
    fn decode(device_io_request: DeviceIoRequest, payload: &mut Payload) -> RdpResult<Self> {
        let fs_info_class_lvl =
            FileSystemInformationClassLevel::from_u32(payload.read_u32::<LittleEndian>()?)
                .ok_or_else(|| {
                    invalid_data_error("failed to read FileSystemInformationClassLevel")
                })?;

        // These are all the FileInformationClass's supported for this message by FreeRDP
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L468
        static VALID_LEVELS: [FileSystemInformationClassLevel; 5] = [
            FileSystemInformationClassLevel::FileFsVolumeInformation,
            FileSystemInformationClassLevel::FileFsSizeInformation,
            FileSystemInformationClassLevel::FileFsAttributeInformation,
            FileSystemInformationClassLevel::FileFsFullSizeInformation,
            FileSystemInformationClassLevel::FileFsDeviceInformation,
        ];

        if !VALID_LEVELS.contains(&fs_info_class_lvl) {
            return Err(invalid_data_error(&format!(
                "read invalid FileInformationClassLevel: {:?}, expected one of {:?}",
                fs_info_class_lvl, VALID_LEVELS,
            )));
        }

        Ok(Self {
            device_io_request,
            fs_info_class_lvl,
        })
    }
}

/// 2.2.3.4.6 Client Drive Query Volume Information Response
#[derive(Debug)]
struct ClientDriveQueryVolumeInformationResponse {
    device_io_reply: DeviceIoResponse,
    /// Specifies the number of bytes in the Buffer field.
    length: u32,
    /// The content of this field is based on the value of the FileInformationClass field in the Server Drive Query Volume Information Request message,
    /// which determines the different structures that MUST be contained in the Buffer field.
    buffer: Option<FileSystemInformationClass>,
}

impl ClientDriveQueryVolumeInformationResponse {
    fn new(
        device_io_request: &DeviceIoRequest,
        io_status: NTSTATUS,
        buffer: Option<FileSystemInformationClass>,
    ) -> RdpResult<Self> {
        match io_status {
            NTSTATUS::STATUS_SUCCESS => {
                if buffer.is_none() {
                    return Err(invalid_data_error(
                        "a ClientDriveQueryVolumeInformationResponse with NTSTATUS::STATUS_SUCCESS \
                        should have Some(FileInformationClass) buffer, got None",
                    ));
                }
            }
            NTSTATUS::STATUS_UNSUCCESSFUL => {
                if buffer.is_some() {
                    return Err(invalid_data_error(&format!(
                        "a ClientDriveQueryVolumeInformationResponse with NTSTATUS = {:?} \
                        should have a None buffer, got {:?}",
                        io_status, buffer,
                    )));
                }
            }
            _ => {
                return Err(invalid_data_error(&format!(
                    "received unsupported io_status for ClientDriveQueryVolumeInformationResponse: {:?}",
                    io_status
                )))
            }
        }

        let length = match buffer {
            Some(ref buf) => match buf {
                FileSystemInformationClass::FileFsVolumeInformation(f) => f.size(),
                FileSystemInformationClass::FileFsSizeInformation(f) => f.size(),
                FileSystemInformationClass::FileFsAttributeInformation(f) => f.size(),
                FileSystemInformationClass::FileFsFullSizeInformation(f) => f.size(),
                FileSystemInformationClass::FileFsDeviceInformation(f) => f.size(),
            },
            None => 0,
        };

        Ok(Self {
            device_io_reply: DeviceIoResponse::new(
                device_io_request,
                NTSTATUS::to_u32(&io_status).unwrap(),
            ),
            length,
            buffer,
        })
    }

    fn encode(&self) -> RdpResult<Vec<u8>> {
        let mut w = vec![];
        w.extend_from_slice(&self.device_io_reply.encode()?);
        w.write_u32::<LittleEndian>(self.length)?;
        if let Some(buffer) = &self.buffer {
            w.extend_from_slice(&buffer.encode()?);
        }

        Ok(w)
    }
}

/// TDP handles time in milliseconds since the UNIX epoch (https://en.wikipedia.org/wiki/Unix_time),
/// whereas Windows prefers 64-bit signed integers representing the number of 100-nanosecond intervals
/// that have elapsed since January 1, 1601, Coordinated Universal Time (UTC)
/// (https://docs.microsoft.com/en-us/openspecs/windows_protocols/ms-fscc/a69cc039-d288-4673-9598-772b6083f8bf).
fn to_windows_time(tdp_time_ms: u64) -> i64 {
    // https://stackoverflow.com/a/5471380/6277051
    // https://docs.microsoft.com/en-us/windows/win32/sysinfo/converting-a-time-t-value-to-a-file-time
    let tdp_time_sec = tdp_time_ms / 1000;
    ((tdp_time_sec * 10000000) + 116444736000000000) as i64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_to_windows_time() {
        // Cross checked against
        // https://www.silisoftware.com/tools/date.php?inputdate=1655246166&inputformat=unix
        assert_eq!(to_windows_time(1655246166 * 1000), 132997197660000000);
        assert_eq!(to_windows_time(1000), 116444736010000000);
    }
}
