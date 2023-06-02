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

package httpbalancer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
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
	// Close is an optional function that will be called (if non-nil) when this
	// round-tripper is no longer needed.
	Close func()
	// PreWarm is an optional function that will be called (if non-nil) to
	// eagerly establish connections and perform any other checks so that there
	// are no delays or unexpected errors incurred by the first HTTP request.
	PreWarm func(ctx context.Context) error
}

// RoundTripperOptions defines the options used to create a round-tripper.
type RoundTripperOptions struct {
	// DialFunc should be used by the round-tripper establish network connections.
	DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
	// ProxyFunc should be used to control HTTP proxying behavior. If the function
	// returns a non-nil URL for a given request, that URL represents the HTTP proxy
	// that should be used.
	ProxyFunc func(*http.Request) (*url.URL, error)
	// ProxyHeadersFunc should be called, if non-nil, before sending a CONNECT
	// request, to query for headers to add to that request. If it returns an
	// error, the round-trip operation should fail immediately with that error.
	ProxyHeadersFunc func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error)
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

// TODO: add this info (whatever's relevant/user-visible) to doc.go

// This package uses a hierarchy with three-layers of transports:
//
// 1. mainTransport: This is the "top" of the hierarchy and is the
//    http.RoundTripper used by NewClient. This transport manages a
//    collection of other transports, grouped by URL scheme and
//    hostname:port.
// 2. transportPool: This provides a pool of transports for a single
//    URL schema and hostname:port. This is the layer in which most
//    of the features are implemented: name resolution, health
//    checking, and load balancing (sub-setting and picking). Each
//    transportPool manages a pool of lower-level transports, each
//    to a potentially different resolved address.
// 3. http.RoundTripper: The bottom of the hierarchy is a normal
//    round tripper, such as *http.Transport. This represents a
//    logical connection to a single resolved address. It may
//    actually represent more than one physical connection, like
//    when using HTTP 1.1 and multiple active requests are made
//    to the same address.

type mainTransport struct {
	rootCtx              context.Context //nolint:containedctx
	resolver             Resolver
	idleTransportTimeout time.Duration
	warmTargets          map[target]struct{}
	roundTripperOpts     RoundTripperOptions
	applyRequestTimeout  func(ctx context.Context) (context.Context, context.CancelFunc)

	mu    sync.RWMutex
	pools map[target]transportPoolEntry
}

func newTransport(opts *clientOptions) *mainTransport {
	var applyTimeout func(ctx context.Context) (context.Context, context.CancelFunc)
	if opts.requestTimeout > 0 {
		applyTimeout = func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, opts.requestTimeout)
		}
	} else if opts.defaultTimeout > 0 {
		applyTimeout = func(ctx context.Context) (context.Context, context.CancelFunc) {
			_, ok := ctx.Deadline()
			if !ok {
				// no existing deadline, so set one
				return context.WithTimeout(ctx, opts.defaultTimeout)
			}
			return ctx, func() {}
		}
	}
	var warmSet map[target]struct{}
	if len(opts.warmTargets) > 0 {
		warmSet = make(map[target]struct{}, len(opts.warmTargets))
		for _, warmTarget := range opts.warmTargets {
			targetURL, err := url.Parse(warmTarget)
			if err != nil {
				targetURL = &url.URL{Host: warmTarget}
			}
			warmSet[target{scheme: targetURL.Scheme, hostPort: targetURL.Host}] = struct{}{}
		}
	}
	// TODO: custom round trippers, resolvers, balancers, etc
	return &mainTransport{
		rootCtx:              opts.rootCtx,
		resolver:             opts.resolver,
		roundTripperOpts:     roundTripperOptionsFrom(opts),
		idleTransportTimeout: opts.idleTransportTimeout,
		warmTargets:          warmSet,
		applyRequestTimeout:  applyTimeout,
		pools:                map[target]transportPoolEntry{},
	}
}

func (m *mainTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	dest := target{scheme: request.URL.Scheme, hostPort: request.URL.Host}
	return m.getOrCreatePool(dest).RoundTrip(request)
}

func (m *mainTransport) getOrCreatePool(dest target) *transportPool {
	m.mu.RLock()
	pool := m.getPoolLocked(dest)
	m.mu.RUnlock()

	if pool != nil {
		return pool
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// double-check in case pool was added while upgrading lock
	pool = m.getPoolLocked(dest)
	if pool != nil {
		return pool
	}

	opts := m.roundTripperOpts
	_, opts.KeepWarm = m.warmTargets[dest]
	pool = newTransportPool(m.rootCtx, dest.scheme, dest.hostPort, simpleFactory{}, opts, m.resolver)
	var activity chan struct{}
	if !opts.KeepWarm {
		activity = make(chan struct{}, 1)
		go m.closeWhenIdle(m.rootCtx, dest, pool, activity)
	}
	m.pools[dest] = transportPoolEntry{pool: pool, activity: activity}
	return pool
}

func (m *mainTransport) getPoolLocked(dest target) *transportPool {
	entry := m.pools[dest]
	if entry.activity != nil {
		// Update activity while lock is held (should be okay since
		// it's usually a read-lock, and this is a non-blocking write).
		// Doing this while locked avoids race condition with idle timer
		// that might be trying to concurrently close this transport.
		select {
		case entry.activity <- struct{}{}:
		default:
		}
	}
	return entry.pool
}

func (m *mainTransport) closeWhenIdle(ctx context.Context, dest target, pool *transportPool, activity <-chan struct{}) {
	timer := time.NewTimer(m.idleTransportTimeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if m.tryClosePool(dest, pool, activity) {
				return
			}
			// If we couldn't close pool, it's due to concurrent activity,
			// so reset timer and try again.
			timer.Reset(m.idleTransportTimeout)
		case <-ctx.Done():
			m.closePool(dest, pool)
			return
		case <-activity:
			timer.Reset(m.idleTransportTimeout)
		}
	}
}

func (m *mainTransport) tryClosePool(dest target, pool *transportPool, activity <-chan struct{}) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	// need to check activity after lock acquired to make
	// sure we aren't racing with use of this pool
	select {
	case <-activity:
		// another goroutine is now using it
		return false
	default:
	}
	delete(m.pools, dest)
	pool.close()
	return true
}

func (m *mainTransport) closePool(dest target, pool *transportPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pools, dest)
	pool.close()
}

func (m *mainTransport) prewarm(ctx context.Context) error {
	if len(m.warmTargets) == 0 {
		return nil
	}
	grp, grpcCtx := errgroup.WithContext(ctx)
	for dest := range m.warmTargets {
		pool := m.getOrCreatePool(dest)
		grp.Go(func() error {
			return pool.prewarm(grpcCtx)
		})
	}
	return grp.Wait()
}

type target struct {
	scheme   string
	hostPort string
}

type transportPoolEntry struct {
	pool     *transportPool
	activity chan<- struct{}
}

type transportPool struct {
	scheme              string
	roundTripperFactory RoundTripperFactory
	roundTripperOptions RoundTripperOptions
	resolved            <-chan struct{}
	resolver            Resolver

	mu sync.RWMutex
	// TODO: we may need better data structures than this to
	//       support "picker" interface and efficient selection
	pool   map[string][]connection
	conns  []connection
	closed bool
}

func newTransportPool(ctx context.Context, scheme, hostPort string, factory RoundTripperFactory, opts RoundTripperOptions, resolver Resolver) *transportPool {
	resolveCtx, resolveCancel := context.WithTimeout(ctx, 10*time.Second /* TODO */)

	pool := &transportPool{
		scheme:              scheme,
		roundTripperFactory: factory,
		roundTripperOptions: opts,
		resolved:            resolveCtx.Done(),
		resolver:            resolver,
		pool:                map[string][]connection{},
	}

	go func() {
		// TODO: customizable resolution (and better DNS resolution than default),
		//       custom subsetting, etc.
		defer resolveCancel()
		backends, err := resolver.Resolve(resolveCtx, hostPort)
		if err != nil {
			// TODO: need to figure out how to handle this...
			pool.mu.Lock()
			pool.pool = map[string][]connection{}
			pool.conns = []connection{}
			pool.mu.Unlock()
			return
		}
		// TODO:
		// - not sure what to do about scheduling periodic resolution requests
		//   (should there be a separate scheduler interface?)
		// - need to implement diffing for resolution after initial resolve
		newPool := map[string][]connection{}
		newConns := []connection{}
		for _, backend := range backends {
			result := factory.New(scheme, backend.HostPort(), opts)
			conn := connection{
				backend: backend,
				conn:    result.RoundTripper,
				close:   result.Close,
				prewarm: result.PreWarm,
			}
			newPool[backend.HostPort()] = []connection{conn}
			newConns = append(newConns, conn)
		}
		pool.mu.Lock()
		pool.pool = newPool
		pool.conns = newConns
		pool.mu.Unlock()
	}()

	return pool
}

func (t *transportPool) RoundTrip(request *http.Request) (*http.Response, error) {
	rt := t.getRoundTripper()
	if rt == nil {
		return nil, errors.New("transport is closed")
	}
	return rt.RoundTrip(request)
}

func (t *transportPool) getRoundTripper() http.RoundTripper {
	t.mu.RLock()
	conns, closed := t.conns, t.closed
	t.mu.RUnlock()

	if !closed && conns == nil {
		// NB: if resolver returns no addresses, that should
		//     be represented via empty but non-nil conns...
		<-t.resolved
		t.mu.RLock()
		conns, closed = t.conns, t.closed
		t.mu.RUnlock()
	}

	if closed {
		return nil
	}
	if len(conns) == 0 {
		return noAddressRoundTripper{}
	}
	return t.pick(conns)
}

func (t *transportPool) pick(conns []connection) http.RoundTripper {
	// TODO: customizable picker
	t.mu.RLock()
	conn := conns[0]
	t.mu.RUnlock()

	return addressRewritingRoundTripper{
		hostPort:     conn.backend.HostPort(),
		roundTripper: conn.conn,
	}
}

func (t *transportPool) prewarm(ctx context.Context) error {
	if !t.roundTripperOptions.KeepWarm {
		// not keeping this one warm...
		return nil
	}
	select {
	case <-t.resolved:
	case <-ctx.Done():
		return ctx.Err()
	}
	t.mu.RLock()
	conns, closed := t.conns, t.closed
	t.mu.RUnlock()
	if closed {
		return nil
	}
	// TODO: we probably want some sort of synchronization to
	//       prevent trying to pre-warm a connection after it's
	//       been closed (if racing with a call to close the pool)
	grp, grpCtx := errgroup.WithContext(ctx)
	for _, conn := range conns {
		conn := conn
		if conn.prewarm != nil {
			grp.Go(func() error {
				return conn.prewarm(grpCtx)
			})
		}
	}
	return grp.Wait()
}

func (t *transportPool) close() {
	t.mu.Lock()
	conns, closed := t.conns, t.closed
	t.closed = true
	t.mu.Unlock()
	if closed {
		// already closed, nothing to do
		return
	}
	// TODO: If a conn has any active/pending requests, we should
	//       arrange to call conn.close() when they complete instead
	//       of calling it now
	for _, conn := range conns {
		if conn.close != nil {
			conn.close()
		}
	}
}

type connection struct {
	backend Backend
	conn    http.RoundTripper
	close   func()
	prewarm func(context.Context) error
}

func roundTripperOptionsFrom(opts *clientOptions) RoundTripperOptions {
	return RoundTripperOptions{
		DialFunc:               opts.dialFunc,
		ProxyFunc:              opts.proxyFunc,
		ProxyHeadersFunc:       opts.proxyHeadersFunc,
		MaxResponseHeaderBytes: opts.maxResponseHeaderBytes,
		IdleConnTimeout:        opts.idleConnTimeout,
		TLSClientConfig:        opts.tlsClientConfig,
		TLSHandshakeTimeout:    opts.tlsHandshakeTimeout,
	}
}

type simpleFactory struct{}

func (s simpleFactory) New(_, _ string, opts RoundTripperOptions) RoundTripperResult {
	transport := &http.Transport{
		Proxy:                  opts.ProxyFunc,
		GetProxyConnectHeader:  opts.ProxyHeadersFunc,
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

type addressRewritingRoundTripper struct {
	hostPort     string
	roundTripper http.RoundTripper
}

func (a addressRewritingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.URL.Host = a.hostPort
	return a.roundTripper.RoundTrip(req)
}

type noAddressRoundTripper struct{}

func (n noAddressRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	// TODO: probably want more useful info in this error message
	return nil, fmt.Errorf("failed to resolve hostname")
}
