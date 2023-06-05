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
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

var (
	errResolverReturnedNoAddresses = errors.New("resolver returned no addresses")
	errTransportIsClosed           = errors.New("transport is closed")
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
	cancel               context.CancelFunc
	idleTransportTimeout time.Duration
	clientOptions        *clientOptions

	mu     sync.RWMutex
	pools  map[target]transportPoolEntry
	closed bool
}

func newTransport(opts *clientOptions) *mainTransport {
	ctx, cancel := context.WithCancel(opts.rootCtx)
	// TODO: custom round trippers, resolvers, balancers, etc
	transport := &mainTransport{
		rootCtx:              ctx,
		cancel:               cancel,
		clientOptions:        opts,
		idleTransportTimeout: opts.idleTransportTimeout,
		pools:                map[target]transportPoolEntry{},
	}
	go func() {
		// close transport immediately if context is cancelled
		<-ctx.Done()
		transport.close()
	}()
	return transport
}

func (m *mainTransport) close() {
	m.mu.Lock()
	alreadyClosed := m.closed
	m.closed = true
	m.mu.Unlock()
	if !alreadyClosed {
		m.cancel()
		m.closeKeepWarmPools()
	}
	// TODO: await all resources being released
}

func (m *mainTransport) closeKeepWarmPools() {
	// Pools that are not kept warm will close automatically thanks to the
	// idle timeout goroutine, which will notice the context cancellation.
	// But we have to explicitly close pools that we keep warm.
	var pools []*transportPool
	func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		pools = make([]*transportPool, 0, len(m.pools))
		for dest, entry := range m.pools {
			if !entry.pool.roundTripperOptions.KeepWarm {
				continue
			}
			pools = append(pools, entry.pool)
			delete(m.pools, dest)
		}
	}()
	for _, pool := range pools {
		pool.close()
	}
}

func (m *mainTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	dest := targetFromURL(request.URL)
	pool, err := m.getOrCreatePool(dest)
	if err != nil {
		return nil, err
	}
	return pool.RoundTrip(request)
}

// getOrCreatePool gets the transport pool for the given dest, creating one if
// none exists. However, this refuses to create a pool, and will return nil, if
// the transport is closed.
func (m *mainTransport) getOrCreatePool(dest target) (*transportPool, error) {
	m.mu.RLock()
	closed := m.closed
	pool := m.getPoolLocked(dest)
	m.mu.RUnlock()

	if closed {
		return nil, errTransportIsClosed
	}
	if pool != nil {
		return pool, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// double-check in case things changed while upgrading lock
	if m.closed {
		return nil, errTransportIsClosed
	}
	pool = m.getPoolLocked(dest)
	if pool != nil {
		return pool, nil
	}

	targetOpts := m.clientOptions.optionsForTarget(dest)
	if targetOpts == nil {
		return nil, fmt.Errorf("client does not allow requests to unconfigured target %s", dest)
	}
	var applyTimeout func(ctx context.Context) (context.Context, context.CancelFunc)
	if targetOpts.requestTimeout > 0 {
		applyTimeout = func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, targetOpts.requestTimeout)
		}
	} else if targetOpts.defaultTimeout > 0 {
		applyTimeout = func(ctx context.Context) (context.Context, context.CancelFunc) {
			_, ok := ctx.Deadline()
			if !ok {
				// no existing deadline, so set one
				return context.WithTimeout(ctx, targetOpts.defaultTimeout)
			}
			return ctx, func() {}
		}
	}

	opts := roundTripperOptionsFrom(targetOpts)
	// explicitly configured targets are kept warm
	_, opts.KeepWarm = m.clientOptions.computedTargetOptions[dest]

	pool = newTransportPool(m.rootCtx, dest, applyTimeout, simpleFactory{}, opts, m.clientOptions.resourceLeakCallback)
	var activity chan struct{}
	if !opts.KeepWarm {
		activity = make(chan struct{}, 1)
		go m.closeWhenIdle(m.rootCtx, dest, pool, activity)
	}
	m.pools[dest] = transportPoolEntry{pool: pool, activity: activity}
	return pool, nil
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
			if m.tryRemovePool(dest, activity) {
				pool.close()
				return
			}
			// If we couldn't close pool, it's due to concurrent activity,
			// so reset timer and try again.
			timer.Reset(m.idleTransportTimeout)
		case <-ctx.Done():
			m.removePool(dest)
			pool.close()
			return
		case <-activity:
			// bump idle timer whenever there's activity
			timer.Reset(m.idleTransportTimeout)
		}
	}
}

func (m *mainTransport) tryRemovePool(dest target, activity <-chan struct{}) bool {
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
	return true
}

func (m *mainTransport) removePool(dest target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pools, dest)
}

func (m *mainTransport) prewarm(ctx context.Context) error {
	if len(m.clientOptions.computedTargetOptions) == 0 {
		return nil
	}
	grp, grpcCtx := errgroup.WithContext(ctx)
	for dest := range m.clientOptions.computedTargetOptions {
		pool, err := m.getOrCreatePool(dest)
		if err == nil {
			grp.Go(func() error {
				return pool.prewarm(grpcCtx)
			})
		}
	}
	return grp.Wait()
}

type target struct {
	scheme   string
	hostPort string
}

func targetFromURL(dest *url.URL) target {
	t := target{scheme: dest.Scheme, hostPort: dest.Host}
	if t.scheme == "" {
		t.scheme = "http"
	}
	return t
}

func (t target) String() string {
	return t.scheme + "://" + t.hostPort
}

type transportPoolEntry struct {
	pool     *transportPool
	activity chan<- struct{}
}

type transportPool struct {
	dest                 target
	applyRequestTimeout  func(ctx context.Context) (context.Context, context.CancelFunc)
	roundTripperFactory  RoundTripperFactory
	roundTripperOptions  RoundTripperOptions
	resolved             <-chan struct{}
	resourceLeakCallback func(req *http.Request, resp *http.Response)

	mu sync.RWMutex
	// TODO: we may need better data structures than this to
	//       support "picker" interface and efficient selection
	pool   map[string][]*connection
	conns  []*connection
	closed bool
}

func newTransportPool(
	_ context.Context,
	dest target,
	applyTimeout func(ctx context.Context) (context.Context, context.CancelFunc),
	factory RoundTripperFactory,
	opts RoundTripperOptions,
	resourceLeakCallback func(req *http.Request, resp *http.Response),
) *transportPool {
	resolved := make(chan struct{})

	pool := &transportPool{
		dest:                 dest,
		applyRequestTimeout:  applyTimeout,
		roundTripperFactory:  factory,
		roundTripperOptions:  opts,
		resolved:             resolved,
		pool:                 map[string][]*connection{},
		resourceLeakCallback: resourceLeakCallback,
	}

	go func() {
		// TODO: customizable resolution (and better DNS resolution than default),
		//       custom subsetting, etc.
		defer close(resolved)
		addr := dest.hostPort
		result := factory.New(dest.scheme, addr, opts)
		conn := &connection{
			addr:    addr,
			conn:    result.RoundTripper,
			close:   result.Close,
			prewarm: result.PreWarm,
		}
		pool.mu.Lock()
		pool.conns = []*connection{conn}
		pool.pool[addr] = []*connection{conn}
		pool.mu.Unlock()
	}()

	return pool
}

func (t *transportPool) RoundTrip(request *http.Request) (*http.Response, error) {
	conn, err := t.getConnection()
	if err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if t.applyRequestTimeout != nil {
		var ctx context.Context
		ctx, cancel = t.applyRequestTimeout(request.Context())
		request = request.WithContext(ctx)
	}
	conn.activeRequests.Add(1)
	resp, err := conn.conn.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	addCompletionHook(request, resp, func() {
		// TODO: may need to use WaitGroup or similar so we can await
		//       completion of active requests
		if cancel != nil {
			cancel()
		}
		conn.activeRequests.Add(-1)
	}, t.resourceLeakCallback)
	return resp, nil
}

func (t *transportPool) getConnection() (*connection, error) {
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
		return nil, errTransportIsClosed
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("failed to resolve hostname %q: %w", t.dest.hostPort, errResolverReturnedNoAddresses)
	}
	return t.pick(conns), nil
}

func (t *transportPool) pick(conns []*connection) *connection {
	// TODO: customizable picker
	return conns[0]
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
	conns, alreadyClosed := t.conns, t.closed
	t.closed = true
	t.mu.Unlock()
	if !alreadyClosed {
		// TODO: If a conn has any active/pending requests, we should
		//       wait for activity to cease before calling conn.close()
		for _, conn := range conns {
			if conn.close != nil {
				conn.close()
			}
		}
	}
	// TODO: await all resources being released
}

type connection struct {
	addr    string
	conn    http.RoundTripper
	close   func()
	prewarm func(context.Context) error

	activeRequests atomic.Int32
}

func roundTripperOptionsFrom(opts *targetOptions) RoundTripperOptions {
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

func addCompletionHook(req *http.Request, resp *http.Response, whenComplete func(), resourceLeakCallback func(req *http.Request, resp *http.Response)) {
	newBody := io.ReadCloser(&hookReadCloser{ReadCloser: resp.Body, hook: whenComplete})
	if resourceLeakCallback != nil {
		runtime.SetFinalizer(newBody, func(closer *hookReadCloser) {
			if closer.closed.CompareAndSwap(false, true) {
				// If this succeeded, it means the body was not previously closed, which is a leak!
				resourceLeakCallback(req, resp)
			}
		})
		// Return a wrapper, so if calling code sets a finalizer, it won't
		// overwrite the one we set above.
		type wrapper struct {
			io.ReadCloser
		}
		newBody = &wrapper{ReadCloser: newBody}
	}
	if w, ok := resp.Body.(io.Writer); ok {
		type readWriteCloser struct {
			io.ReadCloser
			io.Writer
		}
		newBody = &readWriteCloser{ReadCloser: newBody, Writer: w}
	}
	resp.Body = newBody
}

type hookReadCloser struct {
	io.ReadCloser
	hook   func()
	closed atomic.Bool
}

func (h *hookReadCloser) done() {
	if h.closed.CompareAndSwap(false, true) {
		h.hook()
	}
}

func (h *hookReadCloser) Read(p []byte) (n int, err error) {
	n, err = h.ReadCloser.Read(p)
	if err != nil {
		// TODO: Is this correct to call here? At this point, we've either
		// encountered a network error or EOF. Either way, the operation is
		// complete. But the docs on http.Response.Body suggest that a
		// connection may not get re-used unless Close() is called (exhausting
		// the body alone is not sufficient). So maybe we should only hook the
		// Close() method?
		h.done()
	}
	return n, err
}

func (h *hookReadCloser) Close() error {
	err := h.ReadCloser.Close()
	h.done()
	return err
}
