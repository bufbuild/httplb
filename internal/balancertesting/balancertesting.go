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

// Package balancertesting provides some helper functions and types that
// can be useful when testing custom load balancer implementations.
package balancertesting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/bufbuild/httplb/attribute"
	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal/conns"
	"github.com/bufbuild/httplb/picker"
	"github.com/bufbuild/httplb/resolver"
)

// FakeConn is an implementation of conn.Conn that can be used for testing.
// It is not usable for actual request traffic: attempts to call its
// RoundTrip method will always result in error.
//
// To create new instances of FakeConn, use a FakeConnPool.
type FakeConn struct {
	Index int

	addr    atomic.Pointer[resolver.Address]
	prewarm func(conn.Conn, context.Context) error
}

// Scheme implements the conn.Conn interface. For a FakeConn, it always
// returns "http".
func (c *FakeConn) Scheme() string {
	return "http"
}

// Address implements the conn.Conn interface. It returns the resolved
// address associated with this connection.
func (c *FakeConn) Address() resolver.Address {
	return *c.addr.Load()
}

// UpdateAttributes implements the conn.Conn interface. It updates the
// attributes on this connection's associated address.
func (c *FakeConn) UpdateAttributes(values attribute.Values) {
	addr := c.Address()
	addr.Attributes = values
	c.addr.Store(&addr)
}

// RoundTrip implements the conn.Conn interface. For a FakeConn, it
// always returns an error as a FakeConn is not meant for real requests.
func (c *FakeConn) RoundTrip(*http.Request, func()) (*http.Response, error) {
	return nil, errors.New("FakeConn does not support RoundTrip")
}

// Prewarm implements the conn.Conn interface. For a FakeConn, it will
// call the FakeConnPool's Prewarm function, if it was set. Otherwise,
// it returns nil immediately.
func (c *FakeConn) Prewarm(ctx context.Context) error {
	if c.prewarm == nil {
		return nil
	}
	return c.prewarm(c, ctx)
}

// FakeConnPool is an implementation of balancer.ConnPool that can be used
// for testing balancer.Balancer implementations. It marks the connections
// created with its NewConn method with an index in sequential order. So the
// first connection created is a *FakeConn with an Index of 1. The second
// will have Index 2, and so on.
//
// See NewFakeConnPool.
type FakeConnPool struct {
	// Prewarm can be set to a function that is invoked by the Prewarm
	// method of connections created by this pool. It should be set
	// immediately after the pool is created, before any connections
	// are created, to avoid races.
	Prewarm func(conn.Conn, context.Context) error // +checklocksignore: mu is not required, but happens to always be held.

	pickerUpdate chan struct{}
	connsUpdate  chan struct{}
	resolveNowCh chan struct{}

	mu sync.Mutex
	// +checklocks:mu
	index int
	// +checklocks:mu
	active conns.Set
	// +checklocks:mu
	picker PickerState
	// +checklocks:mu
	resolveNowCount int
}

// NewFakeConnPool constructs a new FakeConnPool.
func NewFakeConnPool() *FakeConnPool {
	return &FakeConnPool{
		pickerUpdate: make(chan struct{}, 1),
		connsUpdate:  make(chan struct{}, 1),
		resolveNowCh: make(chan struct{}, 1),
	}
}

// NewConn implements the balancer.ConnPool interface. It always returns
// *FakeConn instances. Test code can asynchronously await a call to NewConn
// or RemoveConn using the AwaitConnUpdate method.
func (p *FakeConnPool) NewConn(address resolver.Address) (conn.Conn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active == nil {
		p.active = conns.Set{}
	}
	p.index++
	newConn := &FakeConn{Index: p.index, prewarm: p.Prewarm}
	newConn.addr.Store(&address)
	p.active[newConn] = struct{}{}
	select {
	case p.connsUpdate <- struct{}{}:
	default:
	}
	return newConn, true
}

// RemoveConn implements the balancer.ConnPool interface. Test code can
// asynchronously await a call to NewConn or RemoveConn using the
// AwaitConnUpdate method.
func (p *FakeConnPool) RemoveConn(toRemove conn.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.active[toRemove]; !ok {
		// Our balancer and conn manager impls should be well-behaved. So this should never happen.
		// So instead of returning false, let's freak out and make sure the test fails.
		panic("misbehaving balancer or conn manager") //nolint:forbidigo
	}
	delete(p.active, toRemove)
	select {
	case p.connsUpdate <- struct{}{}:
	default:
	}
	return true
}

// GetConns implements the balancer.ConnPool interface. It returns a
// snapshot of the pool's set of active connections.
func (p *FakeConnPool) Conns() conn.Conns {
	p.mu.Lock()
	defer p.mu.Unlock()
	return conns.FromSet(p.active)
}

// UpdatePicker implements the balancer.ConnPool interface. Test code can
// asynchronously await a call to UpdatePicker using the AwaitPickerUpdate method.
func (p *FakeConnPool) UpdatePicker(picker picker.Picker, isWarm bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var snapshot conns.Set
	if fake, ok := picker.(*FakePicker); ok {
		snapshot = fake.Conns
	}
	p.picker = PickerState{Picker: picker, IsWarm: isWarm, SnapshotConns: snapshot}
	select {
	case p.pickerUpdate <- struct{}{}:
	default:
	}
}

// ResolveNow implements the balancer.ConnPool interface. It increments a
// counter that can be introspected.
func (p *FakeConnPool) ResolveNow() {
	p.mu.Lock()
	p.resolveNowCount++
	p.mu.Unlock()

	select {
	case p.resolveNowCh <- struct{}{}:
	default:
	}
}

// SnapshotConns returns a snapshot of the current active connections. This will
// include all connections created via NewConn but not yet removed via RemoveConn.
func (p *FakeConnPool) SnapshotConns() conns.Set {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := make(conns.Set, len(p.active))
	for k, v := range p.active {
		snapshot[k] = v
	}
	return snapshot
}

// AwaitPickerUpdate waits for a concurrent call to UpdatePicker. It may return
// immediately if there was a past call to UpdatePicker that has yet to be
// acknowledged via a call to this method. If the pool's current picker is a
// *FakePicker, then the returned state also includes a snapshot of the connections
// used to create the picker. If the pickers is not a *FakePicker, the returned
// state's SnapshotConns field will be nil. An error is returned if the given
// context is cancelled or times out before the picker is updated.
func (p *FakeConnPool) AwaitPickerUpdate(ctx context.Context) (PickerState, error) {
	select {
	case <-ctx.Done():
		return PickerState{}, ctx.Err()
	case <-p.pickerUpdate:
		p.mu.Lock()
		state := p.picker
		p.mu.Unlock()
		return state, nil
	}
}

// AwaitConnUpdate waits for concurrent changes to the set of active connections,
// via calls to NewConn and RemoveConn. It may return immediately if there was a
// past call to NewConn or RemoveConn that has yet to be acknowledged via a call
// to this method. It returns a snapshot of the connections on success. If returns
// an error if the given context is cancelled or times out before the connections
// are updated.
func (p *FakeConnPool) AwaitConnUpdate(ctx context.Context) (conns.Set, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.connsUpdate:
		return p.SnapshotConns(), nil
	}
}

// AwaitResolveNow waits for a concurrent call to ResolveNow, usually called by
// the balancer when the number of healthy connections is running below the
// threshold. If the context is cancelled before the ResolveNow method is
// called, this function returns the context error. If it succeeds, it will
// return the number of times ResolveNow has been called.
func (p *FakeConnPool) AwaitResolveNow(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.resolveNowCh:
		p.mu.Lock()
		count := p.resolveNowCount
		p.mu.Unlock()
		return count, nil
	}
}

// PickerState represents the attributes of a picker. It encapsulates the
// picker itself, whether the balancer indicated that the pool is warm when
// the picker was configured, and also a snapshot of the connections used
// to create the picker (only if the picker is a *FakePicker).
type PickerState struct {
	Picker        picker.Picker
	IsWarm        bool
	SnapshotConns conns.Set
}

// FakePicker is a picker implementation that can be used for testing. It always
// returns the first picker it encounters in its set. Its set is exported so test
// code can examine the set of connections used to create the picker.
//
// Also see NewFakePicker.
type FakePicker struct {
	Conns conns.Set
}

// NewFakePicker constructs a new FakePicker with the given connections.
//
// The return type is picker.Picker so this function can be directly used as
// a "new picker" function with a balancer. But this function *always*
// returns values whose type is *FakePicker.
func NewFakePicker(prev picker.Picker, connections conn.Conns) picker.Picker {
	_ = prev // we need prev so function matches "factory" function signature
	return &FakePicker{Conns: conns.ToSet(connections)}
}

// Pick implements the picker.Picker interface.
func (p *FakePicker) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	for c := range p.Conns {
		return c, nil, nil
	}
	return nil, nil, errors.New("zero conns")
}

// ConnHealth is map that tracks the health state of connections.
type ConnHealth map[conn.Conn]health.State

func (ch ConnHealth) AsSet() conns.Set {
	set := make(conns.Set, len(ch))
	for k := range ch {
		set[k] = struct{}{}
	}
	return set
}

// FakeHealthChecker is an implementation of healthchecker.Checker that can be
// used for testing balancer.Balancer implementations. It tracks the connections
// for which check processes are active (created with New but not closed). By
// default, all connections will be immediately healthy, but SetInitialState
// can be used to change that.
//
// See NewFakeHealthChecker.
type FakeHealthChecker struct {
	checkersUpdated chan struct{}
	mu              sync.Mutex
	// +checklocks:mu
	initialState health.State
	// +checklocks:mu
	trackers map[conn.Conn]health.Tracker
	// +checklocks:mu
	initialized map[conn.Conn]chan struct{}
	// +checklocks:mu
	conns ConnHealth
}

// NewFakeHealthChecker creates a new FakeHealthChecker.
func NewFakeHealthChecker() *FakeHealthChecker {
	return &FakeHealthChecker{
		checkersUpdated: make(chan struct{}, 1),
		initialState:    health.StateHealthy,
		trackers:        map[conn.Conn]health.Tracker{},
		initialized:     map[conn.Conn]chan struct{}{},
		conns:           ConnHealth{},
	}
}

// New implements the healthchecker.Checker interface. It will use the
// given tracker to mark the given connection with the currently configured
// initial health state (which defaults to health).
func (hc *FakeHealthChecker) New(_ context.Context, connection conn.Conn, tracker health.Tracker) io.Closer {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	state := hc.initialState
	hc.conns[connection] = state
	hc.trackers[connection] = tracker
	hc.initialized[connection] = make(chan struct{})
	go func() {
		tracker.UpdateHealthState(connection, state)
		hc.mu.Lock()
		defer hc.mu.Unlock()
		if ch := hc.initialized[connection]; ch != nil {
			close(ch)
		}
	}()
	select {
	case hc.checkersUpdated <- struct{}{}:
	default:
	}
	return closerFunc(func() error {
		hc.mu.Lock()
		delete(hc.conns, connection)
		delete(hc.trackers, connection)
		delete(hc.initialized, connection)
		hc.mu.Unlock()
		select {
		case hc.checkersUpdated <- struct{}{}:
		default:
		}
		return nil
	})
}

// UpdateHealthState allows the state of a connection to be changed.
func (hc *FakeHealthChecker) UpdateHealthState(connection conn.Conn, state health.State) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.trackers[connection].UpdateHealthState(connection, state)
	hc.conns[connection] = state
}

// SetInitialState sets the state that new connections will be put into
// in subsequent calls to New.
func (hc *FakeHealthChecker) SetInitialState(state health.State) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.initialState = state
}

// SnapshotConns returns a snapshot of active connections and their latest health state.
func (hc *FakeHealthChecker) SnapshotConns() ConnHealth {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	snapshot := ConnHealth{}
	for k, v := range hc.conns {
		snapshot[k] = v
	}
	return snapshot
}

// AwaitCheckerUpdate waits for the set of checked connections to change. This will
// return after a call to New or after a process is closed. It may return immediately
// if there was a past call to New or to a process's Close that has yet to be
// acknowledged via a call to this method. It returns a snapshot of the connections
// and their latest health state on success. It returns an error if the given context
// is cancelled or times out before any connection checks are created or closed.
func (hc *FakeHealthChecker) AwaitCheckerUpdate(ctx context.Context) (ConnHealth, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-hc.checkersUpdated:
		return hc.SnapshotConns(), nil
	}
}

// AwaitConnectionInitialized waits for given connection's initial health state to be
// set. It will return immediately if the given connection is not known to this checker
// (which can happen if the connection's health check process has already been closed).
// It returns a snapshot of the connection's state on success. It returns an error if
// the given context is cancelled or times out before the connection's state is
// initialized.
func (hc *FakeHealthChecker) AwaitConnectionInitialized(ctx context.Context, connection conn.Conn) (health.State, error) {
	hc.mu.Lock()
	initializedChan := hc.initialized[connection]
	hc.mu.Unlock()
	if initializedChan == nil {
		return health.StateUnhealthy, nil
	}
	select {
	case <-ctx.Done():
		return health.StateUnknown, ctx.Err()
	case <-initializedChan:
		hc.mu.Lock()
		state := hc.conns[connection]
		hc.mu.Unlock()
		return state, nil
	}
}

// FindConn finds the connection with the given address and index in the given conn.Set.
// returns nil if no such connection can be found.
func FindConn(set conns.Set, addr resolver.Address, index int) conn.Conn {
	for connection := range set {
		fakeConn, ok := connection.(*FakeConn)
		if !ok {
			continue
		}
		if reflect.DeepEqual(fakeConn.Address(), addr) && fakeConn.Index == index {
			return connection
		}
	}
	return nil
}

// DeterministicReconciler sorts the given slices, which are used by a balancer when
// reconciling connections. Sorting them guarantees that reconciliation activity is
// deterministic, making test assertions simpler.
func DeterministicReconciler(newAddrs []resolver.Address, removeConns []conn.Conn) {
	// sort new addresses, so they get created in deterministic order according to address test
	sort.Slice(newAddrs, func(i, j int) bool {
		return newAddrs[i].HostPort < newAddrs[j].HostPort
	})
	// also sort remove conns
	sort.Slice(removeConns, func(i, j int) bool { //nolint:varnamelen // i and j are fine
		if removeConns[i].Address().HostPort == removeConns[j].Address().HostPort {
			iconn, iok := removeConns[i].(*FakeConn)
			jconn, jok := removeConns[j].(*FakeConn)
			if iok && jok {
				return iconn.Index < jconn.Index
			}
		}
		return removeConns[i].Address().HostPort < removeConns[j].Address().HostPort
	})
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}
