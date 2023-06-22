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

package balancer

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/connmanager"
	"github.com/bufbuild/go-http-balancer/balancer/healthchecker"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/bufbuild/go-http-balancer/resolver"
	"golang.org/x/sync/errgroup"
)

var (
	errResolverReturnedNoAddresses = errors.New("resolver returned no addresses")      // +checklocksignore: mu is not required, but happens to always be held.
	errNoHealthyConnections        = errors.New("unavailable: no healthy connections") // +checklocksignore: mu is not required, but happens to always be held.
)

// NewFactory returns a new balancer factory. If no options are given,
// the default behavior is as follows:
// * No subsetting. Connections are created for all resolved addresses.
// * No health checks. All connections are considered usable.
// * Round-robin picker.
func NewFactory(opts ...NewFactoryOption) Factory {
	factory := &defaultBalancerFactory{}
	for _, opt := range opts {
		opt.apply(factory)
	}
	factory.applyDefaults()
	return factory
}

// NewFactoryOption is an option for configuring the behavior of a Factory
// return from NewFactory.
type NewFactoryOption interface {
	apply(balancer *defaultBalancerFactory)
}

// WithConnManager configures a balancer factory to use the given
// connmanager.Factory. It will be used to create a connmanager.ConnManager
// for each Balancer. The ConnManager is what decides what connections to
// create or remove.
func WithConnManager(connManager connmanager.Factory) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultBalancerFactory) {
		factory.connManager = connManager
	})
}

// WithPicker configures a balancer factory to use the given picker.Factory.
// A new picker will be created every time the set of usable connections
// changes for a given balancer.
func WithPicker(picker picker.Factory) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultBalancerFactory) {
		factory.picker = picker
	})
}

// WithHealthChecks configures a balancer factory to use the given health
// checker. This provides details about which resolved addresses are
// healthy or not. The given oracle is used to interpret the health check
// results and decide which connections are usable.
//
// If no such option is given, then no health checking is done and all
// connections (which remain in the Unknown health state) will be considered
// usable.
func WithHealthChecks(checker healthchecker.Checker, oracle healthchecker.UsabilityOracle) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultBalancerFactory) {
		factory.healthChecker = checker
		factory.usabilityOracle = oracle
	})
}

// WithWarmDefinition provides the definition of "warm" for a backend. The
// definition states that the connections to the backend are "warm" when there
// are at least the given number of connections in the given health state (or
// healthier). Note that only *usable* connections are considered. Usable
// connections are those returned from the balancer's UsabilityOracle (see
// WithHealthChecks).
//
// The numConns, if not zero, is an absolute minimum number of connections. So
// if this were set to 3, the backend isn't warm until there at least 3
// connections in the given state.
//
// The percentConns, if not zero, is a percentage (from 0 to 100) of connections.
// So if the total number of connections is 20 and this value is 10.0, then at
// least 2 usable connections (10% * 20 == 2) must reach the given health state
// for the backend to be considered warm.
//
// If both numConns and percentConns are specified, the minimum number of
// connections will be the maximum of the two.
//
// If no such option is provided, then default definition behaves as if
// the following were used:
//
//	balancer.WithWarmDefinition(1, 0, healthchecker.Unknown)
//
// So there must be at least one connection, and it must be either in the
// Unknown or Healthy state.
//
// The "warm" connections must also be considered warm per the
// httplb.RoundTripperFactory that created their underlying transport.
// If the result returned by the factory included a Prewarm function,
// it must complete before the connection is considered warm.
func WithWarmDefinition(numConns int, percentConns float64, state healthchecker.HealthState) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultBalancerFactory) {
		factory.warmMinCount = numConns
		factory.warmMinPercent = percentConns
		factory.warmMaxState = state
	})
}

type newFactoryOptionFunc func(factory *defaultBalancerFactory)

func (o newFactoryOptionFunc) apply(factory *defaultBalancerFactory) {
	o(factory)
}

type defaultBalancerFactory struct {
	connManager     connmanager.Factory
	picker          picker.Factory
	healthChecker   healthchecker.Checker
	usabilityOracle healthchecker.UsabilityOracle
	warmMinCount    int
	warmMinPercent  float64
	warmMaxState    healthchecker.HealthState
}

func (f *defaultBalancerFactory) applyDefaults() {
	if f.connManager == nil {
		f.connManager = connmanager.NewFactory()
	}
	if f.picker == nil {
		f.picker = picker.RoundRobinFactory
	}
	if f.healthChecker == nil {
		f.healthChecker = healthchecker.NoOpChecker
	}
	if f.usabilityOracle == nil {
		f.usabilityOracle = healthchecker.NewOracle(healthchecker.Unknown)
	}
}

func (f *defaultBalancerFactory) New(ctx context.Context, scheme, hostPort string, pool ConnPool) Balancer {
	ctx, cancel := context.WithCancel(ctx)
	balancer := &defaultBalancer{
		ctx:             ctx,
		cancel:          cancel,
		pool:            pool,
		picker:          f.picker,
		healthChecker:   f.healthChecker,
		usabilityOracle: f.usabilityOracle,
		warmMinCount:    f.warmMinCount,
		warmMinPercent:  f.warmMinPercent,
		warmMaxState:    f.warmMaxState,
		resolverUpdates: make(chan struct{}, 1),
		closed:          make(chan struct{}),
		connInfo:        map[conn.Conn]connInfo{},
	}
	balancer.connManager = f.connManager.New(ctx, scheme, hostPort, balancer.updateConns)
	go balancer.receiveAddrs(ctx)
	return balancer
}

type defaultBalancer struct {
	//nolint:containedCtx
	ctx             context.Context // +checklocksignore: mu is not required, but happens to always be held.
	cancel          context.CancelFunc
	pool            ConnPool
	connManager     connmanager.ConnManager       // +checklocksignore: mu is not required, but happens to always be held.
	picker          picker.Factory                // +checklocksignore: mu is not required, but happens to always be held.
	healthChecker   healthchecker.Checker         // +checklocksignore: mu is not required, but happens to always be held.
	usabilityOracle healthchecker.UsabilityOracle // +checklocksignore: mu is not required, but happens to always be held.
	warmMinCount    int
	warmMinPercent  float64
	warmMaxState    healthchecker.HealthState

	closed chan struct{}
	// closedErr is written before writing to closed chan, so can only be read
	// if closed chan is read first (this pattern is only safe/non-racy for
	// situations with a single writer)
	closedErr error

	latestAddrs     atomic.Pointer[[]resolver.Address]
	latestErr       atomic.Pointer[error]
	resolverUpdates chan struct{}

	mu sync.Mutex
	// +checklocks:mu
	latestPicker picker.Picker
	// +checklocks:mu
	latestUsableConns conn.Set
	// +checklocks:mu
	conns []conn.Conn
	// +checklocks:mu
	connInfo map[conn.Conn]connInfo
}

type connInfo struct {
	state        healthchecker.HealthState
	warm         bool
	cancelWarm   context.CancelFunc
	closeChecker io.Closer
}

func (b *defaultBalancer) UpdateHealthState(connection conn.Conn, state healthchecker.HealthState) {
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

func (b *defaultBalancer) warmedUp(connection conn.Conn) {
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

func (b *defaultBalancer) OnResolve(addresses []resolver.Address) {
	clone := make([]resolver.Address, len(addresses))
	copy(clone, addresses)
	b.latestAddrs.Store(&clone)
	select {
	case b.resolverUpdates <- struct{}{}:
	default:
	}
}

func (b *defaultBalancer) OnResolveError(err error) {
	b.latestErr.Store(&err)
	select {
	case b.resolverUpdates <- struct{}{}:
	default:
	}
}

func (b *defaultBalancer) Close() error {
	b.cancel()

	// Don't return until everything is done.
	<-b.closed
	return b.closedErr
}

func (b *defaultBalancer) receiveAddrs(ctx context.Context) {
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
		grp.Go(doClose(b.connManager))
		func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			for key, info := range b.connInfo {
				delete(b.connInfo, key)
				closer := info.closeChecker
				info.cancelWarm()
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
				b.connManager.ReconcileAddresses(addrsClone)
			}
		}
	}
}

func (b *defaultBalancer) updateConns(newAddrs []resolver.Address, removeConns []conn.Conn) []conn.Conn {
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
			// TODO: If this returns false, ConnManager is not well-behaved.
			//       Should we log or notify somehow?
			b.pool.RemoveConn(c)
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
			info.cancelWarm()
			if info.closeChecker != nil {
				_ = info.closeChecker.Close()
			}
			continue
		}
		newConns = append(newConns, existing)
	}
	newConns = append(newConns, addConns...)
	for i := range addConns {
		connection := addConns[i]
		connCtx, connCancel := context.WithCancel(b.ctx)
		healthChecker := b.healthChecker.New(connCtx, connection, b)
		go func() {
			defer connCancel()
			if err := connection.Prewarm(connCtx); err == nil {
				b.warmedUp(connection)
			}
		}()
		b.connInfo[connection] = connInfo{closeChecker: healthChecker, cancelWarm: connCancel}
	}
	b.conns = newConns
	b.newPickerLocked()
	return addConns
}

// +checklocks:b.mu
func (b *defaultBalancer) connHealthLocked(c conn.Conn) healthchecker.HealthState {
	return b.connInfo[c].state
}

// +checklocks:b.mu
func (b *defaultBalancer) newPickerLocked() {
	usable := b.usabilityOracle(conn.ConnectionsFromSlice(b.conns), b.connHealthLocked)
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
	usableSet := conn.SliceToSet(usable)
	if !usableSet.Equals(b.latestUsableConns) {
		// only recreate picker if the connections actually changed
		b.latestPicker = b.picker.New(b.latestPicker, conn.ConnectionsFromSlice(usable))
		b.latestUsableConns = usableSet
	}
	b.pool.UpdatePicker(b.latestPicker, b.isWarmLocked(usable, len(b.conns)))
}

// +checklocks:b.mu
func (b *defaultBalancer) isWarmLocked(conns []conn.Conn, allLen int) bool {
	minConns := b.warmMinCount
	if minPctConns := int(math.Round(b.warmMinPercent * float64(allLen) / 100)); minPctConns > minConns {
		minConns = minPctConns
	}
	if minConns < 1 {
		// definitely not warmed up if no connections are warm and adequately healthy
		minConns = 1
	}
	if minConns > allLen {
		// 100% is good enough, even if less than warmMinCount
		minConns = allLen
	}
	var warmedCount int
	for _, connection := range conns {
		info := b.connInfo[connection]
		if info.warm && info.state <= b.warmMaxState {
			warmedCount++
			if warmedCount == minConns {
				return true
			}
		}
	}
	return false
}

func (b *defaultBalancer) setErrorPicker(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setErrorPickerLocked(err)
}

// +checklocks:b.mu
func (b *defaultBalancer) setErrorPickerLocked(err error) {
	b.pool.UpdatePicker(picker.ErrorPicker(err), false)
}
