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

// Package desktop implements Desktop Access services, like
// windows_desktop_access.
package desktop

import "github.com/gravitational/teleport/api/constants"

const (
	// SNISuffix is the server name suffix used during SNI to specify the
	// target desktop to connect to. The client (proxy_service) will use SNI
	// like "${windowsUsername}+${UUID}.desktop.teleport.cluster.local" to pass
	// the UUID of the desktop and the target windows username for the session.
	//
	// todo(isaiah): explain why ${windowsUnsername}-${UUID} was chosen:
	// - golang x509 package doesn't play nicely with doing this with a double wildcard like *.*.desktop.teleport.cluster.local
	// - windows usernames can't contain -, so we can use that as a separator: https://serverfault.com/questions/604547/rules-for-active-directory-user-name-string todo(isaiah): not true for "-"
	//
	// I'm thinking we should get rid of this little hack and instead just create a little custom protocol
	// where we send the UUID and windows username byte by byte.
	SNISuffix = ".desktop." + constants.APIDomain
	// WildcardServiceDNS is a wildcard DNS address to embed in the service TLS
	// certificate for SNI-based routing. Note: this is different from ALPN SNI
	// routing on the proxy.
	WildcardServiceDNS = "*" + SNISuffix
)
