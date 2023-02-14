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

package enroll

import (
	"context"
	"os"
	"strings"

	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"

	devicepb "github.com/gravitational/teleport/api/gen/proto/go/teleport/devicetrust/v1"
	"github.com/gravitational/teleport/lib/devicetrust/native"
)

func AttemptTokenFileEnroll(ctx context.Context, devicesClient devicepb.DeviceTrustServiceClient, tokenPath string) (*devicepb.Device, error) {
	// Does the token file exist?
	tokenInfo, err := os.Stat(tokenPath)
	if err != nil {
		// TODO(codingllama): This is likely NotFound, thus likely OK too.
		return nil, trace.Wrap(err, "opening token file")
	}

	// Read the actual token.
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, trace.Wrap(err, "reading device token file")
	}
	token := strings.TrimSpace(string(tokenBytes))

	// Compare token and key timestamps to determine whether to proceed.
	cred, err := native.GetDeviceCredential()
	if err != nil {
		return nil, trace.Wrap(err, "reading device credential")
	}
	// TODO(codingllama): Compare credential and tokenFile create times.
	_ = cred
	_ = tokenInfo

	device, err := RunCeremony(ctx, devicesClient, token)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// TODO(codingllama): Attempt to delete the token file after successful
	//  enrollment? We may not have the permissions to do so.

	log.Debugf("Device Trust: successfully auto-enrolled device %v using token file %v", device.AssetTag, tokenPath)
	return device, nil
}
