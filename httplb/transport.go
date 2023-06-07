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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bufbuild/go-http-balancer/resolver"
	"golang.org/x/sync/errgroup"
)

var (
	errResolverReturnedNoAddresses = errors.New("resolver returned no addresses")
	errTransportIsClosed           = errors.New("transport is closed")
)

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

	mu    sync.RWMutex
	pools map[target]transportPoolEntry
	// +checklocks:mu
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
		<-transport.rootCtx.Done()
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
	grp, _ := errgroup.WithContext(context.Background())
	for _, pool := range pools {
		pool := pool
		grp.Go(func() error {
			pool.close()
			return nil
		})
	}
	_ = grp.Wait()
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

	schemeConf, ok := m.clientOptions.schemes[dest.scheme]
	if !ok {
		return nil, fmt.Errorf("unsupported URL scheme %q", dest.scheme)
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

	pool = newTransportPool(m.rootCtx, targetOpts.resolver, dest, applyTimeout, schemeConf, opts, m.clientOptions.resourceLeakCallback)
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
	resolverCloser       io.Closer
	closeComplete        chan struct{}

	mu        sync.RWMutex
	resolveMu sync.Mutex
	// TODO: we may need better data structures than this to
	//       support "picker" interface and efficient selection
	// +checklocks:resolveMu
	pool map[string][]*connection
	// +checklocks:mu
	conns []*connection
	// +checklocks:mu
	closed bool
	// +checklocks:mu
	resolveErr error
}

func newTransportPool(
	ctx context.Context,
	res resolver.Resolver,
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
		closeComplete:        make(chan struct{}),
	}

	pool.resolverCloser = res.Resolve(
		ctx,
		dest.scheme,
		dest.hostPort,
		func(addresses []resolver.Address, err error) {
			var firstResolve bool
			newConns := []*connection{}
			newPool := map[string][]*connection{}
			oldPool := map[string][]*connection{}

			if len(addresses) > 0 {
				defer func() {
					for _, conns := range oldPool {
						for _, conn := range conns {
							conn.close()
						}
					}
				}()

				pool.resolveMu.Lock()
				defer pool.resolveMu.Unlock()

				for hostPort, conns := range pool.pool {
					oldPool[hostPort] = conns
				}
				for _, addr := range addresses {
					if conns, ok := pool.pool[addr.HostPort]; ok {
						newConns = append(newConns, conns...)
						newPool[addr.HostPort] = conns
						delete(oldPool, addr.HostPort)
					} else {
						result := factory.New(dest.scheme, addr.HostPort, opts)
						conn := &connection{
							scheme:  result.Scheme,
							addr:    addr.HostPort,
							conn:    result.RoundTripper,
							close:   result.Close,
							prewarm: result.PreWarm,
						}
						newConns = append(newConns, conn)
						newPool[addr.HostPort] = []*connection{conn}
					}
				}

				pool.mu.Lock()
				firstResolve = pool.conns == nil
				pool.conns = newConns
				pool.pool = newPool
				pool.resolveErr = err
				pool.mu.Unlock()
			} else {
				// nb: hold resolveMu to mitigate the potential race with pool
				// if this callback is called from two different goroutines;
				// acquire resolveMu first to ensure we don't block mu while
				// waiting for it
				pool.resolveMu.Lock()
				pool.mu.Lock()
				firstResolve = pool.conns == nil
				// Ensures that these variables will be empty instead of nil,
				// to signal that the first resolution finished (with error).
				// Otherwise, keep using stale conns in error case.
				if firstResolve {
					pool.conns = newConns
					pool.pool = newPool
				}
				pool.resolveErr = err
				pool.mu.Unlock()
				pool.resolveMu.Unlock()
			}

			if firstResolve {
				close(resolved)
			}
		},
	)

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

	// rewrite request if necessary
	if (conn.scheme != "" && conn.scheme != request.URL.Scheme) || conn.addr != request.URL.Host {
		request = request.Clone(request.Context())
		if conn.scheme != "" {
			request.URL.Scheme = conn.scheme
		}
		request.URL.Host = conn.addr
	}

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
	conns, closed, resolveErr := t.conns, t.closed, t.resolveErr
	t.mu.RUnlock()

	if !closed && conns == nil && resolveErr == nil {
		// NB: if resolver returns no addresses, that should
		//     be represented via empty but non-nil conns...
		<-t.resolved
		t.mu.RLock()
		conns, closed, resolveErr = t.conns, t.closed, t.resolveErr
		t.mu.RUnlock()
	}

	if closed {
		return nil, errTransportIsClosed
	}
	if len(conns) == 0 {
		if resolveErr == nil {
			resolveErr = errResolverReturnedNoAddresses
		}
		return nil, fmt.Errorf("failed to resolve hostname %q: %w", t.dest.hostPort, resolveErr)
	}
	return t.pick(conns), nil
}

func (t *transportPool) pick(conns []*connection) *connection {
	// TODO: customizable picker
	t.mu.RLock()
	defer t.mu.RUnlock()

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
		// TODO: do we need to warmup *every* address?
		conn := conn
		if conn.prewarm != nil {
			grp.Go(func() error {
				return conn.prewarm(grpCtx, conn.addr)
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
	if alreadyClosed {
		<-t.closeComplete
		return
	}
	grp, _ := errgroup.WithContext(context.Background())
	grp.Go(t.resolverCloser.Close)
	for _, conn := range conns {
		conn := conn
		if conn.close != nil {
			grp.Go(func() error {
				// TODO: If a conn has any active/pending requests, we should
				//       wait for activity to cease before calling conn.close()
				conn.close()
				return nil
			})
		}
	}
	_ = grp.Wait()
	close(t.closeComplete)
}

type connection struct {
	scheme  string
	addr    string
	conn    http.RoundTripper
	close   func()
	prewarm func(context.Context, string) error

	// +checkatomic
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

func addCompletionHook(req *http.Request, resp *http.Response, whenComplete func(), resourceLeakCallback func(req *http.Request, resp *http.Response)) {
	var hookedBody *hookReadCloser
	bodyWriter, isWriter := resp.Body.(io.Writer)
	if isWriter {
		hookedWriter := &hookReadWriteCloser{
			hookReadCloser: hookReadCloser{ReadCloser: resp.Body, hook: whenComplete},
			Writer:         bodyWriter,
		}
		hookedBody = &hookedWriter.hookReadCloser
		resp.Body = hookedWriter
	} else {
		hookedBody = &hookReadCloser{ReadCloser: resp.Body, hook: whenComplete}
		resp.Body = hookedBody
	}

	if resourceLeakCallback != nil {
		runtime.SetFinalizer(resp.Body, func(io.ReadCloser) {
			if hookedBody.closed.CompareAndSwap(false, true) {
				// If this succeeded, it means the body was not previously closed, which is a leak!
				resourceLeakCallback(req, resp)
			}
		})
		// Set resp.Body to a wrapper, so if calling code sets a finalizer, it won't
		// overwrite the one we just set above.
		if isWriter {
			type wrapper struct {
				io.ReadWriteCloser
			}
			//nolint:forcetypeassert // we set this above, so we know it implements this interface
			resp.Body = &wrapper{ReadWriteCloser: resp.Body.(io.ReadWriteCloser)}
		} else {
			type wrapper struct {
				io.ReadCloser
			}
			resp.Body = &wrapper{ReadCloser: resp.Body}
		}
	}
}

type hookReadCloser struct {
	io.ReadCloser
	hook func()

	// +checkatomic
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

type hookReadWriteCloser struct {
	hookReadCloser
	io.Writer
}

var _ io.ReadWriteCloser = (*hookReadWriteCloser)(nil)
