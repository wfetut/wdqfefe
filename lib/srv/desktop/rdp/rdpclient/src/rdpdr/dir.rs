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

use super::consts::{
    FileInformationClassLevel, FileSystemInformationClassLevel, MinorFunction, NTSTATUS, TDP_FALSE,
};
use super::path::UnixPath;
use super::{
    Boolean, ClientDriveQueryDirectoryResponse, ClientDriveQueryInformationResponse,
    ClientDriveQueryVolumeInformationResponse, ClientDriveSetInformationResponse,
    DeviceCloseRequest, DeviceCloseResponse, DeviceControlRequest, DeviceControlResponse,
    DeviceCreateRequest, DeviceCreateResponse, DeviceIoRequest, DeviceReadRequest,
    DeviceReadResponse, DeviceWriteRequest, DeviceWriteResponse, FileBothDirectoryInformation,
    FileDirectoryInformation, FileFsAttributeInformation, FileFsDeviceInformation,
    FileFsFullSizeInformation, FileFsSizeInformation, FileFsVolumeInformation,
    FileFullDirectoryInformation, FileInformationClass, FileNamesInformation,
    FileRenameInformation, FileSystemInformationClass, ServerCreateDriveRequest,
    ServerDeviceAnnounceResponse, ServerDriveQueryDirectoryRequest,
    ServerDriveQueryInformationRequest, ServerDriveQueryVolumeInformationRequest,
    ServerDriveSetInformationRequest,
};
use crate::errors::{invalid_data_error, not_implemented_error, try_error, NTSTATUS_OK};
use crate::rdpdr::{flags, CHANNEL_NAME};
use crate::{
    FileSystemObject, FileType, Payload, SharedDirectoryAcknowledge, SharedDirectoryCreateRequest,
    SharedDirectoryCreateResponse, SharedDirectoryDeleteRequest, SharedDirectoryDeleteResponse,
    SharedDirectoryInfoRequest, SharedDirectoryInfoResponse, SharedDirectoryListRequest,
    SharedDirectoryListResponse, SharedDirectoryMoveRequest, SharedDirectoryMoveResponse,
    SharedDirectoryReadRequest, SharedDirectoryReadResponse, SharedDirectoryWriteRequest,
    SharedDirectoryWriteResponse, TdpErrCode,
};
use rdp::core::mcs;
use rdp::model::error::RdpResult;
use std::collections::HashMap;
use std::io::{Read, Write};

/// Client is a client for handling the directory sharing
/// aspects of an RDPDR client as defined in
/// https://winprotocoldoc.blob.core.windows.net/productionwindowsarchives/MS-RDPEFS/%5bMS-RDPEFS%5d.pdf.
///
/// This client is built to work in concert with the TDP File Sharing extension as defined in
/// https://github.com/gravitational/teleport/blob/master/rfd/0067-desktop-access-file-system-sharing.md.
pub struct Client {
    pub allow_directory_sharing: bool,
    /// FileId-indexed cache of FileCacheObjects.
    /// See the documentation of FileCacheObject
    /// for more detail on how this is used.
    file_cache: FileCache,
    next_file_id: u32, // used to generate file ids

    // Functions for sending tdp messages to the browser client.
    tdp_sd_acknowledge: SharedDirectoryAcknowledgeSender,
    tdp_sd_info_request: SharedDirectoryInfoRequestSender,
    tdp_sd_create_request: SharedDirectoryCreateRequestSender,
    tdp_sd_delete_request: SharedDirectoryDeleteRequestSender,
    tdp_sd_list_request: SharedDirectoryListRequestSender,
    tdp_sd_read_request: SharedDirectoryReadRequestSender,
    tdp_sd_write_request: SharedDirectoryWriteRequestSender,
    tdp_sd_move_request: SharedDirectoryMoveRequestSender,

    // CompletionId-indexed maps of handlers for tdp messages coming from the browser client.
    pending_sd_info_resp_handlers: HashMap<u32, SharedDirectoryInfoResponseHandler>,
    pending_sd_create_resp_handlers: HashMap<u32, SharedDirectoryCreateResponseHandler>,
    pending_sd_delete_resp_handlers: HashMap<u32, SharedDirectoryDeleteResponseHandler>,
    pending_sd_list_resp_handlers: HashMap<u32, SharedDirectoryListResponseHandler>,
    pending_sd_read_resp_handlers: HashMap<u32, SharedDirectoryReadResponseHandler>,
    pending_sd_write_resp_handlers: HashMap<u32, SharedDirectoryWriteResponseHandler>,
    pending_sd_move_resp_handlers: HashMap<u32, SharedDirectoryMoveResponseHandler>,
}

pub struct Config {
    pub allow_directory_sharing: bool,
    pub tdp_sd_acknowledge: SharedDirectoryAcknowledgeSender,
    pub tdp_sd_info_request: SharedDirectoryInfoRequestSender,
    pub tdp_sd_create_request: SharedDirectoryCreateRequestSender,
    pub tdp_sd_delete_request: SharedDirectoryDeleteRequestSender,
    pub tdp_sd_list_request: SharedDirectoryListRequestSender,
    pub tdp_sd_read_request: SharedDirectoryReadRequestSender,
    pub tdp_sd_write_request: SharedDirectoryWriteRequestSender,
    pub tdp_sd_move_request: SharedDirectoryMoveRequestSender,
}

impl Client {
    pub fn new(cfg: Config) -> Self {
        if cfg.allow_directory_sharing {
            debug!("creating rdpdr client with directory sharing enabled");
        } else {
            debug!("creating rdpdr client with directory sharing disabled");
        }

        Client {
            allow_directory_sharing: cfg.allow_directory_sharing,
            file_cache: FileCache::new(),
            next_file_id: 0,

            tdp_sd_acknowledge: cfg.tdp_sd_acknowledge,
            tdp_sd_info_request: cfg.tdp_sd_info_request,
            tdp_sd_create_request: cfg.tdp_sd_create_request,
            tdp_sd_delete_request: cfg.tdp_sd_delete_request,
            tdp_sd_list_request: cfg.tdp_sd_list_request,
            tdp_sd_read_request: cfg.tdp_sd_read_request,
            tdp_sd_write_request: cfg.tdp_sd_write_request,
            tdp_sd_move_request: cfg.tdp_sd_move_request,

            pending_sd_info_resp_handlers: HashMap::new(),
            pending_sd_create_resp_handlers: HashMap::new(),
            pending_sd_delete_resp_handlers: HashMap::new(),
            pending_sd_list_resp_handlers: HashMap::new(),
            pending_sd_read_resp_handlers: HashMap::new(),
            pending_sd_write_resp_handlers: HashMap::new(),
            pending_sd_move_resp_handlers: HashMap::new(),
        }
    }

    pub fn handle_device_reply(
        &mut self,
        res: ServerDeviceAnnounceResponse,
    ) -> RdpResult<Vec<Vec<u8>>> {
        let mut err_code = TdpErrCode::Nil;
        if res.result_code != NTSTATUS_OK {
            err_code = TdpErrCode::Failed;
            debug!("ServerDeviceAnnounceResponse for smartcard redirection failed with result code NTSTATUS({})", &res.result_code);
        } else {
            debug!("ServerDeviceAnnounceResponse for shared directory succeeded")
        }

        (self.tdp_sd_acknowledge)(SharedDirectoryAcknowledge {
            err_code,
            directory_id: res.device_id,
        })?;
        Ok(vec![])
    }

    pub fn process_irp_device_control(
        &mut self,
        ioctl: DeviceControlRequest,
    ) -> RdpResult<Vec<u8>> {
        // mimic FreeRDP's "no-op"
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L677-L684
        let resp = DeviceControlResponse::new(&ioctl, NTSTATUS::STATUS_SUCCESS as u32, vec![]);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    pub fn process_irp_create(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L207
        let rdp_req = ServerCreateDriveRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);

        // Send a TDP Shared Directory Info Request
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L210
        let tdp_req = SharedDirectoryInfoRequest::from(rdp_req.clone());
        (self.tdp_sd_info_request)(tdp_req)?;

        // Add a TDP Shared Directory Info Response handler to the handler cache.
        // When we receive a TDP Shared Directory Info Response with this completion_id,
        // this handler will be called.
        self.pending_sd_info_resp_handlers.insert(
            rdp_req.device_io_request.completion_id,
            Box::new(
                |cli: &mut Self, res: SharedDirectoryInfoResponse| -> RdpResult<Vec<u8>> {
                    let rdp_req = rdp_req;

                    match res.err_code {
                        TdpErrCode::Failed | TdpErrCode::AlreadyExists => {
                            return Err(try_error(&format!(
                                "received unexpected TDP error code in SharedDirectoryInfoResponse: {:?}",
                                res.err_code,
                            )));
                        }
                        TdpErrCode::Nil => {
                            // The file exists
                            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L214
                            if res.fso.file_type == FileType::Directory {
                                if rdp_req.create_disposition
                                    == flags::CreateDisposition::FILE_CREATE
                                {
                                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L221
                                    return cli.prep_device_create_response(
                                        &rdp_req,
                                        NTSTATUS::STATUS_OBJECT_NAME_COLLISION,
                                        0,
                                    );
                                }

                                if rdp_req
                                    .create_options
                                    .contains(flags::CreateOptions::FILE_NON_DIRECTORY_FILE)
                                {
                                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L227
                                    return cli.prep_device_create_response(
                                        &rdp_req,
                                        NTSTATUS::STATUS_ACCESS_DENIED,
                                        0,
                                    );
                                }
                            } else if rdp_req
                                .create_options
                                .contains(flags::CreateOptions::FILE_DIRECTORY_FILE)
                            {
                                // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L237
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_NOT_A_DIRECTORY,
                                    0,
                                );
                            }
                        }
                        TdpErrCode::DoesNotExist => {
                            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L242
                            if rdp_req
                                .create_options
                                .contains(flags::CreateOptions::FILE_DIRECTORY_FILE)
                            {
                                if rdp_req.create_disposition.intersects(
                                    flags::CreateDisposition::FILE_OPEN_IF
                                        | flags::CreateDisposition::FILE_CREATE,
                                ) {
                                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L252
                                    return cli.tdp_sd_create(
                                        rdp_req,
                                        FileType::Directory,
                                    );
                                } else {
                                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L258
                                    return cli.prep_device_create_response(
                                        &rdp_req,
                                        NTSTATUS::STATUS_NO_SUCH_FILE,
                                        0,
                                    );
                                }
                            }
                        }
                    }

                    // The actual creation of files and error mapping in FreeRDP happens here, for reference:
                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/file/file.c#L781
                    match rdp_req.create_disposition {
                        flags::CreateDisposition::FILE_SUPERSEDE => {
                            // If the file already exists, replace it with the given file. If it does not, create the given file.
                            if res.err_code == TdpErrCode::Nil {
                                return cli.tdp_sd_overwrite(rdp_req);
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.tdp_sd_create(rdp_req, FileType::File);
                            }
                        }
                        flags::CreateDisposition::FILE_OPEN => {
                            // If the file already exists, open it instead of creating a new file. If it does not, fail the request and do not create a new file.
                            if res.err_code == TdpErrCode::Nil {
                                let file_id = cli.generate_file_id();
                                cli.file_cache.insert(
                                    file_id,
                                    FileCacheObject::new(UnixPath::from(&rdp_req.path), res.fso),
                                );
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_SUCCESS,
                                    file_id,
                                );
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_NO_SUCH_FILE,
                                    0,
                                )
                            }
                        }
                        flags::CreateDisposition::FILE_CREATE => {
                            // If the file already exists, fail the request and do not create or open the given file. If it does not, create the given file.
                            if res.err_code == TdpErrCode::Nil {
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_OBJECT_NAME_COLLISION,
                                    0,
                                );
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.tdp_sd_create(rdp_req, FileType::File);
                            }
                        }
                        flags::CreateDisposition::FILE_OPEN_IF => {
                            // If the file already exists, open it. If it does not, create the given file.
                            if res.err_code == TdpErrCode::Nil {
                                let file_id = cli.generate_file_id();
                                cli.file_cache.insert(
                                    file_id,
                                    FileCacheObject::new(UnixPath::from(&rdp_req.path), res.fso),
                                );
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_SUCCESS,
                                    file_id,
                                );
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.tdp_sd_create(rdp_req, FileType::File);
                            }
                        }
                        flags::CreateDisposition::FILE_OVERWRITE => {
                            // If the file already exists, open it and overwrite it. If it does not, fail the request.
                            if res.err_code == TdpErrCode::Nil {
                                return cli.tdp_sd_overwrite(rdp_req);
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.prep_device_create_response(
                                    &rdp_req,
                                    NTSTATUS::STATUS_NO_SUCH_FILE,
                                    0,
                                )
                            }
                        }
                        flags::CreateDisposition::FILE_OVERWRITE_IF => {
                            // If the file already exists, open it and overwrite it. If it does not, create the given file.
                            if res.err_code == TdpErrCode::Nil {
                                return cli.tdp_sd_overwrite(rdp_req);
                            } else if res.err_code == TdpErrCode::DoesNotExist {
                                return cli.tdp_sd_create(rdp_req, FileType::File);
                            }
                        }
                        _ => {
                            return Err(invalid_data_error(&format!(
                                "received unknown CreateDisposition value for RDP {:?}",
                                rdp_req
                            )));
                        }
                    }

                    Err(try_error("Programmer error, this line should never be reached"))
                },
            ),
        );

        Ok(vec![])
    }

    pub fn process_irp_close(&mut self, device_io_request: DeviceIoRequest) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L236
        let rdp_req = DeviceCloseRequest::decode(device_io_request);
        debug!("received RDP: {:?}", rdp_req);
        // Remove the file from our cache
        if let Some(file) = self.file_cache.remove(rdp_req.device_io_request.file_id) {
            if file.delete_pending {
                return self.tdp_sd_delete(rdp_req, file);
            }
            return self.prep_device_close_response(rdp_req, NTSTATUS::STATUS_SUCCESS);
        }

        self.prep_device_close_response(rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL)
    }

    pub fn process_irp_query_information(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L373
        let rdp_req = ServerDriveQueryInformationRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);
        let f = self.file_cache.get(rdp_req.device_io_request.file_id);
        let code = if f.is_some() {
            NTSTATUS::STATUS_SUCCESS
        } else {
            NTSTATUS::STATUS_UNSUCCESSFUL
        };
        self.prep_query_info_response(&rdp_req, f, code)
    }

    /// The IRP_MJ_DIRECTORY_CONTROL function we support is when it's sent with minor function IRP_MN_QUERY_DIRECTORY,
    /// which is used to retrieve the contents of a directory. RDP does this by repeatedly sending
    /// IRP_MN_QUERY_DIRECTORY, expecting to retrieve the next item in the directory in each reply.
    /// (Which directory is being queried is specified by the FileId in each request).
    ///
    /// An idiosyncrasy of the protocol is that on the first IRP_MN_QUERY_DIRECTORY in a sequence, RDP expects back an
    /// entry for the "." directory, on the second call it expects an entry for the ".." directory, and on subsequent
    /// calls it expects entries for the actual contents of the directory.
    ///
    /// Once all of the directory's contents has been sent back, we alert RDP to stop sending IRP_MN_QUERY_DIRECTORY
    /// by sending it back an NTSTATUS::STATUS_NO_MORE_FILES.
    pub fn process_irp_directory_control(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        let minor_function = device_io_request.minor_function.clone();
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L650
        match minor_function {
            MinorFunction::IRP_MN_QUERY_DIRECTORY => {
                let rdp_req = ServerDriveQueryDirectoryRequest::decode(device_io_request, payload)?;
                debug!("received RDP: {:?}", rdp_req);
                let file_id = rdp_req.device_io_request.file_id;
                // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L610
                if let Some(dir) = self.file_cache.get(file_id) {
                    if dir.fso.file_type != FileType::Directory {
                        return Err(invalid_data_error("received an IRP_MN_QUERY_DIRECTORY request for a file rather than a directory"));
                    }

                    if rdp_req.initial_query == 0 {
                        // This isn't the initial query, ergo we already have this dir's contents filled in.
                        // Just send the next item.
                        return self.prep_next_drive_query_dir_response(&rdp_req);
                    }

                    // On the initial query, we need to get the list of files in this directory from
                    // the client by sending a TDP SharedDirectoryListRequest.
                    // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_file.c#L775
                    // TODO(isaiah): I'm observing that sometimes rdp_req.path will not be precisely equal to dir.path. For example, we will
                    // get a ServerDriveQueryDirectoryRequest where path == "\\*", whereas the corresponding entry in the file_cache will have
                    // path == "\\". I'm not quite sure what to do with this yet, so just leaving this as a note to self.
                    let path = dir.path.clone();

                    // Ask the client for the list of files in this directory.
                    (self.tdp_sd_list_request)(SharedDirectoryListRequest {
                        completion_id: rdp_req.device_io_request.completion_id,
                        directory_id: rdp_req.device_io_request.device_id,
                        path,
                    })?;

                    // When we get the response for that list of files...
                    self.pending_sd_list_resp_handlers.insert(
                        rdp_req.device_io_request.completion_id,
                        Box::new(
                            move |cli: &mut Self,
                                  res: SharedDirectoryListResponse|
                                  -> RdpResult<Vec<u8>> {
                                if res.err_code != TdpErrCode::Nil {
                                    // TODO(isaiah): For now any error will kill the session.
                                    // In the future, we might want to make this send back
                                    // an NTSTATUS::STATUS_UNSUCCESSFUL instead.
                                    return Err(try_error(&format!(
                                        "SharedDirectoryListRequest failed with err_code = {:?}",
                                        res.err_code
                                    )));
                                }

                                // If SharedDirectoryListRequest succeeded, move the
                                // list of FileSystemObjects that correspond to this directory's
                                // contents to its entry in the file cache.
                                if let Some(dir) = cli.file_cache.get_mut(file_id) {
                                    dir.contents = res.fso_list;
                                    // And send back the "." directory over RDP
                                    return cli.prep_next_drive_query_dir_response(&rdp_req);
                                }

                                cli.prep_file_cache_fail_drive_query_dir_response(&rdp_req)
                            },
                        ),
                    );

                    // Return nothing yet, an RDP message will be returned when the pending_sd_list_resp_handlers
                    // closure gets called.
                    return Ok(vec![]);
                }

                // File not found in cache, return a failure
                self.prep_file_cache_fail_drive_query_dir_response(&rdp_req)
            }
            MinorFunction::IRP_MN_NOTIFY_CHANGE_DIRECTORY => {
                debug!("received RDP: {:?}", device_io_request);
                debug!(
                    "ignoring IRP_MN_NOTIFY_CHANGE_DIRECTORY: {:?}",
                    device_io_request
                );
                // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L661
                Ok(vec![])
            }
            _ => {
                debug!("received RDP: {:?}", device_io_request);
                // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L663
                self.prep_drive_query_dir_response(
                    &device_io_request,
                    NTSTATUS::STATUS_NOT_SUPPORTED,
                    None,
                )
            }
        }
    }

    /// https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L442
    pub fn process_irp_query_volume_information(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        let rdp_req = ServerDriveQueryVolumeInformationRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);
        if let Some(dir) = self.file_cache.get(rdp_req.device_io_request.file_id) {
            let buffer = match rdp_req.fs_info_class_lvl {
                FileSystemInformationClassLevel::FileFsVolumeInformation => {
                    Some(FileSystemInformationClass::FileFsVolumeInformation(
                        FileFsVolumeInformation::new(dir.fso.last_modified as i64),
                    ))
                }
                FileSystemInformationClassLevel::FileFsAttributeInformation => {
                    Some(FileSystemInformationClass::FileFsAttributeInformation(
                        FileFsAttributeInformation::new(),
                    ))
                }
                FileSystemInformationClassLevel::FileFsFullSizeInformation => {
                    Some(FileSystemInformationClass::FileFsFullSizeInformation(
                        FileFsFullSizeInformation::new(),
                    ))
                }
                FileSystemInformationClassLevel::FileFsDeviceInformation => {
                    Some(FileSystemInformationClass::FileFsDeviceInformation(
                        FileFsDeviceInformation::new(),
                    ))
                }
                FileSystemInformationClassLevel::FileFsSizeInformation => Some(
                    FileSystemInformationClass::FileFsSizeInformation(FileFsSizeInformation::new()),
                ),
                _ => None,
            };

            let io_status = if buffer.is_some() {
                NTSTATUS::STATUS_SUCCESS
            } else {
                NTSTATUS::STATUS_UNSUCCESSFUL
            };

            return self.prep_query_vol_info_response(
                &rdp_req.device_io_request,
                io_status,
                buffer,
            );
        }

        // File not found in cache
        Err(invalid_data_error(&format!(
            "failed to retrieve an item from the file cache with FileId = {}",
            rdp_req.device_io_request.file_id
        )))
    }

    pub fn process_irp_read(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L268
        let rdp_req = DeviceReadRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);
        self.tdp_sd_read(rdp_req)
    }

    pub fn process_irp_write(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        let rdp_req = DeviceWriteRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);
        self.tdp_sd_write(rdp_req)
    }

    pub fn process_irp_set_information(
        &mut self,
        device_io_request: DeviceIoRequest,
        payload: &mut Payload,
    ) -> RdpResult<Vec<u8>> {
        let rdp_req = ServerDriveSetInformationRequest::decode(device_io_request, payload)?;
        debug!("received RDP: {:?}", rdp_req);

        // Determine whether to send back a STATUS_DIRECTORY_NOT_EMPTY
        // or STATUS_SUCCESS in the case of a succesful operation
        // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_main.c#L430-L431
        let io_status = match self.file_cache.get(rdp_req.device_io_request.file_id) {
            Some(file) => {
                if file.fso.file_type == FileType::Directory && file.fso.is_empty == TDP_FALSE {
                    NTSTATUS::STATUS_DIRECTORY_NOT_EMPTY
                } else {
                    NTSTATUS::STATUS_SUCCESS
                }
            }
            None => {
                // File not found in cache
                return self.prep_set_info_response(&rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL);
            }
        };

        match rdp_req.file_information_class_level {
            FileInformationClassLevel::FileRenameInformation => match rdp_req.set_buffer {
                FileInformationClass::FileRenameInformation(ref rename_info) => {
                    self.rename(rdp_req.clone(), rename_info, io_status)
                }
                _ => Err(invalid_data_error(
                    "FileInformationClass does not match FileInformationClassLevel",
                )),
            },
            FileInformationClassLevel::FileDispositionInformation => match rdp_req.set_buffer {
                FileInformationClass::FileDispositionInformation(ref info) => {
                    if let Some(file) = self.file_cache.get_mut(rdp_req.device_io_request.file_id) {
                        if !(file.fso.file_type == FileType::Directory && file.fso.is_empty == TDP_FALSE) {
                            // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_file.c#L681
                            file.delete_pending = info.delete_pending == 1;
                        }

                        return self.prep_set_info_response(&rdp_req, io_status);
                    }

                    // File not found in cache
                    self.prep_set_info_response(&rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL)
                }
                _ => Err(invalid_data_error(
                    "FileInformationClass does not match FileInformationClassLevel",
                )),

            },
            FileInformationClassLevel::FileBasicInformation
            | FileInformationClassLevel::FileEndOfFileInformation
            | FileInformationClassLevel::FileAllocationInformation => {
                // Each of these ask us to change something we don't have control over at the browser
                // level, so we just do nothing and send back a success.
                // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_file.c#L579
                self.prep_set_info_response(&rdp_req, io_status)
            }

            _ => {
                Err(not_implemented_error(&format!(
                    "support for ServerDriveSetInformationRequest with fs_info_class_lvl = {:?} is not implemented",
                    rdp_req.file_information_class_level
                )))
            }
        }
    }

    fn prep_device_create_response(
        &mut self,
        req: &DeviceCreateRequest,
        io_status: NTSTATUS,
        new_file_id: u32,
    ) -> RdpResult<Vec<u8>> {
        let resp = DeviceCreateResponse::new(req, io_status, new_file_id);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_query_info_response(
        &self,
        req: &ServerDriveQueryInformationRequest,
        file: Option<&FileCacheObject>,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        let resp = ClientDriveQueryInformationResponse::new(req, file, io_status)?;
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_device_close_response(
        &self,
        req: DeviceCloseRequest,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        let resp = DeviceCloseResponse::new(req, io_status);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    /// prep_next_drive_query_dir_response is a helper function that takes advantage of the
    /// Iterator implementation for FileCacheObject in order to respond appropriately to
    /// Server Drive Query Directory Requests as they come in.
    ///
    /// req gives us a FileId, which we use to get the FileCacheObject for the directory that
    /// this request is targeted at. We use that FileCacheObject as an iterator, grabbing the
    /// next() FileSystemObject (starting with ".", then "..", then iterating through the contents
    /// of the target directory), which we then convert to an RDP FileInformationClass for sending back
    /// to the RDP server.
    fn prep_next_drive_query_dir_response(
        &mut self,
        req: &ServerDriveQueryDirectoryRequest,
    ) -> RdpResult<Vec<u8>> {
        if let Some(dir) = self.file_cache.get_mut(req.device_io_request.file_id) {
            // Get the next FileSystemObject from the FileCacheObject for translation
            // into an RDP data structure. Because of how next() is implemented for FileCacheObject,
            // the first time this is called we will get an object for the "." directory, the second
            // time will give us "..", and then we will iterate through any files/directories stored
            // within dir.
            if let Some(fso) = dir.next() {
                let buffer = match req.file_info_class_lvl {
                    FileInformationClassLevel::FileBothDirectoryInformation => {
                        Some(FileInformationClass::FileBothDirectoryInformation(
                            FileBothDirectoryInformation::from(fso)?,
                        ))
                    }
                    FileInformationClassLevel::FileFullDirectoryInformation => {
                        Some(FileInformationClass::FileFullDirectoryInformation(
                            FileFullDirectoryInformation::from(fso)?,
                        ))
                    }
                    FileInformationClassLevel::FileNamesInformation => {
                        Some(FileInformationClass::FileNamesInformation(
                            FileNamesInformation::new(fso.name()?),
                        ))
                    }
                    FileInformationClassLevel::FileDirectoryInformation => {
                        Some(FileInformationClass::FileDirectoryInformation(
                            FileDirectoryInformation::from(fso)?,
                        ))
                    }
                    _ => {
                        return Err(invalid_data_error("received invalid FileInformationClassLevel in ServerDriveQueryDirectoryRequest"));
                    }
                };

                return self.prep_drive_query_dir_response(
                    &req.device_io_request,
                    NTSTATUS::STATUS_SUCCESS,
                    buffer,
                );
            }

            // If we reach here it means our iterator is exhausted,
            // so we send back a NTSTATUS::STATUS_NO_MORE_FILES to
            // alert RDP that we've listed all the contents of this directory.
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/winpr/libwinpr/file/generic.c#L1193
            // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L114
            return self.prep_drive_query_dir_response(
                &req.device_io_request,
                NTSTATUS::STATUS_NO_MORE_FILES,
                None,
            );
        }

        // File not found in cache
        self.prep_file_cache_fail_drive_query_dir_response(req)
    }

    fn prep_drive_query_dir_response(
        &self,
        device_io_request: &DeviceIoRequest,
        io_status: NTSTATUS,
        buffer: Option<FileInformationClass>,
    ) -> RdpResult<Vec<u8>> {
        let resp = ClientDriveQueryDirectoryResponse::new(device_io_request, io_status, buffer)?;
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_file_cache_fail_drive_query_dir_response(
        &self,
        req: &ServerDriveQueryDirectoryRequest,
    ) -> RdpResult<Vec<u8>> {
        debug!(
            "failed to retrieve an item from the file cache with FileId = {}",
            req.device_io_request.file_id
        );
        // https://github.com/FreeRDP/FreeRDP/blob/511444a65e7aa2f537c5e531fa68157a50c1bd4d/channels/drive/client/drive_main.c#L633
        self.prep_drive_query_dir_response(
            &req.device_io_request,
            NTSTATUS::STATUS_UNSUCCESSFUL,
            None,
        )
    }

    fn prep_query_vol_info_response(
        &self,
        device_io_request: &DeviceIoRequest,
        io_status: NTSTATUS,
        buffer: Option<FileSystemInformationClass>,
    ) -> RdpResult<Vec<u8>> {
        let resp =
            ClientDriveQueryVolumeInformationResponse::new(device_io_request, io_status, buffer)?;
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_read_response(
        &self,
        req: DeviceReadRequest,
        io_status: NTSTATUS,
        data: Vec<u8>,
    ) -> RdpResult<Vec<u8>> {
        let resp = DeviceReadResponse::new(&req, io_status, data);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_write_response(
        &self,
        req: DeviceIoRequest,
        io_status: NTSTATUS,
        length: u32,
    ) -> RdpResult<Vec<u8>> {
        let resp = DeviceWriteResponse::new(&req, io_status, length);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    fn prep_set_info_response(
        &mut self,
        req: &ServerDriveSetInformationRequest,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        let resp = ClientDriveSetInformationResponse::new(req, io_status);
        debug!("sending RDP: {:?}", resp);
        resp.encode()
    }

    /// Helper function for sending a TDP SharedDirectoryCreateRequest based on an
    /// RDP DeviceCreateRequest and handling the TDP SharedDirectoryCreateResponse.
    fn tdp_sd_create(
        &mut self,
        rdp_req: DeviceCreateRequest,
        file_type: FileType,
    ) -> RdpResult<Vec<u8>> {
        let tdp_req = SharedDirectoryCreateRequest {
            completion_id: rdp_req.device_io_request.completion_id,
            directory_id: rdp_req.device_io_request.device_id,
            file_type,
            path: UnixPath::from(&rdp_req.path),
        };
        (self.tdp_sd_create_request)(tdp_req)?;

        self.pending_sd_create_resp_handlers.insert(
            rdp_req.device_io_request.completion_id,
            Box::new(
                move |cli: &mut Self, res: SharedDirectoryCreateResponse| -> RdpResult<Vec<u8>> {
                    if res.err_code != TdpErrCode::Nil {
                        return cli.prep_device_create_response(
                            &rdp_req,
                            NTSTATUS::STATUS_UNSUCCESSFUL,
                            0,
                        );
                    }

                    let file_id = cli.generate_file_id();
                    cli.file_cache.insert(
                        file_id,
                        FileCacheObject::new(UnixPath::from(&rdp_req.path), res.fso),
                    );
                    cli.prep_device_create_response(&rdp_req, NTSTATUS::STATUS_SUCCESS, file_id)
                },
            ),
        );
        Ok(vec![])
    }

    /// Helper function for combining a TDP SharedDirectoryDeleteRequest
    /// with a TDP SharedDirectoryCreateRequest to overwrite a file, based
    /// on an RDP DeviceCreateRequest.
    fn tdp_sd_overwrite(&mut self, rdp_req: DeviceCreateRequest) -> RdpResult<Vec<u8>> {
        let tdp_req = SharedDirectoryDeleteRequest {
            completion_id: rdp_req.device_io_request.completion_id,
            directory_id: rdp_req.device_io_request.device_id,
            path: UnixPath::from(&rdp_req.path),
        };
        (self.tdp_sd_delete_request)(tdp_req)?;
        self.pending_sd_delete_resp_handlers.insert(
            rdp_req.device_io_request.completion_id,
            Box::new(
                |cli: &mut Self, res: SharedDirectoryDeleteResponse| -> RdpResult<Vec<u8>> {
                    match res.err_code {
                        TdpErrCode::Nil => cli.tdp_sd_create(rdp_req, FileType::File),
                        _ => cli.prep_device_create_response(
                            &rdp_req,
                            NTSTATUS::STATUS_UNSUCCESSFUL,
                            0,
                        ),
                    }
                },
            ),
        );
        Ok(vec![])
    }

    fn tdp_sd_delete(
        &mut self,
        rdp_req: DeviceCloseRequest,
        file: FileCacheObject,
    ) -> RdpResult<Vec<u8>> {
        let tdp_req = SharedDirectoryDeleteRequest {
            completion_id: rdp_req.device_io_request.completion_id,
            directory_id: rdp_req.device_io_request.device_id,
            path: file.path,
        };
        (self.tdp_sd_delete_request)(tdp_req)?;
        self.pending_sd_delete_resp_handlers.insert(
            rdp_req.device_io_request.completion_id,
            Box::new(
                |cli: &mut Self, res: SharedDirectoryDeleteResponse| -> RdpResult<Vec<u8>> {
                    let code = if res.err_code == TdpErrCode::Nil {
                        NTSTATUS::STATUS_SUCCESS
                    } else {
                        NTSTATUS::STATUS_UNSUCCESSFUL
                    };
                    cli.prep_device_close_response(rdp_req, code)
                },
            ),
        );
        Ok(vec![])
    }

    fn tdp_sd_read(&mut self, rdp_req: DeviceReadRequest) -> RdpResult<Vec<u8>> {
        if let Some(file) = self.file_cache.get(rdp_req.device_io_request.file_id) {
            let tdp_req = SharedDirectoryReadRequest {
                completion_id: rdp_req.device_io_request.completion_id,
                directory_id: rdp_req.device_io_request.device_id,
                path: file.path.clone(),
                length: rdp_req.length,
                offset: rdp_req.offset,
            };
            (self.tdp_sd_read_request)(tdp_req)?;

            self.pending_sd_read_resp_handlers.insert(
                rdp_req.device_io_request.completion_id,
                Box::new(
                    move |cli: &mut Self, res: SharedDirectoryReadResponse| -> RdpResult<Vec<u8>> {
                        match res.err_code {
                            TdpErrCode::Nil => cli.prep_read_response(
                                rdp_req,
                                NTSTATUS::STATUS_SUCCESS,
                                res.read_data,
                            ),
                            _ => cli.prep_read_response(
                                rdp_req,
                                NTSTATUS::STATUS_UNSUCCESSFUL,
                                vec![],
                            ),
                        }
                    },
                ),
            );

            return Ok(vec![]);
        }

        // File not found in cache
        self.prep_read_response(rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL, vec![])
    }

    fn tdp_sd_write(&mut self, rdp_req: DeviceWriteRequest) -> RdpResult<Vec<u8>> {
        if let Some(file) = self.file_cache.get(rdp_req.device_io_request.file_id) {
            let tdp_req = SharedDirectoryWriteRequest {
                completion_id: rdp_req.device_io_request.completion_id,
                directory_id: rdp_req.device_io_request.device_id,
                path: file.path.clone(),
                offset: rdp_req.offset,
                write_data: rdp_req.write_data,
            };
            (self.tdp_sd_write_request)(tdp_req)?;

            let device_io_request = rdp_req.device_io_request;
            self.pending_sd_write_resp_handlers.insert(
                device_io_request.completion_id,
                Box::new(
                    move |cli: &mut Self,
                          res: SharedDirectoryWriteResponse|
                          -> RdpResult<Vec<u8>> {
                        match res.err_code {
                            TdpErrCode::Nil => cli.prep_write_response(
                                device_io_request,
                                NTSTATUS::STATUS_SUCCESS,
                                res.bytes_written,
                            ),
                            _ => cli.prep_write_response(
                                device_io_request,
                                NTSTATUS::STATUS_UNSUCCESSFUL,
                                0,
                            ),
                        }
                    },
                ),
            );

            return Ok(vec![]);
        }

        // File not found in cache
        self.prep_write_response(rdp_req.device_io_request, NTSTATUS::STATUS_UNSUCCESSFUL, 0)
    }

    fn tdp_sd_move(
        &mut self,
        rdp_req: ServerDriveSetInformationRequest,
        rename_info: &FileRenameInformation,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        if let Some(file) = self.file_cache.get(rdp_req.device_io_request.file_id) {
            (self.tdp_sd_move_request)(SharedDirectoryMoveRequest {
                completion_id: rdp_req.device_io_request.completion_id,
                directory_id: rdp_req.device_io_request.device_id,
                original_path: file.path.clone(),
                new_path: UnixPath::from(&rename_info.file_name),
            })?;

            self.pending_sd_move_resp_handlers.insert(
                rdp_req.device_io_request.completion_id,
                Box::new(
                    move |cli: &mut Self, res: SharedDirectoryMoveResponse| -> RdpResult<Vec<u8>> {
                        if res.err_code != TdpErrCode::Nil {
                            return cli
                                .prep_set_info_response(&rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL);
                        }

                        cli.prep_set_info_response(&rdp_req, io_status)
                    },
                ),
            );

            return Ok(vec![]);
        }

        // File not found in cache
        self.prep_set_info_response(&rdp_req, NTSTATUS::STATUS_UNSUCCESSFUL)
    }

    fn rename(
        &mut self,
        rdp_req: ServerDriveSetInformationRequest,
        rename_info: &FileRenameInformation,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        // https://github.com/FreeRDP/FreeRDP/blob/dfa231c0a55b005af775b833f92f6bcd30363d77/channels/drive/client/drive_file.c#L709
        match rename_info.replace_if_exists {
            Boolean::True => self.rename_replace_if_exists(rdp_req, rename_info, io_status),
            Boolean::False => self.rename_dont_replace_if_exists(rdp_req, rename_info, io_status),
        }
    }

    fn rename_replace_if_exists(
        &mut self,
        rdp_req: ServerDriveSetInformationRequest,
        rename_info: &FileRenameInformation,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        // If replace_if_exists is true, we can just send a TDP SharedDirectoryMoveRequest,
        // which works like the unix `mv` utility (meaning it will automatically replace if exists).
        self.tdp_sd_move(rdp_req, rename_info, io_status)
    }

    fn rename_dont_replace_if_exists(
        &mut self,
        rdp_req: ServerDriveSetInformationRequest,
        rename_info: &FileRenameInformation,
        io_status: NTSTATUS,
    ) -> RdpResult<Vec<u8>> {
        let new_path = UnixPath::from(&rename_info.file_name);
        // If replace_if_exists is false, first check if the new_path exists.
        (self.tdp_sd_info_request)(SharedDirectoryInfoRequest {
            completion_id: rdp_req.device_io_request.completion_id,
            directory_id: rdp_req.device_io_request.device_id,
            path: new_path,
        })?;

        let rename_info = (*rename_info).clone();
        self.pending_sd_info_resp_handlers.insert(
            rdp_req.device_io_request.completion_id,
            Box::new(
                move |cli: &mut Self, res: SharedDirectoryInfoResponse| -> RdpResult<Vec<u8>> {
                    if res.err_code == TdpErrCode::DoesNotExist {
                        // If the file doesn't already exist, send a move request.
                        return cli.tdp_sd_move(rdp_req, &rename_info, io_status);
                    }
                    // If it does, send back a name collision error, as is done in FreeRDP.
                    cli.prep_set_info_response(&rdp_req, NTSTATUS::STATUS_OBJECT_NAME_COLLISION)
                },
            ),
        );

        Ok(vec![])
    }

    pub fn handle_tdp_sd_info_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryInfoResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP SharedDirectoryInfoResponse: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_info_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_create_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryCreateResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP SharedDirectoryCreateResponse: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_create_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_delete_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryDeleteResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP SharedDirectoryDeleteResponse: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_delete_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_list_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryListResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP SharedDirectoryListResponse: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_list_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_read_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryReadResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_read_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_write_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryWriteResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_write_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    pub fn handle_tdp_sd_move_response<S: Read + Write>(
        &mut self,
        res: SharedDirectoryMoveResponse,
        mcs: &mut mcs::Client<S>,
    ) -> RdpResult<()> {
        debug!("received TDP SharedDirectoryMoveResponse: {:?}", res);
        if let Some(tdp_resp_handler) = self
            .pending_sd_move_resp_handlers
            .remove(&res.completion_id)
        {
            let rdp_responses = tdp_resp_handler(self, res)?;
            let chan = &CHANNEL_NAME.to_string();
            for resp in rdp_responses {
                mcs.write(chan, resp)?;
            }
            return Ok(());
        }

        Err(try_error(&format!(
            "received invalid completion id: {}",
            res.completion_id
        )))
    }

    fn generate_file_id(&mut self) -> u32 {
        self.next_file_id = self.next_file_id.wrapping_add(1);
        self.next_file_id
    }
}

struct FileCache {
    cache: HashMap<u32, FileCacheObject>,
}

impl FileCache {
    fn new() -> Self {
        Self {
            cache: HashMap::new(),
        }
    }

    /// Insert a FileCacheObject into the file cache.
    ///
    /// If the file cache did not have this key present, [`None`] is returned.
    ///
    /// If the file cache did have this key present, the value is updated, and the old
    /// value is returned. The key is not updated, though; this matters for
    /// types that can be `==` without being identical.
    fn insert(&mut self, file_id: u32, file: FileCacheObject) -> Option<FileCacheObject> {
        self.cache.insert(file_id, file)
    }

    /// Retrieves a FileCacheObject from the file cache,
    /// without removing it from the cache.
    fn get(&self, file_id: u32) -> Option<&FileCacheObject> {
        self.cache.get(&file_id)
    }
    /// Retrieves a mutable FileCacheObject from the file cache,
    /// without removing it from the cache.
    fn get_mut(&mut self, file_id: u32) -> Option<&mut FileCacheObject> {
        self.cache.get_mut(&file_id)
    }

    /// Retrieves a FileCacheObject from the file cache,
    /// removing it from the cache.
    fn remove(&mut self, file_id: u32) -> Option<FileCacheObject> {
        self.cache.remove(&file_id)
    }
}

/// FileCacheObject is an in-memory representation of
/// of a file or directory holding the metadata necessary
/// for RDP drive redirection. They are stored in map indexed
/// by their RDP FileId.
///
/// The lifecycle for a FileCacheObject is a function of the
/// MajorFunction of RDP DeviceIoRequests:
///
/// | Sequence | MajorFunction | results in                                               |
/// | -------- | ------------- | ---------------------------------------------------------|
/// | 1        | IRP_MJ_CREATE | A new FileCacheObject is created and assigned a FileId   |
/// | -------- | ------------- | ---------------------------------------------------------|
/// | 2        | <other>       | The FCO is retrieved from the cache by the FileId in the |
/// |          |               | DeviceIoRequest and metadata is used to craft a response |
/// | -------- | ------------- | ---------------------------------------------------------|
/// | 3        | IRP_MJ_CLOSE  | The FCO is deleted from the cache                        |
/// | -------- | ------------- | ---------------------------------------------------------|
#[derive(Debug, Clone)]
pub struct FileCacheObject {
    path: UnixPath,
    pub delete_pending: bool,
    /// The FileSystemObject pertaining to the file or directory at path.
    pub fso: FileSystemObject,
    /// A vector of the contents of the directory at path.
    contents: Vec<FileSystemObject>,

    /// Book-keeping variable, see Iterator implementation
    contents_i: usize,
    /// Book-keeping variable, see Iterator implementation
    dot_sent: bool,
    /// Book-keeping variable, see Iterator implementation
    dotdot_sent: bool,
}

impl FileCacheObject {
    fn new(path: UnixPath, fso: FileSystemObject) -> Self {
        Self {
            path,
            delete_pending: false,
            fso,
            contents: Vec::new(),

            contents_i: 0,
            dot_sent: false,
            dotdot_sent: false,
        }
    }
}

/// FileCacheObject is used as an iterator for the implementation of
/// IRP_MJ_DIRECTORY_CONTROL, which requires that we iterate through
/// all the files of a directory one by one. In this case, the directory
/// is the FileCacheObject itself, with it's own fso field representing
/// the directory, and its contents being represented by FileSystemObject's
/// in its contents field.
///
/// We account for an idiosyncrasy of the RDP protocol here: when fielding an
/// IRP_MJ_DIRECTORY_CONTROL, RDP first expects to receive an entry for the "."
/// directory, and next an entry for the ".." directory. Only after those two
/// directories have been sent do we begin sending the actual contents of this
/// directory (the contents field). (This is why we maintain dot_sent and dotdot_sent
/// fields on each FileCacheObject)
///
/// Note that this implementation only makes sense in the case that this FileCacheObject
/// is itself a directory (fso.file_type == FileType::Directory). We leave it up to the
/// caller to ensure iteration makes sense in the given context that it's used.
impl Iterator for FileCacheObject {
    type Item = FileSystemObject;

    fn next(&mut self) -> Option<Self::Item> {
        // On the first call to next, return the "." directory
        if !self.dot_sent {
            self.dot_sent = true;
            Some(FileSystemObject {
                last_modified: self.fso.last_modified,
                size: self.fso.size,
                file_type: self.fso.file_type,
                is_empty: TDP_FALSE,
                path: UnixPath::from(".".to_string()),
            })
        } else if !self.dotdot_sent {
            // On the second call to next, return the ".." directory
            self.dotdot_sent = true;
            Some(FileSystemObject {
                last_modified: self.fso.last_modified,
                size: 0,
                file_type: FileType::Directory,
                is_empty: TDP_FALSE,
                path: UnixPath::from("..".to_string()),
            })
        } else {
            // "." and ".." have been sent, now start iterating through
            // the actual contents of the directory
            if self.contents_i < self.contents.len() {
                let i = self.contents_i;
                self.contents_i += 1;
                return Some(self.contents[i].clone());
            }
            None
        }
    }
}

type SharedDirectoryAcknowledgeSender = Box<dyn Fn(SharedDirectoryAcknowledge) -> RdpResult<()>>;
type SharedDirectoryInfoRequestSender = Box<dyn Fn(SharedDirectoryInfoRequest) -> RdpResult<()>>;
type SharedDirectoryCreateRequestSender =
    Box<dyn Fn(SharedDirectoryCreateRequest) -> RdpResult<()>>;
type SharedDirectoryDeleteRequestSender =
    Box<dyn Fn(SharedDirectoryDeleteRequest) -> RdpResult<()>>;
type SharedDirectoryListRequestSender = Box<dyn Fn(SharedDirectoryListRequest) -> RdpResult<()>>;
type SharedDirectoryReadRequestSender = Box<dyn Fn(SharedDirectoryReadRequest) -> RdpResult<()>>;
type SharedDirectoryWriteRequestSender = Box<dyn Fn(SharedDirectoryWriteRequest) -> RdpResult<()>>;
type SharedDirectoryMoveRequestSender = Box<dyn Fn(SharedDirectoryMoveRequest) -> RdpResult<()>>;

type SharedDirectoryInfoResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryInfoResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryCreateResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryCreateResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryDeleteResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryDeleteResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryListResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryListResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryReadResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryReadResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryWriteResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryWriteResponse) -> RdpResult<Vec<u8>>>;
type SharedDirectoryMoveResponseHandler =
    Box<dyn FnOnce(&mut Client, SharedDirectoryMoveResponse) -> RdpResult<Vec<u8>>>;
