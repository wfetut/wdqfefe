/*
Copyright 2023 Gravitational, Inc.

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

package reverseproxy

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// Rewriter is an interface for rewriting http requests.
type Rewriter interface {
	Rewrite(*http.Request)
}

// NewHeaderRewriter creates a new HeaderRewriter middleware.
func NewHeaderRewriter() *HeaderRewriter {
	h, err := os.Hostname()
	if err != nil {
		h = "localhost"
	}
	return &HeaderRewriter{TrustForwardHeader: true, Hostname: h}
}

// HeaderRewriter is responsible for removing hop-by-hop headers and setting forwarding headers.
type HeaderRewriter struct {
	TrustForwardHeader bool
	Hostname           string
}

// clean up IP in case if it is ipv6 address and it has {zone} information in it, like "[fe80::d806:a55d:eb1b:49cc%vEthernet (vmxnet3 Ethernet Adapter - Virtual Switch)]:64692".
func ipv6fix(clientIP string) string {
	return strings.Split(clientIP, "%")[0]
}

// Rewrite request headers.
func (rw *HeaderRewriter) Rewrite(req *http.Request) {
	if !rw.TrustForwardHeader {
		for _, h := range XHeaders {
			req.Header.Del(h)
		}
	}

	if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		clientIP = ipv6fix(clientIP)

		if req.Header.Get(XRealIP) == "" {
			req.Header.Set(XRealIP, clientIP)
		}
	}

	xfProto := req.Header.Get(XForwardedProto)
	if xfProto == "" {
		if req.TLS != nil {
			req.Header.Set(XForwardedProto, "https")
		} else {
			req.Header.Set(XForwardedProto, "http")
		}
	}

	if xfPort := req.Header.Get(XForwardedPort); xfPort == "" {
		req.Header.Set(XForwardedPort, forwardedPort(req))
	}

	if xfHost := req.Header.Get(XForwardedHost); xfHost == "" && req.Host != "" {
		req.Header.Set(XForwardedHost, req.Host)
	}

	if rw.Hostname != "" {
		req.Header.Set(XForwardedServer, rw.Hostname)
	}
}

func forwardedPort(req *http.Request) string {
	if req == nil {
		return ""
	}

	if _, port, err := net.SplitHostPort(req.Host); err == nil && port != "" {
		return port
	}

	if req.Header.Get(XForwardedProto) == "https" || req.Header.Get(XForwardedProto) == "wss" {
		return "443"
	}

	if req.TLS != nil {
		return "443"
	}

	return "80"
}
