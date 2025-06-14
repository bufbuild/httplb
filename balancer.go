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

package httplb

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal"
	"github.com/bufbuild/httplb/internal/conns"
	"github.com/bufbuild/httplb/picker"
	"github.com/bufbuild/httplb/resolver"
	"golang.org/x/sync/errgroup"
)

var (
	errResolverReturnedNoAddresses = errors.New("resolver returned no addresses")      // +checklocksignore: mu is not required, but happens to always be held.
	errNoHealthyConnections        = errors.New("unavailable: no healthy connections") // +checklocksignore: mu is not required, but happens to always be held.
)

const (
	// reresolveMinPercent is the percentage of backends becoming unhealthy
	// that trigger a forced re-resolve of addresses.
	reresolveMinPercent = 50
)

func newBalancer(
	ctx context.Context,
	picker func(prev picker.Picker, allConns conn.Conns) picker.Picker,
	checker health.Checker,
	pool connPool,
	roundTripperMaxLifetime time.Duration,
) *balancer {
	ctx, cancel := context.WithCancel(ctx)
	balancer := &balancer{
		ctx:                     ctx,
		cancel:                  cancel,
		pool:                    pool,
		newPicker:               picker,
		healthChecker:           checker,
		roundTripperMaxLifetime: roundTripperMaxLifetime,
		resolverUpdates:         make(chan struct{}, 1),
		recycleConns:            make(chan struct{}, 1),
		closed:                  make(chan struct{}),
		connInfo:                map[conn.Conn]connInfo{},
		clock:                   internal.NewRealClock(),
	}
	balancer.connManager.updateFunc = balancer.updateConns
	return balancer
}

// connPool is an abstraction of *transportPool that makes it easier to test the balancer logic
// (see balancertesting.NewFakeConnPool).
type connPool interface {
	NewConn(resolver.Address) (conn.Conn, bool)
	RemoveConn(conn.Conn) bool
	UpdatePicker(picker.Picker, bool)
	ResolveNow()
}

type balancer struct {
	//nolint:containedCtx
	ctx           context.Context // +checklocksignore: mu is not required, but happens to always be held.
	cancel        context.CancelFunc
	pool          connPool
	newPicker     func(prev picker.Picker, allConns conn.Conns) picker.Picker // +checklocksignore: mu is not required, but happens to always be held.
	healthChecker health.Checker                                              // +checklocksignore: mu is not required, but happens to always be held.
	connManager   connManager

	roundTripperMaxLifetime time.Duration // +checklocksignore: mu is not required, but happens to always be held.

	// NB: only set from tests
	updateHook func([]resolver.Address, []conn.Conn)

	closed chan struct{}
	// closedErr is written before writing to closed chan, so can only be read
	// if closed chan is read first (this pattern is only safe/non-racy for
	// situations with a single writer)
	closedErr error

	latestAddrs     atomic.Pointer[[]resolver.Address]
	latestErr       atomic.Pointer[error]
	resolverUpdates chan struct{}
	recycleConns    chan struct{}
	clock           internal.Clock

	mu sync.Mutex
	// +checklocks:mu
	latestPicker picker.Picker
	// +checklocks:mu
	latestUsableConns conns.Set
	// +checklocks:mu
	conns []conn.Conn
	// +checklocks:mu
	connInfo map[conn.Conn]connInfo
	// +checklocks:mu
	connsToRecycle []conn.Conn
}

func (b *balancer) UpdateHealthState(connection conn.Conn, state health.State) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.connInfo[connection]
	if !ok {
		// when closing, we may remove an entry from b.connInfo, but
		// associated checker is still closing, so we may get a late
		// arriving update that we can ignore
		return
	}
	if info.state == state {
		// no change, nothing else to do
		return
	}
	info.state = state
	b.connInfo[connection] = info
	b.newPickerLocked()
}

func (b *balancer) warmedUp(connection conn.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.connInfo[connection]
	if !ok {
		// when closing, we may remove an entry from b.connInfo, but
		// associated warm-up routine could still be running, so we
		// may get a late arriving update that we can ignore
		return
	}
	info.warm = true
	b.connInfo[connection] = info
	b.newPickerLocked()
}

func (b *balancer) OnResolve(addresses []resolver.Address) {
	clone := make([]resolver.Address, len(addresses))
	copy(clone, addresses)
	b.latestAddrs.Store(&clone)
	select {
	case b.resolverUpdates <- struct{}{}:
	default:
	}
}

func (b *balancer) OnResolveError(err error) {
	b.latestErr.Store(&err)
	select {
	case b.resolverUpdates <- struct{}{}:
	default:
	}
}

func (b *balancer) Close() error {
	b.cancel()

	// Don't return until everything is done.
	<-b.closed
	return b.closedErr
}

func (b *balancer) start() {
	go b.receiveAddrs(b.ctx)
}

func (b *balancer) receiveAddrs(ctx context.Context) {
	defer func() {
		// Shutdown conn manager and health check process on the way out.
		grp, _ := errgroup.WithContext(context.Background())
		var closeErr atomic.Pointer[error]
		doClose := func(closer io.Closer) func() error {
			return func() error {
				if err := closer.Close(); err != nil {
					// We don't return an error since that would cancel all
					// of the other outstanding close tasks. We don't really
					// need to track all of the errors may happen, so we'll
					// just keep track of the last one.
					closeErr.CompareAndSwap(nil, &err)
				}
				return nil
			}
		}
		func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			for key, info := range b.connInfo {
				delete(b.connInfo, key)
				closer := info.closeChecker
				info.cancel()
				if closer != nil {
					grp.Go(doClose(closer))
				}
			}
		}()
		_ = grp.Wait()
		// All done!
		errPtr := closeErr.Load()
		if errPtr != nil {
			b.closedErr = *errPtr
		}
		close(b.closed)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.recycleConns:
			b.mu.Lock()
			connsToRecycle := b.connsToRecycle
			b.connsToRecycle = nil
			b.mu.Unlock()
			if len(connsToRecycle) > 0 {
				// TODO: In practice, this will often recycle all connections at the same time
				//       since they are often created at the same time (when we get results
				//       from the resolver) and they all have the same max lifetime.
				//       This should be fine in vast majority of cases, but it would pose
				//       resource-usage issues if, for example, there were 1000s of TLS
				//       connections (or even 100s of TLS connections that use client certs and
				//       a computationally expensive private key algorithm like RSA). We might
				//       consider rate-limiting here the rate at which we recycle connections,
				//       allowing connections to live a little beyond their max lifetime just
				//       so we don't recycle everything all at once.
				b.connManager.recycleConns(connsToRecycle)
			}

		case <-b.resolverUpdates:
			addrs := b.latestAddrs.Load()
			if addrs == nil {
				errPtr := b.latestErr.Load()
				if errPtr == nil {
					// No addresses and no error? Should not be possible...
					// We'll ignore until we see one or the other.
					continue
				}
				// reset error once we've observed it
				b.latestErr.CompareAndSwap(errPtr, nil)
				resolveErr := *errPtr
				if resolveErr == nil {
					// OnError(nil) was called, but should not have been
					resolveErr = errors.New("internal: resolver failed but did not report error")
				}
				b.setErrorPicker(resolveErr)
				continue
			}
			// TODO: Look at latestErr and log if non-nil? As is, a resolver could
			//       provide addresses and then subsequently provide errors, but those
			//       errors will be effectively ignored and the original addresses
			//       will continue to be used.
			// TODO: If we can get an update that says zero addresses but no error,
			//       should we respect it, and potentially close all connections?
			//       For now, we ignore the update, and keep the last known addresses.
			if len(*addrs) > 0 {
				addrsClone := make([]resolver.Address, len(*addrs))
				copy(addrsClone, *addrs)
				b.connManager.reconcileAddresses(addrsClone)
			}
		}
	}
}

func (b *balancer) updateConns(newAddrs []resolver.Address, removeConns []conn.Conn) []conn.Conn {
	if b.updateHook != nil {
		b.updateHook(newAddrs, removeConns)
	}
	numAdded := len(newAddrs)
	numRemoved := len(removeConns)
	addConns := make([]conn.Conn, 0, numAdded)
	for _, addr := range newAddrs {
		newConn, ok := b.pool.NewConn(addr)
		if ok {
			addConns = append(addConns, newConn)
		}
	}
	setToRemove := make(map[conn.Conn]struct{}, numRemoved)
	for _, c := range removeConns {
		setToRemove[c] = struct{}{}
	}

	// we wait until we've created a new picker before actually removing
	// the connections from the underlying pool
	defer func() {
		for _, c := range removeConns {
			b.pool.RemoveConn(c)
		}
	}()
	var checkClosers []io.Closer
	defer func() {
		for _, c := range checkClosers {
			_ = c.Close()
		}
	}()
	b.mu.Lock()
	defer b.mu.Unlock()
	newConns := make([]conn.Conn, 0, len(b.conns)+numAdded-numRemoved)
	for _, existing := range b.conns {
		if _, ok := setToRemove[existing]; ok {
			// close health check process for this connection
			// and omit it from newConns
			info := b.connInfo[existing]
			delete(b.connInfo, existing)
			info.cancel()
			if info.closeChecker != nil {
				checkClosers = append(checkClosers, info.closeChecker)
			}
			continue
		}
		newConns = append(newConns, existing)
	}
	newConns = append(newConns, addConns...)
	b.initConnInfoLocked(addConns)
	b.conns = newConns
	b.newPickerLocked()
	return addConns
}

// +checklocks:b.mu
func (b *balancer) initConnInfoLocked(conns []conn.Conn) {
	for i := range conns {
		connection := conns[i]
		connCtx, connCancel := context.WithCancel(b.ctx)
		healthChecker := b.healthChecker.New(connCtx, connection, b)
		cancel := connCancel
		if b.roundTripperMaxLifetime != 0 {
			timer := b.clock.AfterFunc(b.roundTripperMaxLifetime, func() {
				b.recycle(connection)
			})
			cancel = func() {
				connCancel()
				timer.Stop()
			}
		}
		b.connInfo[connection] = connInfo{closeChecker: healthChecker, cancel: cancel}
		go func() {
			if err := connection.Prewarm(connCtx); err == nil {
				b.warmedUp(connection)
			}
		}()
	}
}

// +checklocks:b.mu
func (b *balancer) newPickerLocked() {
	usable := b.computeUsableConnsLocked()
	if len(usable) == 0 {
		addrs := b.latestAddrs.Load()
		if addrs == nil || len(*addrs) == 0 {
			b.setErrorPickerLocked(errResolverReturnedNoAddresses)
		}
		// TODO: Should we set the picker to fail? Or should we let the client
		//       continue with previous picker (which may also fail, but it's
		//       not guaranteed to fail). Or maybe the client should be able to
		//       await (up to time limit) connections becoming healthy instead
		//       of failing fast?
		b.setErrorPickerLocked(errNoHealthyConnections)
		return
	}
	usableSet := conns.SetFromSlice(usable)
	if !usableSet.Equals(b.latestUsableConns) {
		// only recreate picker if the connections actually changed
		b.latestPicker = b.newPicker(b.latestPicker, conns.FromSlice(usable))
		b.latestUsableConns = usableSet
	}
	b.pool.UpdatePicker(b.latestPicker, b.isWarmLocked(usable))
}

// +checklocks:b.mu
func (b *balancer) isWarmLocked(conns []conn.Conn) bool {
	// TODO: possible future extension: make the definition of warm configurable
	for _, connection := range conns {
		info := b.connInfo[connection]
		if info.warm && info.state == health.StateHealthy {
			return true
		}
	}
	return false
}

// +checklocks:b.mu
func (b *balancer) computeUsableConnsLocked() []conn.Conn {
	// TODO: possible future extension: make the strategy for which connections to use configurable
	connsByState := map[health.State][]conn.Conn{}
	for _, connection := range b.conns {
		connState := b.connInfo[connection].state
		connsByState[connState] = append(connsByState[connState], connection)
	}
	// TODO: possible future extension: make these hard-coded values configurable
	minConns := 3
	if minPctConns := int(math.Round(float64(len(b.conns)) * 0.25)); minPctConns > minConns {
		minConns = minPctConns
	}

	var results []conn.Conn
	for candidateState := health.StateHealthy; candidateState != health.StateUnhealthy; candidateState++ {
		results = append(results, connsByState[candidateState]...)
		if len(results) >= minConns {
			break
		}
	}

	// If we have fewer usable connections than the re-resolve threshold, re-resolve.
	// TODO: make this logic for assessing re-resolve conditions configurable
	numHealthy := len(connsByState[health.StateHealthy])
	numTotal := len(b.conns)
	if int(math.Round(reresolveMinPercent*float64(numTotal)/100)) >= numHealthy {
		b.pool.ResolveNow()
	}

	return results
}

func (b *balancer) setErrorPicker(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setErrorPickerLocked(err)
}

// +checklocks:b.mu
func (b *balancer) setErrorPickerLocked(err error) {
	b.pool.UpdatePicker(picker.ErrorPicker(err), false)
}

func (b *balancer) recycle(c conn.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connsToRecycle = append(b.connsToRecycle, c)
	// Notify goroutine that there is a connection to recycle.
	select {
	case b.recycleConns <- struct{}{}:
	default:
	}
}

type connInfo struct {
	state health.State
	warm  bool

	// Cancels any in-progress warm-up and also cancels any timer
	// for recycling the connection. Invoked when the connection
	// is closed.
	cancel       context.CancelFunc
	closeChecker io.Closer
}

type connManager struct {
	// only used by a single goroutine, so no mutex necessary
	connsByAddr map[string][]conn.Conn

	updateFunc func([]resolver.Address, []conn.Conn) []conn.Conn
}

func (c *connManager) reconcileAddresses(addrs []resolver.Address) {
	// TODO: future extension: make connection establishing strategy configurable
	//       (which would allow more sophisticated connection strategies in the face
	//       of, for example, layer-4 load balancers)
	var newAddrs []resolver.Address
	var toRemove []conn.Conn
	// We allow subsetter to select the same address more than once. So
	// partition addresses by hostPort, to make reconciliation below easier.
	desired := make(map[string][]resolver.Address, len(addrs))
	for _, addr := range addrs {
		desired[addr.HostPort] = append(desired[addr.HostPort], addr)
	}
	remaining := make(map[string][]conn.Conn, len(c.connsByAddr))

	for hostPort, got := range c.connsByAddr {
		want := desired[hostPort]
		if len(want) > len(got) {
			// sync attributes of existing connection with new values from resolver
			for i := range got {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// and schedule new connections to be created
			remaining[hostPort] = got
			newAddrs = append(newAddrs, want[len(got):]...)
		} else {
			// sync attributes of existing connection with new values from resolver
			for i := range want {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// schedule extra connections to be removed
			remaining[hostPort] = got[:len(want)]
			toRemove = append(toRemove, got[len(want):]...)
		}
	}
	for hostPort, want := range desired {
		if _, ok := c.connsByAddr[hostPort]; ok {
			// already checked in loop above
			continue
		}
		newAddrs = append(newAddrs, want...)
	}

	c.connsByAddr = remaining
	c.doUpdate(newAddrs, toRemove)
}

func (c *connManager) doUpdate(newAddrs []resolver.Address, toRemove []conn.Conn) {
	// we make a single call to update connections in batch to create a single
	// new picker (avoids potential picker churn from making one change at a time)
	newConns := c.updateFunc(newAddrs, toRemove)
	// add newConns to set of connections
	for _, cn := range newConns {
		hostPort := cn.Address().HostPort
		c.connsByAddr[hostPort] = append(c.connsByAddr[hostPort], cn)
	}
}

func (c *connManager) recycleConns(connsToRecycle []conn.Conn) {
	var needToCompact bool
	for i, cn := range connsToRecycle {
		addr := cn.Address().HostPort
		existing := c.connsByAddr[addr]
		var found bool
		for i, existingConn := range existing {
			if existingConn == cn {
				found = true
				// remove cn from the slice
				copy(existing[i:], existing[i+1:])
				existing[len(existing)-1] = nil // don't leak memory
				c.connsByAddr[addr] = existing[:len(existing)-1]
				break
			}
		}
		if !found {
			// this connection has already been closed/removed
			connsToRecycle[i] = nil
			needToCompact = true
		}
	}
	if needToCompact {
		// TODO: when we can rely on Go 1.21, we should change this
		//       to use new std-lib function slices.DeleteFunc
		i := 0
		for _, cn := range connsToRecycle {
			if cn != nil {
				connsToRecycle[i] = cn
				i++
			}
		}
		if i == 0 {
			// nothing to actually recycle
			return
		}
		connsToRecycle = connsToRecycle[:i]
	}
	newAddrs := make([]resolver.Address, len(connsToRecycle))
	for i := range connsToRecycle {
		newAddrs[i] = connsToRecycle[i].Address()
	}

	c.doUpdate(newAddrs, connsToRecycle)
}
