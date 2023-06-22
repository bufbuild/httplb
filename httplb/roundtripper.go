// Copyright 2023 Buf Technologies, Inc.
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

package httplb

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/http2"
)

// RoundTripperFactory is used to create "leaf" transports in the client. A leaf transport
// handles requests to a single resolved address.
type RoundTripperFactory interface {
	// New creates a new [http.RoundTripper] for requests using the given scheme to the
	// given host, configured using the given options.
	New(scheme, target string, options RoundTripperOptions) RoundTripperResult
}

// RoundTripperResult represents a "leaf" transport created by a RoundTripperFactory.
type RoundTripperResult struct {
	// RoundTripper is the actual round-tripper that handles requests.
	RoundTripper http.RoundTripper
	// Scheme, if non-empty, is the scheme to use for requests to RoundTripper. This
	// replaces the request's original scheme. This is useful when a custom scheme
	// is used to trigger a custom transport, but the underlying RoundTripper still
	// expects a non-custom scheme, such as "http" or "https".
	Scheme string
	// Close is an optional function that will be called (if non-nil) when this
	// round-tripper is no longer needed.
	Close func()
	// Prewarm is an optional function that will be called (if non-nil) to
	// eagerly establish connections and perform any other checks so that there
	// are no delays or unexpected errors incurred by the first HTTP request.
	Prewarm func(ctx context.Context, scheme, addr string) error
}

// RoundTripperOptions defines the options used to create a round-tripper.
type RoundTripperOptions struct {
	// DialFunc should be used by the round-tripper establish network connections.
	DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	// ProxyFunc should be used to control HTTP proxying behavior. If the function
	// returns a non-nil URL for a given request, that URL represents the HTTP proxy
	// that should be used.
	ProxyFunc func(*http.Request) (*url.URL, error)
	// ProxyConnectHeadersFunc should be called, if non-nil, before sending a CONNECT
	// request, to query for headers to add to that request. If it returns an
	// error, the round-trip operation should fail immediately with that error.
	ProxyConnectHeadersFunc func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)
	// MaxResponseHeaderBytes configures the maximum size of the response status
	// line and response headers.
	MaxResponseHeaderBytes int64
	// IdleConnTimeout, if non-zero, is used to expire idle network connections.
	IdleConnTimeout time.Duration
	// TLSClientConfig, is present, provides custom TLS configuration for use
	// with secure ("https") servers.
	TLSClientConfig *tls.Config
	// TLSHandshakeTimeout configures the maximum time allowed for a TLS handshake
	// to complete.
	TLSHandshakeTimeout time.Duration
	// KeepWarm indicates that the round-tripper should try to keep a ready
	// network connection open to reduce any delays in processing a request.
	KeepWarm bool
}

type simpleFactory struct{}

func (s simpleFactory) New(_, _ string, opts RoundTripperOptions) RoundTripperResult {
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
	}
	// no way to populate pre-warm function since http.Transport doesn't provide
	// any way to do that :(
	return RoundTripperResult{RoundTripper: transport, Close: transport.CloseIdleConnections}
}

type h2cFactory struct{}

func (s h2cFactory) New(_, _ string, opts RoundTripperOptions) RoundTripperResult {
	// We can't support all round tripper options with H2C.
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, network, addr)
		},
		// We don't bother setting the TLS config, because h2c is plain-text only
		//TLSClientConfig:   opts.TLSClientConfig,
		MaxHeaderListSize: uint32(opts.MaxResponseHeaderBytes),
	}
	return RoundTripperResult{RoundTripper: transport, Scheme: "http", Close: transport.CloseIdleConnections}
}
