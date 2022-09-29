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

//go:build windows
// +build windows

package webauthnwin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/duo-labs/webauthn/protocol"
	"github.com/duo-labs/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/gravitational/teleport/api/client/proto"
	wanlib "github.com/gravitational/teleport/lib/auth/webauthn"
	"github.com/gravitational/teleport/lib/auth/webauthnwin"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// TestIntegrationWithWindowsWebautn is self automated integration test that
// verifies if integration with Windows Webauthn API is working correctly.
// Note that it requires human interactions and following devices:
// - windows machine capable of windows hello
// - FIDO2 security key capable of passwordless login.
func TestIntegrationWithWindowsWebautn(t *testing.T) {
	// TODO(tobiaszheller): add some flags when to run.

	const origin = "https://goteleport.com"
	const llamaUserName = "llama"
	web, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Teleport",
		RPID:          uuid.NewString(),
		RPOrigin:      origin,
	})
	require.NoError(t, err)

	runRegistrationsUntilAttestationWorks(t, web, origin, 5 /* max */)

	t.Run("full flow using windows hello", func(t *testing.T) {
		// Given llamaUser and device with windows hello
		// When user register device
		// Then user is able to login using given device.

		// Given
		llamaUser := &fakeUser{id: []byte(uuid.NewString()), name: llamaUserName}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// When
		dialogOkCancel(t, "Please use windows hello fingerprint/pin to register and login")
		cc, sessionData, err := web.BeginRegistration(llamaUser,
			webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
				AuthenticatorAttachment: protocol.Platform,
			}),
			// Let's use direct attestion to verify if user really selected
			// security device.
			webauthn.WithConveyancePreference(protocol.PreferDirectAttestation))

		require.NoError(t, err)
		reg, err := webauthnwin.Register(ctx, origin, (*wanlib.CredentialCreation)(cc))
		require.NoError(t, err, "Register failed")
		cred, err := web.CreateCredential(llamaUser, *sessionData, registerResponseToParsedCCR(t, reg))
		require.NoError(t, err, "CreateCredential failed")
		require.Equal(t, "tpm", cred.AttestationType, "AttestationType")
		// Save credential for Login test below.
		llamaUser.credentials = append(llamaUser.credentials, *cred)

		// Then
		a, sessionData, err := web.BeginLogin(llamaUser)
		require.NoError(t, err, "BeginLogin failed")
		assertionResp, _, err := webauthnwin.Login(ctx, origin, (*wanlib.CredentialAssertion)(a), nil)
		require.NoError(t, err, "Login failed")
		_, err = web.ValidateLogin(llamaUser, *sessionData, authResponseToParsedCredentialAssertionData(t, assertionResp))
		require.NoError(t, err, "ValidatLogin failed")
	})

	t.Run("full flow using FIDO device", func(t *testing.T) {
		// Given llamaUser and device with FIDO device
		// When user register device
		// Then user is able to login.

		// Given
		llamaUser := &fakeUser{id: []byte(uuid.NewString()), name: llamaUserName}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// When
		dialogOkCancel(t, "Please use security device to register and login "+
			"(press security button on UI twice)")
		cc, sessionData, err := web.BeginRegistration(llamaUser,
			webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
				AuthenticatorAttachment: protocol.Platform,
			}),
			// Let's use direct attestion to verify if user really selected
			// security device.
			webauthn.WithConveyancePreference(protocol.PreferDirectAttestation))
		require.NoError(t, err)
		reg, err := webauthnwin.Register(ctx, origin, (*wanlib.CredentialCreation)(cc))
		require.NoError(t, err, "Register failed")
		cred, err := web.CreateCredential(llamaUser, *sessionData, registerResponseToParsedCCR(t, reg))
		require.NoError(t, err, "CreateCredential failed")
		require.Equal(t, "packed", cred.AttestationType, "AttestationType")
		// Save credential for Login test below.
		llamaUser.credentials = append(llamaUser.credentials, *cred)

		// Then
		a, sessionData, err := web.BeginLogin(llamaUser)
		require.NoError(t, err, "BeginLogin failed")
		assertionResp, _, err := webauthnwin.Login(ctx, origin, (*wanlib.CredentialAssertion)(a), nil)
		require.NoError(t, err, "Login failed")
		_, err = web.ValidateLogin(llamaUser, *sessionData, authResponseToParsedCredentialAssertionData(t, assertionResp))
		require.NoError(t, err, "ValidatLogin failed")
	})
	t.Run("register both devices and make sure you can login", func(t *testing.T) {

	})
}

func runRegistrationsUntilAttestationWorks(t *testing.T, web *webauthn.WebAuthn, origin string, max int) {
	dummyUser := &fakeUser{id: []byte(uuid.NewString()), name: "dummyUser"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < max; i++ {
		t.Logf("Checking if attestation works for %v time\n", i+1)
		dialogOkCancel(t, fmt.Sprintf("Please use windows hello fingerprint/pin to check if attestation works, attempt: %v", i+1))
		cc, sessionData, err := web.BeginRegistration(dummyUser,
			webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
				AuthenticatorAttachment: protocol.Platform,
			}),
			webauthn.WithConveyancePreference(protocol.PreferDirectAttestation))
		require.NoError(t, err)
		reg, err := webauthnwin.Register(ctx, origin, (*wanlib.CredentialCreation)(cc))
		require.NoError(t, err, "Register failed")
		cred, err := web.CreateCredential(dummyUser, *sessionData, registerResponseToParsedCCR(t, reg))
		require.NoError(t, err, "CreateCredential failed")
		if cred.AttestationType == "tpm" {
			return
		}
	}
	t.Fatal("Could not get tpm attestation")
}

func registerResponseToParsedCCR(t *testing.T, reg *proto.MFARegisterResponse) *protocol.ParsedCredentialCreationData {
	// We have to marshal and parse ccr due to an unavoidable quirk of the
	// webauthn API.
	body, err := json.Marshal(wanlib.CredentialCreationResponseFromProto(reg.GetWebauthn()))
	require.NoError(t, err)

	parsedCCR, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body))
	require.NoError(t, err, "ParseCredentialCreationResponseBody failed")
	return parsedCCR
}

func authResponseToParsedCredentialAssertionData(t *testing.T, assertionResp *proto.MFAAuthenticateResponse) *protocol.ParsedCredentialAssertionData {
	// We have to marshal and parse ccr due to an unavoidable quirk of the
	// webauthn API.
	body, err := json.Marshal(wanlib.CredentialAssertionResponseFromProto(assertionResp.GetWebauthn()))
	require.NoError(t, err)
	parsedAssertion, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(body))
	require.NoError(t, err, "ParseCredentialRequestResponseBody failed")
	return parsedAssertion
}

type fakeUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func (u *fakeUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (u *fakeUser) WebAuthnDisplayName() string {
	return u.name
}

func (u *fakeUser) WebAuthnID() []byte {
	return u.id
}

func (u *fakeUser) WebAuthnIcon() string {
	return ""
}

func (u *fakeUser) WebAuthnName() string {
	return u.name
}

var (
	moduser32               = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow = moduser32.NewProc("GetForegroundWindow")
	procMessageBoxW         = moduser32.NewProc("MessageBoxW")
)

func messageBox(hwnd syscall.Handle, caption, title string, flags uint) int {
	ret, _, _ := procMessageBoxW.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(caption))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))),
		uintptr(flags))

	return int(ret)
}

func dialogOk(caption string) bool {
	const MB_OK = 0x00000000
	hwnd, err := getForegroundWindow()
	if err != nil {
		panic(err)
	}
	// return value 1 indicates OK was selected
	return messageBox(hwnd, caption, "Teleport winwebauthn tests", MB_OK) == 1
}

func dialogOkCancel(t *testing.T, caption string) {
	const MB_OKCANCEL = 0x00000001
	hwnd, err := getForegroundWindow()
	if err != nil {
		panic(err)
	}
	v := messageBox(hwnd, caption, "Teleport winwebauthn tests", MB_OKCANCEL)
	if v == 2 {
		t.Fatal("Operation was canceled by used")
	}
	if v != 1 {
		t.Fatal("Unexpected value from dialog: ", v)
	}
}

func getForegroundWindow() (hwnd syscall.Handle, err error) {
	r0, _, err := procGetForegroundWindow.Call()
	if err != syscall.Errno(0) {
		return syscall.InvalidHandle, err
	}
	return syscall.Handle(r0), nil
}
