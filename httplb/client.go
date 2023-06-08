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

	"github.com/bufbuild/go-http-balancer/balancer"
	"github.com/bufbuild/go-http-balancer/resolver"
)

//nolint:gochecknoglobals
var (
	defaultDialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	defaultNameTTL         = 5 * time.Minute
	defaultResolver        = resolver.NewDNSResolver(net.DefaultResolver, "ip", defaultNameTTL)
	defaultBalancerFactory = balancer.NewFactory()
)

// ClientOption is an option used to customize the behavior of an HTTP client.
type ClientOption interface {
	applyToClient(*clientOptions)
}

// TargetOption is an option used to customize the behavior of an HTTP client
// that can be applied to a single target or backend.
//
// A TargetOption can be used as a ClientOption, in which case it applies as
// a default for all targets.
type TargetOption interface {
	applyToClient(*clientOptions)
	applyToTarget(*targetOptions)
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

// WithResolver configures the HTTP client to use the given resolver, which
// is how hostnames are resolved into individual addresses for the underlying
// connections.
//
// If not provided, the default resolver will resolve A and AAAA records
// using net.DefaultResolver.
func WithResolver(res resolver.Resolver) ClientOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.resolver = res
	})
}

// WithBalancer configures the HTTP client to use the given factory to create
// [balancer.Balancer] implementations, which are how requests are load balanced
// to a particular target hostname.
//
// If not provided, the default balancer will create connections to all resolved
// addresses and then pick connections using a round-robin strategy.
func WithBalancer(balancerFactory balancer.Factory) ClientOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.balancer = balancerFactory
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
) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.proxyFunc = proxyFunc
		opts.proxyHeadersFunc = proxyHeadersFunc
	})
}

// WithNoProxy returns an option that disables use of HTTP proxies.
func WithNoProxy() TargetOption {
	return WithProxy(
		// never use a proxy
		func(*http.Request) (*url.URL, error) { return nil, nil },
		nil)
}

// WithRedirects configures how the HTTP client handles redirect responses.
// If no such option is provided, the client will not follow any redirects.
func WithRedirects(redirectFunc RedirectFunc) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
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
func WithDefaultTimeout(duration time.Duration) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.defaultTimeout = duration
		opts.requestTimeout = 0
	})
}

// WithRequestTimeout limits all requests to the given timeout. This time
// is the entire duration of the request, including sending the request,
// writing the request body, waiting for a response, and consuming the
// response body.
func WithRequestTimeout(duration time.Duration) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.defaultTimeout = 0
		opts.requestTimeout = duration
	})
}

// WithDialer configures the HTTP client to use the given function to
// establish network connections. If no WithDialer option is provided,
// a default [net.Dialer] is used that uses a 30-second dial timeout and
// configures the connection to use TCP keep-alive every 30 seconds.
func WithDialer(dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.dialFunc = dialFunc
	})
}

// WithTLSConfig adds custom TLS configuration to the HTTP client. The
// given config is used when using TLS to communicate with servers. The
// given timeout is applied to the TLS handshake step. If the given timeout
// is zero or no WithTLSConfig option is used, a default timeout of 10
// seconds will be used.
func WithTLSConfig(config *tls.Config, handshakeTimeout time.Duration) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
		opts.tlsClientConfig = config
		opts.tlsHandshakeTimeout = handshakeTimeout
	})
}

// WithMaxResponseHeaderBytes configures the maximum size of response headers
// to consume. If zero or if no WithMaxResponseHeaderBytes option is used, the
// HTTP client will default to a 1 MB limit (2^20 bytes).
func WithMaxResponseHeaderBytes(limit int) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
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
func WithIdleConnectionTimeout(duration time.Duration) TargetOption {
	return targetOptionFunc(func(opts *targetOptions) {
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

// WithRoundTripperFactory returns an option that uses a custom factory
// for [http.RoundTripper] instances for the given URL scheme. This allows
// one to override the default factories for "http", "https", and "h2c"
// schemes and also allows one to support custom URL schemes that map to
// custom transports created by the given factory.
func WithRoundTripperFactory(scheme string, factory RoundTripperFactory) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		if opts.schemes == nil {
			opts.schemes = map[string]RoundTripperFactory{}
		}
		opts.schemes[scheme] = factory
	})
}

// WithBackendTarget configures the given target (identified by URL scheme
// and host:port) with the given options. Targets configured this way will be
// kept warm, meaning that associated transports will not be closed due to
// inactivity, regardless of the idle transport timeout configuration. Further,
// hosts configured this way can be "warmed up" via the Prewarm function, to
// make sure they are ready for application use.
//
// The scheme and host:port given must match those of associated requests. So
// if requests omit the port from the URL (for example), then the hostPort
// given here should also omit the port.
func WithBackendTarget(scheme, hostPort string, targetOpts ...TargetOption) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		dest := target{scheme: scheme, hostPort: hostPort}
		if dest.scheme == "" {
			dest.scheme = "http"
		}
		if opts.targetOptions == nil {
			opts.targetOptions = map[target][]TargetOption{}
		}
		opts.targetOptions[dest] = append(opts.targetOptions[dest], targetOpts...)
	})
}

// WithDisallowUnconfiguredTargets configures the client to disallow HTTP
// requests to targets that were not configured via WithBackendTarget options.
func WithDisallowUnconfiguredTargets() ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.disallowOthers = true
	})
}

// WithDebugResourceLeaks configures the client so that it calls the given
// function if it detects a resource leak in a client operation. A resource
// leak is where the calling code fails to exhaust or close the body of an
// HTTP response. If your program has such a resource leak, some features of
// load balancing may not work as expected. For example, a "least loaded"
// algorithm will see these HTTP operations as forever in progress. Also,
// attempts to Close a client may hang indefinitely, waiting on these
// orphaned operations to complete.
//
// It is recommended that, in unit tests of HTTP client code, the client be
// configured with this option using a callback that will fail the test or
// panic if a leak is detected.
func WithDebugResourceLeaks(callback func(req *http.Request, resp *http.Response)) ClientOption {
	return clientOptionFunc(func(opts *clientOptions) {
		opts.resourceLeakCallback = callback
	})
}

// NewClient returns a new HTTP client that uses the given options.
func NewClient(options ...ClientOption) *http.Client {
	var opts clientOptions
	for _, opt := range options {
		opt.applyToClient(&opts)
	}
	opts.applyDefaults()
	opts.computeTargetOptions()
	return &http.Client{
		Transport:     newTransport(&opts),
		CheckRedirect: opts.redirect,
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
// configured via WithBackendTarget have been warmed up. This ensures that
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

func (f clientOptionFunc) applyToClient(opts *clientOptions) {
	f(opts)
}

type clientOptions struct {
	rootCtx              context.Context //nolint:containedctx
	idleTransportTimeout time.Duration
	// if true, only targets configured below are allowed; requests to others will fail
	disallowOthers bool
	schemes        map[string]RoundTripperFactory

	// target options are accumulated in these
	defaultTargetOptions []TargetOption
	targetOptions        map[target][]TargetOption

	// the above options are then applied to these computed results
	computedDefaultTargetOptions targetOptions
	computedTargetOptions        map[target]*targetOptions

	resourceLeakCallback func(req *http.Request, resp *http.Response)
}

func (opts *clientOptions) applyDefaults() {
	if opts.rootCtx == nil {
		opts.rootCtx = context.Background()
	}
	if opts.idleTransportTimeout == 0 {
		opts.idleTransportTimeout = 15 * time.Minute
	}
	if opts.schemes == nil {
		opts.schemes = map[string]RoundTripperFactory{}
	}
	// put default factories for http, https, and h2c if necessary
	if _, ok := opts.schemes["http"]; !ok {
		opts.schemes["http"] = simpleFactory{}
	}
	if _, ok := opts.schemes["https"]; !ok {
		opts.schemes["https"] = simpleFactory{}
	}
	if _, ok := opts.schemes["h2c"]; !ok {
		opts.schemes["h2c"] = h2cFactory{}
	}
}

func (opts *clientOptions) computeTargetOptions() {
	// compute the defaults
	for _, opt := range opts.defaultTargetOptions {
		opt.applyToTarget(&opts.computedDefaultTargetOptions)
	}
	opts.computedDefaultTargetOptions.applyDefaults()

	// and compute for each configured target
	opts.computedTargetOptions = make(map[target]*targetOptions, len(opts.targetOptions))
	for target, targetOptionSlice := range opts.targetOptions {
		var targetOpts targetOptions
		// apply defaults first
		for _, opt := range opts.defaultTargetOptions {
			opt.applyToTarget(&targetOpts)
		}
		// then others, to override defaults
		for _, opt := range targetOptionSlice {
			opt.applyToTarget(&targetOpts)
		}
		// finally, fill in defaults for unset values
		targetOpts.applyDefaults()

		opts.computedTargetOptions[target] = &targetOpts
	}
}

func (opts *clientOptions) redirect(req *http.Request, via []*http.Request) error {
	// use original request target to determine redirect rules
	dest := targetFromURL(via[0].URL)
	targetOpts := opts.optionsForTarget(dest)
	if targetOpts == nil {
		return nil
	}
	return targetOpts.redirectFunc(req, via)
}

// optionsForTarget returns the options to use for requests to the given
// target. If the given target was not configured, this will return the
// default options, or it will return nil if WithDisallowUnconfiguredTargets
// was used.
func (opts *clientOptions) optionsForTarget(dest target) *targetOptions {
	if targetOpts := opts.computedTargetOptions[dest]; targetOpts != nil {
		return targetOpts
	}
	if opts.disallowOthers {
		return nil
	}
	return &opts.computedDefaultTargetOptions
}

type targetOptionFunc func(*targetOptions)

func (f targetOptionFunc) applyToClient(opts *clientOptions) {
	opts.defaultTargetOptions = append(opts.defaultTargetOptions, f)
}

func (f targetOptionFunc) applyToTarget(opts *targetOptions) {
	f(opts)
}

type targetOptions struct {
	resolver               resolver.Resolver
	balancer               balancer.Factory
	dialFunc               func(ctx context.Context, network, addr string) (net.Conn, error)
	proxyFunc              func(*http.Request) (*url.URL, error)
	proxyHeadersFunc       func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)
	redirectFunc           func(req *http.Request, via []*http.Request) error
	maxResponseHeaderBytes int64
	idleConnTimeout        time.Duration
	tlsClientConfig        *tls.Config
	tlsHandshakeTimeout    time.Duration
	defaultTimeout         time.Duration
	requestTimeout         time.Duration
}

func (opts *targetOptions) applyDefaults() {
	if opts.resolver == nil {
		opts.resolver = defaultResolver
	}
	if opts.balancer == nil {
		opts.balancer = defaultBalancerFactory
	}
	if opts.dialFunc == nil {
		opts.dialFunc = defaultDialer.DialContext
	}
	if opts.proxyFunc == nil {
		opts.proxyFunc = http.ProxyFromEnvironment
	}
	if opts.redirectFunc == nil {
		opts.redirectFunc = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	if opts.maxResponseHeaderBytes == 0 {
		opts.maxResponseHeaderBytes = 1 << 20
	}
	if opts.tlsHandshakeTimeout == 0 {
		opts.tlsHandshakeTimeout = 10 * time.Second
	}
}
