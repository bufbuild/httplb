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

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/picker"
	"github.com/bufbuild/httplb/resolver"
)

//nolint:gochecknoglobals
var (
	defaultDialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	defaultNameTTL  = 5 * time.Minute
	defaultResolver = resolver.NewDNSResolver(net.DefaultResolver, "ip", defaultNameTTL)
)

// Client is an HTTP client that supports configurable client-side load
// balancing, name resolution, and subsetting. It embeds the standard library's
// *[http.Client] and exposes some additional operations.
//
// If working with a library that requires an *[http.Client], use the embedded
// Client. If working with a library that requires an [http.RoundTripper], use
// the Transport field.
type Client struct {
	*http.Client
}

// NewClient constructs a new HTTP client optimized for server-to-server
// communication. By default, the client re-resolves addresses every 5 minutes
// and uses a round-robin load balancing policy.
func NewClient(options ...ClientOption) *Client {
	var opts clientOptions
	for _, opt := range options {
		opt.applyToClient(&opts)
	}
	opts.applyDefaults()
	return &Client{
		Client: &http.Client{
			Transport:     newTransport(&opts),
			CheckRedirect: opts.redirectFunc,
		},
	}
}

// Close the HTTP client, releasing any resources and stopping any associated
// background goroutines.
func (c *Client) Close() error {
	transport, ok := c.Transport.(*mainTransport)
	if !ok {
		return errors.New("client not created by httplb.NewClient")
	}
	transport.close()
	return nil
}

// prewarm pre-warms the HTTP client, making sure that any targets configured
// via WithAllowBackendTarget have been warmed up. This ensures that relevant
// addresses are resolved, any health checks performed, connections eagerly
// established where possible, etc.
//
// The given context should usually have a timeout, so that this step can
// fail if it takes too long. Most warming errors manifest as excessive
// delays vs. outright failure because the background machinery that gets
// transports ready will keep re-trying instead of giving up and failing
// fast.
func (c *Client) prewarm(ctx context.Context) error {
	// TODO: Expose prewarm capability from this package and export this?
	//       For now, this is just used from tests.
	transport, ok := c.Transport.(*mainTransport)
	if !ok {
		return errors.New("client not created by httplb.NewClient")
	}
	return transport.prewarm(ctx)
}

// ClientOption is an option used to customize the behavior of an HTTP client
// that is created via NewClient.
type ClientOption interface {
	applyToClient(*clientOptions)
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
// To prevent transports from being closed due to being idle, set an
// arbitrarily large timeout (i.e. math.MaxInt64) or use WithAllowBackendTarget.
func WithIdleTransportTimeout(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.idleTransportTimeout = duration
	})
}

// WithRoundTripperMaxLifetime configures a limit for how long a single
// round tripper (or "leaf" transport) will be used. If no option is given,
// round trippers are retained indefinitely, until their parent transport
// is closed (which can happen if the transport is idle; see
// WithIdleTransportTimeout). When a round tripper reaches its maximum
// lifetime, a new one is first created to replace it. Any in-progress
// operations are allowed to finish, but no new operations will use it.
//
// This function is mainly useful when the target host is a layer-4 proxy.
// In this situation, it is possible for multiple round trippers to all
// get connected to the same backend host, resulting in poor load
// distribution. With a lifetime limit, a single round tripper will get
// "recycled", and its replacement is likely to be connected to a
// different backend host. So when a transport gets into a scenario where
// it has poor backend diversity, the lifetime limit allows it to self-heal.
func WithRoundTripperMaxLifetime(duration time.Duration) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.roundTripperMaxLifetime = duration
	})
}

// WithTransport returns an option that uses a custom transport to create
// [http.RoundTripper] instances for the given URL scheme. This allows
// one to override the default implementations for "http", "https", and
// "h2c" schemes and also allows one to support custom URL schemes that
// map to these custom transports.
func WithTransport(scheme string, transport Transport) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		if opts.schemes == nil {
			opts.schemes = map[string]Transport{}
		}
		opts.schemes[scheme] = transport
	})
}

// WithAllowBackendTarget configures the client to only allow requests to the
// given target (identified by URL scheme and host:port).
//
// When configured this way, connections to the backend target will be kept warm,
// meaning that associated transports will not be closed due to inactivity,
// regardless of the idle transport timeout configuration.
//
// The scheme and host:port given must match those of associated requests. So
// if requests omit the port from the URL (for example), then the hostPort
// given here should also omit the port.
func WithAllowBackendTarget(scheme, hostPort string) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		dest := target{scheme: scheme, hostPort: hostPort}
		if dest.scheme == "" {
			dest.scheme = "http"
		}
		opts.allowedTarget = &dest
	})
}

// WithResolver configures the HTTP client to use the given resolver, which
// is how hostnames are resolved into individual addresses for the underlying
// connections.
//
// If not provided, the default resolver will resolve A and AAAA records
// using net.DefaultResolver.
func WithResolver(res resolver.Resolver) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.resolver = res
	})
}

// WithPicker configures the HTTP client to use the given function to create
// picker instances. The factory is invoked to create a new picker every time
// the set of usable connections changes for a target.
func WithPicker(newPicker func(prev picker.Picker, allConns conn.Conns) picker.Picker) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.newPicker = newPicker
	})
}

// WithHealthChecks configures the HTTP client to use the given health
// checker. This provides details about which resolved addresses are
// healthy or not.
//
// If no such option is given, then no health checking is done and all
// connections will be considered healthy.
func WithHealthChecks(checker health.Checker) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.healthChecker = checker
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
// The given proxyConnectHeadersFunc, if non-nil, provides a way to supply
// extra request headers to the proxy for a CONNECT request. The target
// provided to this function is the "host:port" to which to connect. If no
// extra headers should be added to the request, the function should return
// nil, nil. If the function returns an error, the request will fail
// immediately with that error.
func WithProxy(
	proxyFunc func(*http.Request) (*url.URL, error),
	proxyConnectHeadersFunc func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error),
) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.proxyFunc = proxyFunc
		opts.proxyConnectHeadersFunc = proxyConnectHeadersFunc
	})
}

// WithNoProxy returns an option that disables use of HTTP proxies.
func WithNoProxy() ClientOption {
	return WithProxy(
		// never use a proxy
		func(*http.Request) (*url.URL, error) { return nil, nil },
		nil)
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
	return func(_ *http.Request, via []*http.Request) error {
		if len(via) > limit {
			return fmt.Errorf("too many redirects (> %d)", limit)
		}
		return nil
	}
}

type clientOptionFunc func(*clientOptions)

func (f clientOptionFunc) applyToClient(opts *clientOptions) {
	f(opts)
}

type clientOptions struct {
	rootCtx                 context.Context //nolint:containedctx
	idleTransportTimeout    time.Duration
	roundTripperMaxLifetime time.Duration
	schemes                 map[string]Transport
	redirectFunc            func(req *http.Request, via []*http.Request) error
	allowedTarget           *target
	resolver                resolver.Resolver
	newPicker               func(prev picker.Picker, allConns conn.Conns) picker.Picker
	healthChecker           health.Checker
	dialFunc                func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyFunc               func(*http.Request) (*url.URL, error)
	proxyConnectHeadersFunc func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)
	maxResponseHeaderBytes  int64
	idleConnTimeout         time.Duration
	tlsClientConfig         *tls.Config
	tlsHandshakeTimeout     time.Duration
	defaultTimeout          time.Duration
	requestTimeout          time.Duration
}

func (opts *clientOptions) applyDefaults() {
	if opts.rootCtx == nil {
		opts.rootCtx = context.Background()
	}
	if opts.idleTransportTimeout == 0 {
		opts.idleTransportTimeout = 15 * time.Minute
	}
	if opts.schemes == nil {
		opts.schemes = map[string]Transport{}
	}
	if opts.redirectFunc == nil {
		opts.redirectFunc = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	// put default factories for http, https, and h2c if necessary
	if _, ok := opts.schemes["http"]; !ok {
		opts.schemes["http"] = simpleTransport{}
	}
	if _, ok := opts.schemes["https"]; !ok {
		opts.schemes["https"] = simpleTransport{}
	}
	if _, ok := opts.schemes["h2c"]; !ok {
		opts.schemes["h2c"] = h2cTransport{}
	}
	if opts.resolver == nil {
		opts.resolver = defaultResolver
	}
	if opts.newPicker == nil {
		opts.newPicker = picker.NewRoundRobin
	}
	if opts.healthChecker == nil {
		opts.healthChecker = health.NopChecker
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
}
