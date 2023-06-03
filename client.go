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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

//nolint:gochecknoglobals
var (
	defaultDialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
)

// ClientOption is an option used to customize the behavior of an HTTP client.
type ClientOption interface {
	apply(*clientOptions)
}

// WithRootContext configures the root context used for any background
// goroutines that an HTTP client may create. If not specified,
// [context.Background] is used.
//
// If the given context is cancelled (or times out), many functions of the
// HTTP client may fail to operate correctly. It should only be cancelled
// after the HTTP client is no longer in use, and may be used to eagerly
// free any associated resources.
func WithRootContext(ctx context.Context) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.rootCtx = ctx
	})
}

// WithProxy configures how the HTTP client interacts with HTTP proxies for
// reaching remote hosts.
//
// The given proxyFunc returns the URL of a proxy server to use for the
// given HTTP request. If no proxy should be used, it should return nil, nil.
// If an error is returned, the request fails immediately with that error.
// If a nil proxyFunc is provided, no proxy will ever be used. This can be
// useful to disable proxies. If this function is set to nil or no
// WithProxy option is provided, [http.ProxyFromEnvironment] will be used
// as the proxyFunc. (Also see WithNoProxy.)
//
// The given onProxyConnectFunc, if non-nil, provides a way to examine the
// response from the proxy for a CONNECT request. If the onProxyConnectFunc
// returns an error, the request will fail immediately with that error.
//
// The given proxyHeadersFunc, if non-nil, provides a way to supply extra
// request headers to the proxy for a CONNECT request. The target provided
// to this function is the "host:port" to which to connect. If no extra
// headers should be added to the request, the function should return nil, nil.
// If the function returns an error, the request will fail immediately with
// that error.
func WithProxy(
	proxyFunc func(*http.Request) (*url.URL, error),
	proxyHeadersFunc func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error),
) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.proxyFunc = proxyFunc
		opts.proxyHeadersFunc = proxyHeadersFunc
	})
}

// WithNoProxy returns an option that disables use of HTTP proxies.
func WithNoProxy() ClientOption {
	return WithProxy(
		// never use a proxy
		func(*http.Request) (*url.URL, error) { return nil, nil },
		nil)
}

// WithRedirects configures how the HTTP client handles redirect responses.
// If no such option is provided, the client will not follow any redirects.
func WithRedirects(redirectFunc RedirectFunc) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.redirectFunc = redirectFunc
	})
}

// RedirectFunc is a function that advises an HTTP client on whether to
// follow a redirect. The given req is the redirected request, based on
// the server's previous status code and "Location" header, and the given
// via is the set of requests already issued, each resulting in a redirect.
// The via slice is sorted oldest first, so the first element is the always
// the original request and the last element is the latest redirect.
//
// See FollowRedirects.
type RedirectFunc func(req *http.Request, via []*http.Request) error

// FollowRedirects is a helper to create a RedirectFunc that will follow
// up to the given number of redirects. If a request sequence results in more
// redirects than the given limit, the request will fail.
func FollowRedirects(limit int) RedirectFunc {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > limit {
			return fmt.Errorf("too many redirects (> %d)", limit)
		}
		return nil
	}
}

// WithDefaultTimeout limits requests that otherwise have no timeout to
// the given timeout. Unlike WithRequestTimeout, if the request's context
// already has a deadline, then no timeout is applied. Otherwise, the
// given timeout is used and applies to the entire duration of the request,
// from sending the first request byte to receiving the last response byte.
func WithDefaultTimeout(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.defaultTimeout = duration
		opts.requestTimeout = 0
	})
}

// WithRequestTimeout limits all requests to the given timeout. This time
// is the entire duration of the request, including sending the request,
// writing the request body, waiting for a response, and consuming the
// response body.
func WithRequestTimeout(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.defaultTimeout = 0
		opts.requestTimeout = duration
	})
}

// WithDialer configures the HTTP client to use the given function to
// establish network connections. If no WithDialer option is provided,
// a default [net.Dialer] is used that uses a 30-second dial timeout and
// configures the connection to use TCP keep-alive every 30 seconds.
func WithDialer(dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.dialFunc = dialFunc
	})
}

// WithTLSConfig adds custom TLS configuration to the HTTP client. The
// given config is used when using TLS to communicate with servers. The
// given timeout is applied to the TLS handshake step. If the given timeout
// is zero or no WithTLSConfig option is used, a default timeout of 10
// seconds will be used.
func WithTLSConfig(config *tls.Config, handshakeTimeout time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.tlsClientConfig = config
		opts.tlsHandshakeTimeout = handshakeTimeout
	})
}

// WithMaxResponseHeaderBytes configures the maximum size of response headers
// to consume. If zero or if no WithMaxResponseHeaderBytes option is used, the
// HTTP client will default to a 1 MB limit (2^20 bytes).
func WithMaxResponseHeaderBytes(limit int) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.maxResponseHeaderBytes = int64(limit)
	})
}

// WithIdleConnectionTimeout configures a timeout for how long an idle
// connection will remain open. If zero or no WithIdleConnectionTimeout
// option is used, idle connections will be left open indefinitely. If
// backend servers or intermediary proxies/load balancers place time
// limits on idle connections, this should be configured to be less
// than that time limit, to prevent the client from trying to use a
// connection could be concurrently closed by a server for being idle
// for too long.
func WithIdleConnectionTimeout(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.idleConnTimeout = duration
	})
}

// WithIdleTransportTimeout configures a timeout for how long an idle
// transport will remain available. Transports are created per target
// host. When a transport is closed, all underlying connections are
// also closed.
//
// This differs from WithIdleConnectionTimeout in that it is for
// managing client resources, to prevent the underlying set of transports
// from growing too large if the HTTP client is used for dynamic outbound
// requests. Whereas WithIdleConnectionTimeout is to coordinate with
// servers that close idle connections.
//
// If zero or no WithIdleTransportTimeout option is used, a default of
// 15 minutes will be used.
//
// To prevent some transports from being closed due to being idle, use
// WithKeepWarmTargets.
func WithIdleTransportTimeout(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.idleTransportTimeout = duration
	})
}

// WithKeepWarmTargets prevents the given targets from being closed even
// if idle. Transports for these targets are immortal, to make sure there is
// always a "warm" transport available for sending a request.
//
// Each target must be in "scheme://host:port" format. If the scheme is
// omitted, "http" is assumed. The "host:port" portion should exactly match
// HTTP requests used with the client. So if those requests omit the port
// then so should the target used with this function.
func WithKeepWarmTargets(targets ...string) ClientOption {
	// TODO: validate targets here? if invalid, return error? panic?
	return clientOptionFunc(func(opts *clientOptions) {
		opts.warmTargets = append(opts.warmTargets, targets...)
	})
}

// NewClient returns a new HTTP client that uses the given options.
func NewClient(options ...ClientOption) *http.Client {
	var opts clientOptions
	for _, opt := range options {
		opt.apply(&opts)
	}
	opts.applyDefaults()
	return &http.Client{
		Transport:     newTransport(&opts),
		CheckRedirect: opts.redirectFunc,
	}
}

// Close closes the given HTTP client, releasing any resources and stopping
// any associated background goroutines.
//
// If the given client was not created using NewClient, this will return an
// error.
func Close(client *http.Client) error {
	transport, ok := client.Transport.(*mainTransport)
	if !ok {
		return errors.New("client not created by this package")
	}
	transport.close()
	return nil
}

// Prewarm pre-warms the given HTTP client, making sure that any targets
// configured via WithKeepWarmTargets have been warmed up. This ensures that
// relevant addresses are resolved, any health checks performed, connections
// possibly already established, etc.
//
// If the given client was not created using NewClient, this will return an
// error.
//
// The given context should usually have a timeout, so that this step can
// fail if it takes too long. Most warming errors manifest as excessive
// delays vs. outright failure because the background machinery that gets
// transports ready will keep re-trying instead of giving up and failing
// fast.
func Prewarm(ctx context.Context, client *http.Client) error {
	transport, ok := client.Transport.(*mainTransport)
	if !ok {
		return errors.New("client not created by this package")
	}
	return transport.prewarm(ctx)
}

type clientOptionFunc func(*clientOptions)

func (f clientOptionFunc) apply(opts *clientOptions) {
	f(opts)
}

type clientOptions struct {
	rootCtx                context.Context //nolint:containedctx
	dialFunc               func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyFunc              func(*http.Request) (*url.URL, error)
	proxyHeadersFunc       func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)
	redirectFunc           func(req *http.Request, via []*http.Request) error
	maxResponseHeaderBytes int64
	idleConnTimeout        time.Duration
	idleTransportTimeout   time.Duration
	warmTargets            []string
	tlsClientConfig        *tls.Config
	tlsHandshakeTimeout    time.Duration
	defaultTimeout         time.Duration
	requestTimeout         time.Duration
}

func (opts *clientOptions) applyDefaults() {
	if opts.rootCtx == nil {
		opts.rootCtx = context.Background()
	}
	if opts.dialFunc == nil {
		opts.dialFunc = defaultDialer.DialContext
	}
	if opts.proxyFunc == nil {
		opts.proxyFunc = http.ProxyFromEnvironment
	}
	if opts.maxResponseHeaderBytes == 0 {
		opts.maxResponseHeaderBytes = 1 << 20
	}
	if opts.tlsHandshakeTimeout == 0 {
		opts.tlsHandshakeTimeout = 10 * time.Second
	}
	if opts.idleTransportTimeout == 0 {
		opts.idleTransportTimeout = 15 * time.Minute
	}
}
