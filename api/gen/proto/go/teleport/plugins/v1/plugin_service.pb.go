// Copyright 2023 Gravitational, Inc
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

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: teleport/plugins/v1/plugin_service.proto

package v1

import (
	types "github.com/gravitational/teleport/api/types"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// CreatePluginRequest creates a new plugin from the given spec and initial credentials.
type CreatePluginRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Plugin is the plugin object without live credentials.
	Plugin *types.PluginV1 `protobuf:"bytes,1,opt,name=plugin,proto3" json:"plugin,omitempty"`
	// InitialCredentials are the initial credentials of the plugin.
	// In the scope of processing this request, these are exchanged for
	// "live" credentials, which are stored in the Plugin.
	InitialCredentials *types.PluginInitialCredentialsV1 `protobuf:"bytes,2,opt,name=initial_credentials,json=initialCredentials,proto3" json:"initial_credentials,omitempty"`
}

func (x *CreatePluginRequest) Reset() {
	*x = CreatePluginRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *CreatePluginRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreatePluginRequest) ProtoMessage() {}

func (x *CreatePluginRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreatePluginRequest.ProtoReflect.Descriptor instead.
func (*CreatePluginRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{0}
}

func (x *CreatePluginRequest) GetPlugin() *types.PluginV1 {
	if x != nil {
		return x.Plugin
	}
	return nil
}

func (x *CreatePluginRequest) GetInitialCredentials() *types.PluginInitialCredentialsV1 {
	if x != nil {
		return x.InitialCredentials
	}
	return nil
}

// GetPluginRequest is a request to return a plugin instance by name.
type GetPluginRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name is the name of the plugin instance.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// WithSecrets specifies whether to load associated secrets.
	WithSecrets bool `protobuf:"varint,2,opt,name=with_secrets,json=withSecrets,proto3" json:"with_secrets,omitempty"`
}

func (x *GetPluginRequest) Reset() {
	*x = GetPluginRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetPluginRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetPluginRequest) ProtoMessage() {}

func (x *GetPluginRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetPluginRequest.ProtoReflect.Descriptor instead.
func (*GetPluginRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{1}
}

func (x *GetPluginRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *GetPluginRequest) GetWithSecrets() bool {
	if x != nil {
		return x.WithSecrets
	}
	return false
}

// GetPluginsRequest is a request to return all plugin instances.
type GetPluginsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// WithSecrets specifies whether to load associated secrets.
	WithSecrets bool `protobuf:"varint,1,opt,name=with_secrets,json=withSecrets,proto3" json:"with_secrets,omitempty"`
}

func (x *GetPluginsRequest) Reset() {
	*x = GetPluginsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetPluginsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetPluginsRequest) ProtoMessage() {}

func (x *GetPluginsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetPluginsRequest.ProtoReflect.Descriptor instead.
func (*GetPluginsRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{2}
}

func (x *GetPluginsRequest) GetWithSecrets() bool {
	if x != nil {
		return x.WithSecrets
	}
	return false
}

// DeletePluginRequest is a request to delete a plugin instance by name.
type DeletePluginRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name is the name of the plugin instance.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
}

func (x *DeletePluginRequest) Reset() {
	*x = DeletePluginRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DeletePluginRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeletePluginRequest) ProtoMessage() {}

func (x *DeletePluginRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeletePluginRequest.ProtoReflect.Descriptor instead.
func (*DeletePluginRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{3}
}

func (x *DeletePluginRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

// SetPluginCredentialsRequest is a request to set credentials for an existing plugin
type SetPluginCredentialsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name is the name of the plugin instance.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Credentials are the credentials obtained after exchanging the initial credentials,
	// and after successive credential renewals.
	Credentials *types.PluginCredentialsV1 `protobuf:"bytes,2,opt,name=credentials,proto3" json:"credentials,omitempty"`
}

func (x *SetPluginCredentialsRequest) Reset() {
	*x = SetPluginCredentialsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SetPluginCredentialsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetPluginCredentialsRequest) ProtoMessage() {}

func (x *SetPluginCredentialsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetPluginCredentialsRequest.ProtoReflect.Descriptor instead.
func (*SetPluginCredentialsRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{4}
}

func (x *SetPluginCredentialsRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *SetPluginCredentialsRequest) GetCredentials() *types.PluginCredentialsV1 {
	if x != nil {
		return x.Credentials
	}
	return nil
}

// SetPluginStatusRequest is a request to set the status for an existing plugin
type SetPluginStatusRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Name is the name of the plugin instance.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Status is the plugin status.
	Status *types.PluginStatusV1 `protobuf:"bytes,2,opt,name=status,proto3" json:"status,omitempty"`
}

func (x *SetPluginStatusRequest) Reset() {
	*x = SetPluginStatusRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SetPluginStatusRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetPluginStatusRequest) ProtoMessage() {}

func (x *SetPluginStatusRequest) ProtoReflect() protoreflect.Message {
	mi := &file_teleport_plugins_v1_plugin_service_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetPluginStatusRequest.ProtoReflect.Descriptor instead.
func (*SetPluginStatusRequest) Descriptor() ([]byte, []int) {
	return file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP(), []int{5}
}

func (x *SetPluginStatusRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *SetPluginStatusRequest) GetStatus() *types.PluginStatusV1 {
	if x != nil {
		return x.Status
	}
	return nil
}

var File_teleport_plugins_v1_plugin_service_proto protoreflect.FileDescriptor

var file_teleport_plugins_v1_plugin_service_proto_rawDesc = []byte{
	0x0a, 0x28, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2f, 0x70, 0x6c, 0x75, 0x67, 0x69,
	0x6e, 0x73, 0x2f, 0x76, 0x31, 0x2f, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x5f, 0x73, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x13, 0x74, 0x65, 0x6c, 0x65,
	0x70, 0x6f, 0x72, 0x74, 0x2e, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76, 0x31, 0x1a,
	0x1b, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2f, 0x65, 0x6d, 0x70, 0x74, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x21, 0x74, 0x65,
	0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2f, 0x6c, 0x65, 0x67, 0x61, 0x63, 0x79, 0x2f, 0x74, 0x79,
	0x70, 0x65, 0x73, 0x2f, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22,
	0x92, 0x01, 0x0a, 0x13, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x27, 0x0a, 0x06, 0x70, 0x6c, 0x75, 0x67, 0x69,
	0x6e, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0f, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e,
	0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x56, 0x31, 0x52, 0x06, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e,
	0x12, 0x52, 0x0a, 0x13, 0x69, 0x6e, 0x69, 0x74, 0x69, 0x61, 0x6c, 0x5f, 0x63, 0x72, 0x65, 0x64,
	0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x21, 0x2e,
	0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x49, 0x6e, 0x69, 0x74,
	0x69, 0x61, 0x6c, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x56, 0x31,
	0x52, 0x12, 0x69, 0x6e, 0x69, 0x74, 0x69, 0x61, 0x6c, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74,
	0x69, 0x61, 0x6c, 0x73, 0x22, 0x49, 0x0a, 0x10, 0x47, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69,
	0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x21, 0x0a, 0x0c,
	0x77, 0x69, 0x74, 0x68, 0x5f, 0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0x73, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x08, 0x52, 0x0b, 0x77, 0x69, 0x74, 0x68, 0x53, 0x65, 0x63, 0x72, 0x65, 0x74, 0x73, 0x22,
	0x36, 0x0a, 0x11, 0x47, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x77, 0x69, 0x74, 0x68, 0x5f, 0x73, 0x65, 0x63,
	0x72, 0x65, 0x74, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0b, 0x77, 0x69, 0x74, 0x68,
	0x53, 0x65, 0x63, 0x72, 0x65, 0x74, 0x73, 0x22, 0x29, 0x0a, 0x13, 0x44, 0x65, 0x6c, 0x65, 0x74,
	0x65, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x12,
	0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x22, 0x6f, 0x0a, 0x1b, 0x53, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x43,
	0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x3c, 0x0a, 0x0b, 0x63, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74,
	0x69, 0x61, 0x6c, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x74, 0x79, 0x70,
	0x65, 0x73, 0x2e, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74,
	0x69, 0x61, 0x6c, 0x73, 0x56, 0x31, 0x52, 0x0b, 0x63, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69,
	0x61, 0x6c, 0x73, 0x22, 0x5b, 0x0a, 0x16, 0x53, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x12, 0x0a,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d,
	0x65, 0x12, 0x2d, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x15, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e,
	0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x56, 0x31, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x32, 0xfd, 0x03, 0x0a, 0x0d, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x53, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x12, 0x50, 0x0a, 0x0c, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x50, 0x6c, 0x75, 0x67,
	0x69, 0x6e, 0x12, 0x28, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2e, 0x70, 0x6c,
	0x75, 0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x50,
	0x6c, 0x75, 0x67, 0x69, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x67,
	0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x45,
	0x6d, 0x70, 0x74, 0x79, 0x12, 0x43, 0x0a, 0x09, 0x47, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69,
	0x6e, 0x12, 0x25, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2e, 0x70, 0x6c, 0x75,
	0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x47, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69,
	0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x0f, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73,
	0x2e, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x56, 0x31, 0x12, 0x49, 0x0a, 0x0a, 0x47, 0x65, 0x74,
	0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x12, 0x26, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f,
	0x72, 0x74, 0x2e, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x47, 0x65,
	0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a,
	0x13, 0x2e, 0x74, 0x79, 0x70, 0x65, 0x73, 0x2e, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x4c, 0x69,
	0x73, 0x74, 0x56, 0x31, 0x12, 0x50, 0x0a, 0x0c, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x50, 0x6c,
	0x75, 0x67, 0x69, 0x6e, 0x12, 0x28, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2e,
	0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74,
	0x65, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16,
	0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x12, 0x60, 0x0a, 0x14, 0x53, 0x65, 0x74, 0x50, 0x6c, 0x75,
	0x67, 0x69, 0x6e, 0x43, 0x72, 0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x12, 0x30,
	0x2e, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2e, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e,
	0x73, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x43, 0x72,
	0x65, 0x64, 0x65, 0x6e, 0x74, 0x69, 0x61, 0x6c, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62,
	0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79, 0x12, 0x56, 0x0a, 0x0f, 0x53, 0x65, 0x74, 0x50,
	0x6c, 0x75, 0x67, 0x69, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x2b, 0x2e, 0x74, 0x65,
	0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2e, 0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x2e, 0x76,
	0x31, 0x2e, 0x53, 0x65, 0x74, 0x50, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x53, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x45, 0x6d, 0x70, 0x74, 0x79,
	0x42, 0x48, 0x5a, 0x46, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x67,
	0x72, 0x61, 0x76, 0x69, 0x74, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x61, 0x6c, 0x2f, 0x74, 0x65, 0x6c,
	0x65, 0x70, 0x6f, 0x72, 0x74, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2f, 0x67, 0x6f, 0x2f, 0x74, 0x65, 0x6c, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x2f,
	0x70, 0x6c, 0x75, 0x67, 0x69, 0x6e, 0x73, 0x2f, 0x76, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x33,
}

var (
	file_teleport_plugins_v1_plugin_service_proto_rawDescOnce sync.Once
	file_teleport_plugins_v1_plugin_service_proto_rawDescData = file_teleport_plugins_v1_plugin_service_proto_rawDesc
)

func file_teleport_plugins_v1_plugin_service_proto_rawDescGZIP() []byte {
	file_teleport_plugins_v1_plugin_service_proto_rawDescOnce.Do(func() {
		file_teleport_plugins_v1_plugin_service_proto_rawDescData = protoimpl.X.CompressGZIP(file_teleport_plugins_v1_plugin_service_proto_rawDescData)
	})
	return file_teleport_plugins_v1_plugin_service_proto_rawDescData
}

var file_teleport_plugins_v1_plugin_service_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_teleport_plugins_v1_plugin_service_proto_goTypes = []interface{}{
	(*CreatePluginRequest)(nil),              // 0: teleport.plugins.v1.CreatePluginRequest
	(*GetPluginRequest)(nil),                 // 1: teleport.plugins.v1.GetPluginRequest
	(*GetPluginsRequest)(nil),                // 2: teleport.plugins.v1.GetPluginsRequest
	(*DeletePluginRequest)(nil),              // 3: teleport.plugins.v1.DeletePluginRequest
	(*SetPluginCredentialsRequest)(nil),      // 4: teleport.plugins.v1.SetPluginCredentialsRequest
	(*SetPluginStatusRequest)(nil),           // 5: teleport.plugins.v1.SetPluginStatusRequest
	(*types.PluginV1)(nil),                   // 6: types.PluginV1
	(*types.PluginInitialCredentialsV1)(nil), // 7: types.PluginInitialCredentialsV1
	(*types.PluginCredentialsV1)(nil),        // 8: types.PluginCredentialsV1
	(*types.PluginStatusV1)(nil),             // 9: types.PluginStatusV1
	(*emptypb.Empty)(nil),                    // 10: google.protobuf.Empty
	(*types.PluginListV1)(nil),               // 11: types.PluginListV1
}
var file_teleport_plugins_v1_plugin_service_proto_depIdxs = []int32{
	6,  // 0: teleport.plugins.v1.CreatePluginRequest.plugin:type_name -> types.PluginV1
	7,  // 1: teleport.plugins.v1.CreatePluginRequest.initial_credentials:type_name -> types.PluginInitialCredentialsV1
	8,  // 2: teleport.plugins.v1.SetPluginCredentialsRequest.credentials:type_name -> types.PluginCredentialsV1
	9,  // 3: teleport.plugins.v1.SetPluginStatusRequest.status:type_name -> types.PluginStatusV1
	0,  // 4: teleport.plugins.v1.PluginService.CreatePlugin:input_type -> teleport.plugins.v1.CreatePluginRequest
	1,  // 5: teleport.plugins.v1.PluginService.GetPlugin:input_type -> teleport.plugins.v1.GetPluginRequest
	2,  // 6: teleport.plugins.v1.PluginService.GetPlugins:input_type -> teleport.plugins.v1.GetPluginsRequest
	3,  // 7: teleport.plugins.v1.PluginService.DeletePlugin:input_type -> teleport.plugins.v1.DeletePluginRequest
	4,  // 8: teleport.plugins.v1.PluginService.SetPluginCredentials:input_type -> teleport.plugins.v1.SetPluginCredentialsRequest
	5,  // 9: teleport.plugins.v1.PluginService.SetPluginStatus:input_type -> teleport.plugins.v1.SetPluginStatusRequest
	10, // 10: teleport.plugins.v1.PluginService.CreatePlugin:output_type -> google.protobuf.Empty
	6,  // 11: teleport.plugins.v1.PluginService.GetPlugin:output_type -> types.PluginV1
	11, // 12: teleport.plugins.v1.PluginService.GetPlugins:output_type -> types.PluginListV1
	10, // 13: teleport.plugins.v1.PluginService.DeletePlugin:output_type -> google.protobuf.Empty
	10, // 14: teleport.plugins.v1.PluginService.SetPluginCredentials:output_type -> google.protobuf.Empty
	10, // 15: teleport.plugins.v1.PluginService.SetPluginStatus:output_type -> google.protobuf.Empty
	10, // [10:16] is the sub-list for method output_type
	4,  // [4:10] is the sub-list for method input_type
	4,  // [4:4] is the sub-list for extension type_name
	4,  // [4:4] is the sub-list for extension extendee
	0,  // [0:4] is the sub-list for field type_name
}

func init() { file_teleport_plugins_v1_plugin_service_proto_init() }
func file_teleport_plugins_v1_plugin_service_proto_init() {
	if File_teleport_plugins_v1_plugin_service_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*CreatePluginRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetPluginRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetPluginsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*DeletePluginRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SetPluginCredentialsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_teleport_plugins_v1_plugin_service_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SetPluginStatusRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_teleport_plugins_v1_plugin_service_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_teleport_plugins_v1_plugin_service_proto_goTypes,
		DependencyIndexes: file_teleport_plugins_v1_plugin_service_proto_depIdxs,
		MessageInfos:      file_teleport_plugins_v1_plugin_service_proto_msgTypes,
	}.Build()
	File_teleport_plugins_v1_plugin_service_proto = out.File
	file_teleport_plugins_v1_plugin_service_proto_rawDesc = nil
	file_teleport_plugins_v1_plugin_service_proto_goTypes = nil
	file_teleport_plugins_v1_plugin_service_proto_depIdxs = nil
}
