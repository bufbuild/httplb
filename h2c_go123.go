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

//go:build !go1.24

package httplb

import (
	"context"
	"crypto/tls"
	"net"

	"golang.org/x/net/http2"
)

// h2cTransport provides support for the "h2c" scheme, which is used to force
// HTTP/2 over clear-text (no TLS), aka H2C. Prior to Go 1.24, we must use
// features of the golang.org/x/net/http2 client implementation to make this
// work.
type h2cTransport struct{}

func (s h2cTransport) NewRoundTripper(_, _ string, opts TransportConfig) RoundTripperResult {
	// We can't support all round tripper options with H2C.
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, network, addr)
		},
		// We don't bother setting the TLS config, because h2c is plain-text only
		//TLSClientConfig:   opts.TLSClientConfig,
		MaxHeaderListSize:  uint32(opts.MaxResponseHeaderBytes),
		DisableCompression: opts.DisableCompression,
	}
	return RoundTripperResult{RoundTripper: transport, Scheme: "http", Close: transport.CloseIdleConnections}
}
