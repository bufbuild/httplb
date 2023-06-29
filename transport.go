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
	"math"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal"
	"github.com/bufbuild/httplb/picker"
	"github.com/bufbuild/httplb/resolver"
	"golang.org/x/sync/errgroup"
)

var (
	errTransportIsClosed = errors.New("transport is closed")
	errTryAgain          = errors.New("internal: leaf transport closed; try again")
)

// mainTransport is the root of the transport hierarchy. For each target
// backend (scheme + host:port), it maintains a transportPool. It
// implements http.RoundTripper and is used as the transport for clients
// created via NewClient.
//
// Its implementation of RoundTrip delegates to a transportPool that
// corresponds to the scheme + host:port in the request URL.
type mainTransport struct {
	rootCtx              context.Context //nolint:containedctx
	cancel               context.CancelFunc
	idleTransportTimeout time.Duration
	clientOptions        *clientOptions
	clock                internal.Clock

	runningPools sync.WaitGroup

	mu sync.RWMutex
	// +checklocks:mu
	pools map[target]transportPoolEntry
	// +checklocks:mu
	closed bool
}

func newTransport(opts *clientOptions) *mainTransport {
	ctx, cancel := context.WithCancel(opts.rootCtx)
	transport := &mainTransport{
		rootCtx:              ctx,
		cancel:               cancel,
		clientOptions:        opts,
		clock:                internal.NewRealClock(),
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
	// Don't return until everything is cleaned up
	m.runningPools.Wait()
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
	for {
		pool, err := m.getOrCreatePool(dest)
		if err != nil {
			return nil, err
		}
		resp, err := pool.RoundTrip(request)
		if errors.Is(err, errTryAgain) {
			continue
		}
		return resp, err
	}
}

// CloseIdleConnections is exported so that the method of the same name
// on *[http.Client] works as expected.
func (m *mainTransport) CloseIdleConnections() {
	var pools []*transportPool
	func() {
		m.mu.RLock()
		defer m.mu.RUnlock()
		pools = make([]*transportPool, 0, len(m.pools))
		for _, entry := range m.pools {
			pools = append(pools, entry.pool)
		}
	}()
	for _, pool := range pools {
		pool.CloseIdleConnections()
	}
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

	m.runningPools.Add(1)
	pool = newTransportPool(
		m.rootCtx,
		targetOpts.resolver,
		targetOpts.picker,
		targetOpts.healthChecker,
		dest,
		applyTimeout,
		schemeConf,
		opts,
		m.runningPools.Done,
	)
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
	timer := m.clock.NewTimer(m.idleTransportTimeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.Chan():
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

// transportPool is a round tripper that is actually a pool
// of other transports. The other transports, or "connections",
// are managed by a balancer. Particular transports are
// selected for a request by a [picker.Picker].
type transportPool struct {
	dest                target // +checklocksignore: mu is not required, it just happens to be held always.
	applyRequestTimeout func(ctx context.Context) (context.Context, context.CancelFunc)
	roundTripperFactory RoundTripperFactory // +checklocksignore: mu is not required, it just happens to be held always.
	roundTripperOptions RoundTripperOptions // +checklocksignore: mu is not required, it just happens to be held always.
	pickerInitialized   chan struct{}
	resolver            io.Closer
	reresolve           chan struct{}
	balancer            *balancer
	closeComplete       chan struct{}
	onClose             func()

	picker atomic.Pointer[picker.Picker]

	mu sync.RWMutex
	// +checklocks:mu
	isWarm bool
	// +checklocks:mu
	warmCond *sync.Cond
	// +checklocks:mu
	conns []*connection // active
	// +checklocks:mu
	removedConns []*connection // pending closure
	// +checklocks:mu
	closed bool
}

func newTransportPool(
	ctx context.Context,
	res resolver.Factory,
	pickerFactory picker.Factory,
	checker health.Checker,
	dest target,
	applyTimeout func(ctx context.Context) (context.Context, context.CancelFunc),
	rtFactory RoundTripperFactory,
	opts RoundTripperOptions,
	onClose func(),
) *transportPool {
	pickerInitialized := make(chan struct{})
	pool := &transportPool{
		dest:                dest,
		applyRequestTimeout: applyTimeout,
		roundTripperFactory: rtFactory,
		roundTripperOptions: opts,
		pickerInitialized:   pickerInitialized,
		closeComplete:       make(chan struct{}),
		reresolve:           make(chan struct{}, 1),
		onClose:             onClose,
	}
	pool.warmCond = sync.NewCond(&pool.mu)
	pool.balancer = newBalancer(ctx, pickerFactory, checker, pool)
	pool.resolver = res.New(ctx, dest.scheme, dest.hostPort, pool.balancer, pool.reresolve)
	pool.balancer.start()
	return pool
}

func (t *transportPool) NewConn(address resolver.Address) (conn.Conn, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, false
	}

	result := t.roundTripperFactory.New(t.dest.scheme, address.HostPort, t.roundTripperOptions)
	newConn := &connection{
		scheme:    result.Scheme,
		addr:      address.HostPort,
		conn:      result.RoundTripper,
		doPrewarm: result.Prewarm,
		closed:    make(chan struct{}),
	}
	if newConn.scheme == "" {
		newConn.scheme = t.dest.scheme
	}
	newConn.doClose = func() {
		if result.Close != nil {
			result.Close()
		}
		t.connClosed(newConn)
	}

	newConn.UpdateAttributes(address.Attributes)

	// make copy of t.conns that has newConn at the end
	length := len(t.conns)
	newConns := make([]*connection, length+1)
	copy(newConns, t.conns)
	newConns[length] = newConn
	t.conns = newConns

	return newConn, true
}

func (t *transportPool) RemoveConn(toRemove conn.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	// make copy of t.conns that has toRemove omitted
	newLen := len(t.conns) - 1
	if newLen < 0 {
		newLen = 0
	}
	newConns := make([]*connection, 0, newLen)
	found := false
	for _, connection := range t.conns {
		if connection == toRemove {
			found = true
			continue
		}
		newConns = append(newConns, connection)
	}
	t.conns = newConns
	if !found {
		return false
	}
	//nolint:forcetypeassert,errcheck // if must be this type or else found could not be true
	c := toRemove.(*connection)
	t.removedConns = append(t.removedConns, c)
	go c.close()
	return true
}

func (t *transportPool) connClosed(closedConn conn.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// make copy of t.removedConns that has closedConn omitted
	newLen := len(t.removedConns) - 1
	if newLen < 0 {
		newLen = 0
	}
	newRemovedConns := make([]*connection, 0, newLen)
	for _, connection := range t.conns {
		if connection == closedConn {
			continue
		}
		newRemovedConns = append(newRemovedConns, connection)
	}
	t.removedConns = newRemovedConns
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

func (t *transportPool) ResolveNow() {
	select {
	case t.reresolve <- struct{}{}:
	default:
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

	return chosen.RoundTrip(request, func() {
		if cancel != nil {
			cancel()
		}
		if whenDone != nil {
			whenDone()
		}
	})
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
	t.mu.RLock()
	warm, closed := t.isWarm, t.closed
	t.mu.RUnlock()
	if warm || closed {
		return nil
	}

	// We must await the balancer indicating that connections
	// are warmed up.

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

func (t *transportPool) CloseIdleConnections() {
	t.mu.RLock()
	conns, alreadyClosed := t.conns, t.closed
	t.mu.RUnlock()
	if alreadyClosed {
		return
	}
	for _, leafTransport := range conns {
		type closeIdler interface {
			CloseIdleConnections()
		}
		if closer, ok := leafTransport.conn.(closeIdler); ok {
			closer.CloseIdleConnections()
		}
	}
}

func (t *transportPool) close() {
	t.mu.Lock()
	conns, removedConns, alreadyClosed := t.conns, t.removedConns, t.closed
	t.closed = true
	t.mu.Unlock()
	if alreadyClosed {
		<-t.closeComplete
		return
	}
	// Close resolver first. This will stop any new addresses from
	// being sent to the balancer.
	_ = t.resolver.Close()
	// Then close the balancer. This will stop calls to create and
	// remove connections.
	_ = t.balancer.Close()
	// Now we can stop all the connections in the pool. We do this
	// for removed connections, too, to make sure we wait for all
	// activity to complete before returning. There could still be
	// in-progress operations on removed connections that we need
	// to wait for.
	grp, _ := errgroup.WithContext(context.Background())
	for _, connSlice := range [][]*connection{conns, removedConns} {
		for _, current := range connSlice {
			current := current
			grp.Go(func() error {
				current.close()
				return nil
			})
		}
	}
	_ = grp.Wait()
	close(t.reresolve)
	<-t.reresolve // drain channel
	close(t.closeComplete)
	if t.onClose != nil {
		t.onClose()
	}
}

type connection struct {
	scheme string
	addr   string
	conn   http.RoundTripper
	attrs  atomic.Pointer[resolver.Attrs]

	doClose   func()
	doPrewarm func(context.Context, string, string) error

	closed chan struct{}
	// +checkatomic
	outstandingRequests atomic.Int64 // negative value means closing, no more requests
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

func (c *connection) UpdateAttributes(attrs resolver.Attrs) {
	c.attrs.Store(&attrs)
}

func (c *connection) RoundTrip(req *http.Request, whenDone func()) (*http.Response, error) {
	if !c.startRequest() {
		if whenDone != nil {
			whenDone()
		}
		return nil, errTryAgain
	}
	onFinish := func() {
		if whenDone != nil {
			whenDone()
		}
		c.endRequest()
	}
	resp, err := c.conn.RoundTrip(req)
	if err != nil {
		onFinish()
		return nil, err
	}
	addCompletionHook(resp, onFinish)
	return resp, nil
}

func (c *connection) Prewarm(ctx context.Context) error {
	if c.doPrewarm == nil {
		return nil
	}
	return c.doPrewarm(ctx, c.scheme, c.addr)
}

func (c *connection) startRequest() bool {
	result := c.outstandingRequests.Add(1)
	if result < 0 {
		// marked as closed, abort the request we started
		c.outstandingRequests.Add(-1)
		return false
	}
	return true
}

func (c *connection) endRequest() {
	result := c.outstandingRequests.Add(-1)
	if result == math.MinInt64 {
		close(c.closed)
	}
}

func (c *connection) close() {
	// Mark closed.
	for {
		reqs := c.outstandingRequests.Load()
		if reqs < 0 {
			// already marked
			break
		}
		markedValue := reqs - math.MinInt64
		if c.outstandingRequests.CompareAndSwap(reqs, markedValue) {
			// marked!
			if markedValue == math.MinInt64 {
				// there were no active requests, so we can close
				close(c.closed)
			}
			break
		}
		// another thread updated outstandingRequests, try again
	}

	// Wait for requests to acquiesce.
	<-c.closed

	// Finally, close the underlying round-tripper.
	if c.doClose != nil {
		c.doClose()
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

func addCompletionHook(
	resp *http.Response,
	whenComplete func(),
) {
	bodyWriter, isWriter := resp.Body.(io.Writer)
	if isWriter {
		resp.Body = &hookReadWriteCloser{
			hookReadCloser: hookReadCloser{ReadCloser: resp.Body, hook: whenComplete},
			Writer:         bodyWriter,
		}
	} else {
		resp.Body = &hookReadCloser{ReadCloser: resp.Body, hook: whenComplete}
	}
}
