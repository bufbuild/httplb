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

type NewFactoryOption interface {
	apply(balancer *defaultBalancerFactory)
}

// WithConnManager configures a balancer factory to use the given
// connmanager.Factory. It will be used to create a connmanager.ConnManager
// for each Balancer. The ConnManager is what decides what connections to
// create or remove.
func WithConnManager(connManager connmanager.Factory) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.connManager = connManager
	})
}

// WithPicker configures a balancer factory to use the given picker.Factory.
// A new picker will be created every time the set of usable connections
// changes for a given balancer.
func WithPicker(picker picker.Factory) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.picker = picker
	})
}

// WithHealthChecks configures a balancer factory to use the given health
// checker. This provides details about which resolved addresses are
// healthy or not. The given oracle is used to interpret the health check
// results and decide which connections are usable.
func WithHealthChecks(checker healthchecker.Checker, oracle healthchecker.UsabilityOracle) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.healthChecker = checker
		factory.usabilityOracle = oracle
	})
}

type newFactoryOption func(factory *defaultBalancerFactory)

func (o newFactoryOption) apply(factory *defaultBalancerFactory) {
	o(factory)
}

type defaultBalancerFactory struct {
	connManager     connmanager.Factory
	picker          picker.Factory
	healthChecker   healthchecker.Checker
	usabilityOracle healthchecker.UsabilityOracle
}

func (f *defaultBalancerFactory) applyDefaults() {
	if f.connManager == nil {
		f.connManager = connmanager.NewFactory(connmanager.UseAll())
	}
	if f.picker == nil {
		f.picker = picker.ChooseFirstFactory // TODO: use round-robin once implemented
	}
	if f.healthChecker == nil {
		f.healthChecker = healthchecker.NoOpChecker
	}
	if f.usabilityOracle == nil {
		f.usabilityOracle = healthchecker.DefaultUsabilityOracle
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
		resolverUpdates: make(chan struct{}, 1),
		closed:          make(chan struct{}),
		health:          map[conn.Conn]connHealth{},
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
	conns []conn.Conn
	// +checklocks:mu
	health map[conn.Conn]connHealth
}

type connHealth struct {
	state        healthchecker.HealthState
	closeChecker io.Closer
}

func (b *defaultBalancer) UpdateHealthState(connection conn.Conn, state healthchecker.HealthState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.health[connection]
	if !ok {
		// when closing, we may remove an entry from b.health, but
		// associated checker is still closing, so we may get a late
		// arriving update that we can ignore
		return
	}
	if info.state == state {
		// no change, nothing else to do
		return
	}
	info.state = state
	b.health[connection] = info
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
		// Shutdown health check process on the way out.
		grp, _ := errgroup.WithContext(context.Background())
		var closeErr atomic.Pointer[error]
		func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			for key, health := range b.health {
				delete(b.health, key)
				closer := health.closeChecker
				if closer != nil {
					grp.Go(func() error {
						if err := closer.Close(); err != nil {
							// we'll just retain the first error encountered
							closeErr.CompareAndSwap(nil, &err)
						}
						return nil
					})
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
				err := b.latestErr.Load()
				if err == nil {
					// No addresses and no error? Should not be possible...
					// We'll ignore until we see one or the other.
					continue
				}
				resolveErr := *err
				if resolveErr == nil {
					// OnError(nil) was called, but should not have been
					resolveErr = errors.New("internal: resolver failed but did not report error")
				}
				b.setErrorPicker(resolveErr)
				continue
			}
			// TODO: If we can get an update that says zero addresses but no error,
			//       should we respect it, and potentially close all connections?
			//       For now, we ignore the update, and keep the last known addresses.
			if len(*addrs) > 0 {
				b.connManager.ReconcileAddresses(*addrs)
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
			break
		}
		newConns = append(newConns, existing)
	}
	newConns = append(newConns, addConns...)
	for i, c := range addConns {
		healthChecker := b.healthChecker.New(b.ctx, addConns[i], b)
		b.health[c] = connHealth{closeChecker: healthChecker}
	}
	b.conns = newConns
	b.newPickerLocked()
	return addConns
}

// +checklocks:b.mu
func (b *defaultBalancer) connHealthLocked(c conn.Conn) healthchecker.HealthState {
	return b.health[c].state
}

// +checklocks:b.mu
func (b *defaultBalancer) newPickerLocked() {
	usable := b.usabilityOracle(connections(b.conns), b.connHealthLocked)
	if len(usable) == 0 {
		addrs := b.latestAddrs.Load()
		if addrs == nil || len(*addrs) == 0 {
			b.setErrorPickerLocked(errResolverReturnedNoAddresses)
		}
		// TODO: Should we set the picker to fail? Or should we let the client
		//       continue with previous picker (which may also fail, but it's
		//       no guaranteed to fail). Or maybe the client should be able to
		//       await (up to time limit) connections becoming healthy instead
		//       of failing fast?
		b.setErrorPickerLocked(errNoHealthyConnections)
		return
	}
	b.latestPicker = b.picker.New(b.latestPicker, connections(usable))
	// TODO: Configurable way to assess if pool is "warm" or not. Until it's
	//       configurable, we consider having at least one usable connection
	//       to be warm.
	b.pool.UpdatePicker(b.latestPicker, true)
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

type connections []conn.Conn

func (c connections) Len() int {
	return len(c)
}

func (c connections) Get(i int) conn.Conn {
	return c[i]
}
