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
	"net/http"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/connmanager"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/bufbuild/go-http-balancer/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultBalancer_BasicConnManagement(t *testing.T) {
	factory := NewFactory(
		WithConnManager(deterministicConnManagerFactory{connmanager.NewFactory()}),
		WithPicker(&fakePickerFactory{}),
	)
	pool := &fakeConnPool{
		pickersUpdate: make(chan struct{}, 10),
	}
	balancer := factory.New(context.Background(), "http", "foo.com", pool)

	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	pool.awaitPickerUpdate(t, true, addrs, []int{1, 2, 3, 4})

	// redundant addrs should result in extra conns to those addrs
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	pool.awaitPickerUpdate(t, true, addrs, []int{1, 2, 3, 4, 5, 6})

	// make sure conns are removed when their addr goes away
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
	}
	balancer.OnResolve(addrs)
	pool.awaitPickerUpdate(t, true, addrs, []int{1, 2, 3})

	// finally, add some new IPs, and expect some new conns
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	balancer.OnResolve(addrs)
	pool.awaitPickerUpdate(t, true, addrs, []int{1, 2, 7, 8})

	// Also make sure the set of all conns (not just ones that the picker sees) arrives at the right state
	pool.awaitConns(t, addrs, []int{1, 2, 7, 8})
}

type fakeConnPool struct {
	mu            sync.Mutex
	index         int
	active        map[conn.Conn]struct{}
	pickers       []pickerState
	pickersUpdate chan struct{}
}

func (p *fakeConnPool) NewConn(address resolver.Address) (conn.Conn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active == nil {
		p.active = map[conn.Conn]struct{}{}
	}
	p.index++
	c := &fakeConn{index: p.index}
	c.addr.Store(&address)
	p.active[c] = struct{}{}
	return c, true
}

func (p *fakeConnPool) RemoveConn(c conn.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.active[c]; !ok {
		// Our balancer and conn manager impls should be well-behaved. So this should never happen.
		// So instead of returning false, let's freak out and make sure the test fails.
		panic("misbehaving balancer or conn manager")
	}
	delete(p.active, c)
	return true
}

func (p *fakeConnPool) UpdatePicker(picker picker.Picker, isWarm bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var snapshot map[conn.Conn]struct{}
	if fake, ok := picker.(*fakePicker); ok {
		snapshot = fake.conns
	}
	p.pickers = append(p.pickers, pickerState{picker: picker, isWarm: isWarm, activeSnapshot: snapshot})
	select {
	case p.pickersUpdate <- struct{}{}:
	default:
	}
}

func (p *fakeConnPool) snapshotActive() map[conn.Conn]struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := make(map[conn.Conn]struct{}, len(p.active))
	for k, v := range p.active {
		snapshot[k] = v
	}
	return snapshot
}

func (p *fakeConnPool) awaitPickerUpdate(t *testing.T, warm bool, addrs []resolver.Address, indexes []int) {
	timer := time.After(time.Second)
	var lastState *pickerState
	for {
		select {
		case <-p.pickersUpdate:
			p.mu.Lock()
			if len(p.pickers) == 0 {
				lastState = nil
			} else {
				lastState = &p.pickers[len(p.pickers)-1]
			}
			p.mu.Unlock()
			if lastState != nil && lastState.check(warm, addrs, indexes) {
				return
			}
		case <-timer:
			if lastState == nil {
				require.FailNow(t, "didn't get picker update after 1 second")
			}
			want := connStatesFromAddrsIndexes(addrs, indexes)
			got := connStatesFromSnapshot(lastState.activeSnapshot)
			require.FailNow(t, "didn't get expected picker update after 1 second", "want %+v\ngot %+v", want, got)
		}
	}
}

func (p *fakeConnPool) awaitConns(t *testing.T, addrs []resolver.Address, indexes []int) {
	deadline := time.Now().Add(time.Second)
	ticker := time.NewTimer(50 * time.Millisecond)
	immediate := make(chan struct{})
	close(immediate)
	defer ticker.Stop()
	want := connStatesFromAddrsIndexes(addrs, indexes)
	for {
		select {
		case <-ticker.C:
		case <-immediate:
		}
		p.mu.Lock()
		got := connStatesFromSnapshot(p.active)
		p.mu.Unlock()
		if reflect.DeepEqual(want, got) {
			return
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "didn't get expected picker update after 1 second", "want %+v\ngot %+v", want, got)
		}
		immediate = nil // immediate is only to trigger an immediate check, instead of waiting 50 millis for first check
	}
}

type pickerState struct {
	picker         picker.Picker
	isWarm         bool
	activeSnapshot map[conn.Conn]struct{}
}

func (s pickerState) check(warm bool, addrs []resolver.Address, indexes []int) bool {
	if s.isWarm != warm {
		return false
	}
	want := connStatesFromAddrsIndexes(addrs, indexes)
	got := connStatesFromSnapshot(s.activeSnapshot)
	return reflect.DeepEqual(want, got)
}

type connState struct {
	hostPort string
	index    int
}

func connStatesFromAddrsIndexes(addrs []resolver.Address, indexes []int) map[connState]attrs.Attributes {
	want := map[connState]attrs.Attributes{}
	for i := range addrs {
		want[connState{
			hostPort: addrs[i].HostPort,
			index:    indexes[i],
		}] = addrs[i].Attributes
	}
	return want
}

func connStatesFromSnapshot(snapshot map[conn.Conn]struct{}) map[connState]attrs.Attributes {
	got := map[connState]attrs.Attributes{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*fakeConn).index,
		}] = c.Address().Attributes
	}
	return got
}

type fakePickerFactory struct{}

func (f *fakePickerFactory) New(_ picker.Picker, allConns conn.Connections) picker.Picker {
	conns := map[conn.Conn]struct{}{}
	for i := 0; i < allConns.Len(); i++ {
		conns[allConns.Get(i)] = struct{}{}
	}
	return &fakePicker{conns: conns}
}

type fakePicker struct {
	conns map[conn.Conn]struct{}
}

func (p *fakePicker) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	for c := range p.conns {
		return c, nil, nil
	}
	return nil, nil, errors.New("zero conns")
}

type fakeConn struct {
	conn.Conn
	addr  atomic.Pointer[resolver.Address]
	index int
}

func (c *fakeConn) Address() resolver.Address {
	return *c.addr.Load()
}

func (c *fakeConn) UpdateAttributes(attributes attrs.Attributes) {
	addr := c.Address()
	addr.Attributes = attributes
	c.addr.Store(&addr)
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
