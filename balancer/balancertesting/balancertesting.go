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

package balancertesting

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/connmanager"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/bufbuild/go-http-balancer/resolver"
)

//nolint:gochecknoglobals
var (
	// FakePickerFactory returns instances of *FakePicker.
	FakePickerFactory picker.Factory = fakePickerFactory{}
)

// FakeConn is an implementation of conn.Conn that can be used for testing.
// It is not usable for actual request traffic: attempts to call its
// RoundTrip method will always result in error.
//
// To create new instances of FakeConn, use a FakeConnPool.
type FakeConn struct {
	Index int

	addr atomic.Pointer[resolver.Address]
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
func (c *FakeConn) UpdateAttributes(attributes attrs.Attributes) {
	addr := c.Address()
	addr.Attributes = attributes
	c.addr.Store(&addr)
}

// RoundTrip implements the conn.Conn interface. For a FakeConn, it
// always returns an error as a FakeConn is not meant for real requests.
func (c *FakeConn) RoundTrip(*http.Request, func()) (*http.Response, error) {
	return nil, errors.New("FakeConn does not support RoundTrip")
}

// FakeConnPool is an implementation of balancer.ConnPool that can be used
// for testing balancer.Balancer implementations. It marks the connections
// created with its NewConn method with an index in sequential order. So the
// first connection created is a *FakeConn with an Index of 1. The second
// will have Index 2, and so on.
//
// See NewFakeConnPool.
type FakeConnPool struct {
	pickerUpdate chan struct{}
	connsUpdate  chan struct{}

	mu sync.Mutex
	// +checklocks:mu
	index int
	// +checklocks:mu
	active conn.Set
	// +checklocks:mu
	picker PickerState
}

// NewFakeConnPool constructs a new FakeConnPool.
func NewFakeConnPool() *FakeConnPool {
	return &FakeConnPool{
		pickerUpdate: make(chan struct{}, 1),
		connsUpdate:  make(chan struct{}, 1),
	}
}

// NewConn implements the balancer.ConnPool interface. It always returns
// *FakeConn instances. Test code can asynchronously await a call to NewConn
// or RemoveConn using the AwaitConnUpdate method.
func (p *FakeConnPool) NewConn(address resolver.Address) (conn.Conn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active == nil {
		p.active = conn.Set{}
	}
	p.index++
	newConn := &FakeConn{Index: p.index}
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

// UpdatePicker implements the balancer.ConnPool interface. Test code can
// asynchronously await a call to UpdatePicker using the AwaitPickerUpdate method.
func (p *FakeConnPool) UpdatePicker(picker picker.Picker, isWarm bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var snapshot conn.Set
	if fake, ok := picker.(*FakePicker); ok {
		snapshot = fake.Conns
	}
	p.picker = PickerState{Picker: picker, IsWarm: isWarm, SnapshotConns: snapshot}
	select {
	case p.pickerUpdate <- struct{}{}:
	default:
	}
}

// SnapshotConns returns a snapshot of the current active connections. This will
// include all connections created via NewConn but not yet removed via RemoveConn.
func (p *FakeConnPool) SnapshotConns() conn.Set {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := make(conn.Set, len(p.active))
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
func (p *FakeConnPool) AwaitConnUpdate(ctx context.Context) (conn.Set, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.connsUpdate:
		return p.SnapshotConns(), nil
	}
}

// PickerState represents the attributes of a picker. It encapsulates the
// picker itself, whether the balancer indicated that the pool is warm when
// the picker was configured, and also a snapshot of the connections used
// to create the picker (only if the picker is a *FakePicker).
type PickerState struct {
	Picker        picker.Picker
	IsWarm        bool
	SnapshotConns conn.Set
}

// FakePicker is a picker implementation that can be used for testing. It always
// returns the first picker it encounters in its set. Its set is exported so test
// code can examine the set of connections used to create the picker.
//
// Also see FakePickerFactory.
type FakePicker struct {
	Conns conn.Set
}

// NewFakePicker constructs a new FakePicker with the given connections.
func NewFakePicker(conns conn.Connections) *FakePicker {
	return &FakePicker{Conns: conn.ConnectionsToSet(conns)}
}

// Pick implements the picker.Picker interface.
func (p *FakePicker) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	for c := range p.Conns {
		return c, nil, nil
	}
	return nil, nil, errors.New("zero conns")
}

type fakePickerFactory struct{}

func (f fakePickerFactory) New(_ picker.Picker, allConns conn.Connections) picker.Picker {
	return NewFakePicker(allConns)
}

// DeterministicConnManagerFactory attempts to make ConnManager's that will result in
// deterministic construction of connections. It delegates to the given factory, but
// intercepts call to the ConnUpdater to sort the set of addresses for which new
// connections should be created. (The default balancer implementation will call
// ConnPool.NewConn to create connections in the order they appear in the set of
// addresses provided by the ConnManager.) Combined with a FakeConnPool, which stamps
// an index onto each FakeConn it creates, this can be used to make certain kinds of
// assertions about connection management simpler and deterministic.
func DeterministicConnManagerFactory(factory connmanager.Factory) connmanager.Factory {
	return deterministicConnManagerFactory{factory}
}

type deterministicConnManagerFactory struct {
	connmanager.Factory
}

func (f deterministicConnManagerFactory) New(ctx context.Context, scheme, hostPort string, updateConns connmanager.ConnUpdater) connmanager.ConnManager {
	return f.Factory.New(ctx, scheme, hostPort, func(newAddrs []resolver.Address, removeConns []conn.Conn) (added []conn.Conn) {
		// sort new addresses, so they get created in deterministic order according to address test
		sort.Slice(newAddrs, func(i, j int) bool {
			return newAddrs[i].HostPort < newAddrs[j].HostPort
		})
		return updateConns(newAddrs, removeConns)
	})
}
