// Copyright 2023-2025 Buf Technologies, Inc.
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

//go:build go1.24

package httplb

import (
	"net/http"
	"time"
)

// h2cTransport provides support for the "h2c" scheme, which is used to force
// HTTP/2 over clear-text (no TLS), aka H2C. As of Go 1.24, this basically
// looks just like simpleTransport (used for "http" and "https" schemes),
// except that it must adjust the transport's supported protocols (as well as
// transform the "h2c" scheme to "http").
//
// As of Go 1,24, many more transport config options can be supported with
// H2C.
type h2cTransport struct{}

func (s h2cTransport) NewRoundTripper(_, _ string, opts TransportConfig) RoundTripperResult {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)

	transport := &http.Transport{
		Proxy:                  opts.ProxyFunc,
		GetProxyConnectHeader:  opts.ProxyConnectHeadersFunc,
		DialContext:            opts.DialFunc,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           1,
		MaxIdleConnsPerHost:    1,
		IdleConnTimeout:        opts.IdleConnTimeout,
		TLSHandshakeTimeout:    opts.TLSHandshakeTimeout,
		TLSClientConfig:        opts.TLSClientConfig,
		MaxResponseHeaderBytes: opts.MaxResponseHeaderBytes,
		ExpectContinueTimeout:  1 * time.Second,
		DisableCompression:     opts.DisableCompression,
		Protocols:              &protocols,
	}
	// no way to populate pre-warm function since http.Transport doesn't provide
	// any way to do that :(
	return RoundTripperResult{RoundTripper: transport, Scheme: "http", Close: transport.CloseIdleConnections}
}
