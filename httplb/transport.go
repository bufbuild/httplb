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

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/balancer"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/bufbuild/go-http-balancer/resolver"
	"golang.org/x/sync/errgroup"
)

var (
	errTransportIsClosed = errors.New("transport is closed")
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

	mu sync.RWMutex
	// +checklocks:mu
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

	pool = newTransportPool(m.rootCtx, targetOpts.resolver, targetOpts.balancer, dest, applyTimeout, schemeConf, opts, m.clientOptions.resourceLeakCallback)
	var activity chan struct{}
	if !opts.KeepWarm {
		activity = make(chan struct{}, 1)
		go m.closeWhenIdle(m.rootCtx, dest, pool, activity)
	}
	m.pools[dest] = transportPoolEntry{pool: pool, activity: activity}
	return pool, nil
}

// +checklocksread:m.mu
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
	dest                 target // +checklocksignore: mu is not required, it just happens to be held always.
	applyRequestTimeout  func(ctx context.Context) (context.Context, context.CancelFunc)
	roundTripperFactory  RoundTripperFactory // +checklocksignore: mu is not required, it just happens to be held always.
	roundTripperOptions  RoundTripperOptions // +checklocksignore: mu is not required, it just happens to be held always.
	pickerInitialized    chan struct{}
	resourceLeakCallback func(req *http.Request, resp *http.Response)
	resolverCloser       io.Closer
	balancer             balancer.Balancer
	closeComplete        chan struct{}

	picker atomic.Pointer[picker.Picker]

	mu sync.RWMutex
	// +checklocks:mu
	isWarm bool
	// +checklocks:mu
	warmCond *sync.Cond
	// +checklocks:mu
	conns []*connection
	// +checklocks:mu
	closed bool
}

func newTransportPool(
	ctx context.Context,
	res resolver.Resolver,
	balancerFactory balancer.Factory,
	dest target,
	applyTimeout func(ctx context.Context) (context.Context, context.CancelFunc),
	rtFactory RoundTripperFactory,
	opts RoundTripperOptions,
	resourceLeakCallback func(req *http.Request, resp *http.Response),
) *transportPool {
	pickerInitialized := make(chan struct{})
	pool := &transportPool{
		dest:                 dest,
		applyRequestTimeout:  applyTimeout,
		roundTripperFactory:  rtFactory,
		roundTripperOptions:  opts,
		pickerInitialized:    pickerInitialized,
		resourceLeakCallback: resourceLeakCallback,
		closeComplete:        make(chan struct{}),
	}
	pool.warmCond = sync.NewCond(&pool.mu)
	pool.balancer = balancerFactory.New(ctx, dest.scheme, dest.hostPort, pool)
	pool.resolverCloser = res.Resolve(ctx, dest.scheme, dest.hostPort, pool.balancer)
	return pool
}

func (t *transportPool) NewConn(address resolver.Address) conn.Conn {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.roundTripperFactory.New(t.dest.scheme, address.HostPort, t.roundTripperOptions)
	newConn := &connection{
		scheme:  result.Scheme,
		addr:    address.HostPort,
		conn:    result.RoundTripper,
		close:   result.Close,
		prewarm: result.PreWarm,
	}
	newConn.UpdateAttributes(address.Attributes)

	// make copy of t.conns that has newConn at the end
	length := len(t.conns)
	newConns := make([]*connection, length+1)
	copy(newConns, t.conns)
	newConns[length] = newConn
	t.conns = newConns

	return newConn
}

func (t *transportPool) RemoveConn(toRemove conn.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// make copy of t.conns that has toRemove omitted
	newConns := make([]*connection, 0, len(t.conns)-1)
	found := false
	for _, connection := range t.conns {
		if connection == toRemove {
			found = true
			continue
		}
		newConns = append(newConns, connection)
	}
	t.conns = newConns
	if found {
		//nolint:forcetypeassert // if must be this type or else found could not be true
		go t.closeConn(toRemove.(*connection))
	}
}

func (t *transportPool) closeConn(toClose *connection) {
	// TODO: If a conn has any active/pending requests, we should
	//       wait for activity to cease before calling conn.close()
	if toClose.close != nil {
		toClose.close()
	}
}

func (t *transportPool) UpdatePicker(picker picker.Picker, isWarm bool) {
	if t.picker.CompareAndSwap(nil, &picker) {
		close(t.pickerInitialized)
	} else {
		t.picker.Store(&picker)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.isWarm = isWarm
	if isWarm {
		t.warmCond.Broadcast()
	}
}

func (t *transportPool) RoundTrip(request *http.Request) (*http.Response, error) {
	chosen, whenDone, err := t.getConnection(request)
	if err != nil {
		return nil, err
	}
	var cancel context.CancelFunc
	if t.applyRequestTimeout != nil {
		var ctx context.Context
		ctx, cancel = t.applyRequestTimeout(request.Context())
		request = request.WithContext(ctx)
	}

	// rewrite request if necessary
	chosenScheme, chosenAddr := chosen.Scheme(), chosen.Address().HostPort
	if (chosenScheme != "" && chosenScheme != request.URL.Scheme) || chosenAddr != request.URL.Host {
		request = request.Clone(request.Context())
		if chosenScheme != "" {
			request.URL.Scheme = chosenScheme
		}
		request.URL.Host = chosenAddr
	}

	resp, err := chosen.RoundTripper().RoundTrip(request)
	if err != nil {
		return nil, err
	}
	addCompletionHook(request, resp, func() {
		// TODO: may need to use WaitGroup or similar so we can await
		//       completion of active requests
		if cancel != nil {
			cancel()
		}
		if whenDone != nil {
			whenDone()
		}
	}, t.resourceLeakCallback)
	return resp, nil
}

func (t *transportPool) getConnection(request *http.Request) (conn.Conn, func(), error) {
	pickerPtr := t.picker.Load()

	if pickerPtr == nil {
		<-t.pickerInitialized
		pickerPtr = t.picker.Load()
	}

	if pickerPtr == nil {
		// should not be possible
		return nil, nil, errors.New("internal: picker not initialized")
	}
	return (*pickerPtr).Pick(request)
}

func (t *transportPool) prewarm(ctx context.Context) error {
	if !t.roundTripperOptions.KeepWarm {
		// not keeping this one warm...
		return nil
	}
	select {
	case <-t.pickerInitialized:
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
	for _, current := range conns {
		// TODO: Do we need to warmup *every* address? Probably not...
		if current.prewarm != nil {
			current := current
			grp.Go(func() error {
				return current.prewarm(grpCtx, current.addr)
			})
		}
	}
	if err := grp.Wait(); err != nil {
		return err
	}

	// Finally, we await the balancer indicating that connections
	// are adequately healthy and usable.

	// TODO: This stinks, but we do it because sync.Cond does not
	//       respect context :(
	returned := make(chan struct{})
	defer close(returned)
	go func() {
		select {
		case <-returned:
			return
		case <-ctx.Done():
			// break the loop below out of waiting when the
			// context closes by broadcasting on the condition
			t.mu.Lock()
			defer t.mu.Unlock()
			t.warmCond.Broadcast()
			return
		}
	}()
	t.mu.Lock()
	defer t.mu.Unlock()
	for {
		if t.isWarm {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		t.warmCond.Wait()
	}
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
	// Close resolver first. This will stop any new addresses from
	// being sent to the balancer.
	_ = t.resolverCloser.Close()
	// Then close the balancer. This will stop calls to create and
	// remove connections.
	_ = t.balancer.Close()
	// Now we can stop all the connections in the pool
	grp, _ := errgroup.WithContext(context.Background())
	for _, current := range conns {
		current := current
		grp.Go(func() error {
			t.closeConn(current)
			return nil
		})
	}
	_ = grp.Wait()
	close(t.closeComplete)
}

type connection struct {
	scheme  string
	addr    string
	attrs   atomic.Pointer[attrs.Attributes]
	conn    http.RoundTripper
	close   func()
	prewarm func(context.Context, string) error
}

func (c *connection) Scheme() string {
	return c.scheme
}

func (c *connection) Address() resolver.Address {
	addr := resolver.Address{HostPort: c.addr}
	if attr := c.attrs.Load(); attr != nil {
		addr.Attributes = *attr
	}
	return addr
}

func (c *connection) UpdateAttributes(attributes attrs.Attributes) {
	c.attrs.Store(&attributes)
}

func (c *connection) RoundTripper() http.RoundTripper {
	return c.conn
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
