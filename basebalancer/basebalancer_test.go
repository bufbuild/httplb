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

package basebalancer_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bufbuild/httplb/balancer"
	. "github.com/bufbuild/httplb/basebalancer"
	"github.com/bufbuild/httplb/internal/balancertesting"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultBalancer_BasicConnManagement(t *testing.T) {
	t.Parallel()
	connMgrFactory, activeConnMgrs := balancertesting.DeterministicConnManagerFactory(NewConnManagerFactory())
	factory := NewFactory(
		WithConnManager(connMgrFactory),
		WithPicker(balancertesting.FakePickerFactory),
	)
	pool := balancertesting.NewFakeConnPool()
	bal := factory.New(context.Background(), "http", "foo.com", pool)
	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	bal.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})

	// redundant addrs should result in extra conns to those addrs
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
	}
	bal.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4, 5, 6})

	// reconcile number of conns to single address
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
	}
	bal.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4, 5})

	// make sure conns are removed when their addr goes away
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
	}
	bal.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3})

	// finally, add some new IPs, and expect some new conns
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	bal.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 7, 8})

	// Also make sure the set of all conns (not just ones that the picker sees) arrives at the right state
	awaitConns(t, pool, addrs, []int{1, 2, 7, 8})

	err := bal.Close()
	require.NoError(t, err)
	require.Zero(t, activeConnMgrs())
}

func TestDefaultBalancer_HealthChecking(t *testing.T) {
	t.Parallel()
	connMgrFactory, activeConnMgrs := balancertesting.DeterministicConnManagerFactory(NewConnManagerFactory())
	checker := balancertesting.NewFakeHealthChecker()
	var oracle balancertesting.SwappableUsabilityOracle
	factory := NewFactory(
		WithConnManager(connMgrFactory),
		WithPicker(balancertesting.FakePickerFactory),
		WithHealthChecks(checker, oracle.Do),
	)
	pool := balancertesting.NewFakeConnPool()
	bal := factory.New(context.Background(), "http", "foo.com", pool)

	oracle.Set(NewOracle(HealthyState)) // only consider healthy connections usable
	checker.SetInitialState(UnknownState)

	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	bal.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 3, 4})
	awaitPickerUpdate(t, pool, false, nil, nil)

	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	conn4 := balancertesting.FindConn(conns, addrs[3], 4)
	checker.UpdateHealthState(conn1, HealthyState)
	awaitPickerUpdate(t, pool, true, addrs[:1], []int{1})
	checker.UpdateHealthState(conn2, HealthyState)
	awaitPickerUpdate(t, pool, true, addrs[:2], []int{1, 2})

	// now consider anything other than unhealthy to be usable
	oracle.Set(func(conns balancer.Conns, state func(balancer.Conn) HealthState) []balancer.Conn {
		results := make([]balancer.Conn, 0, conns.Len())
		for i := 0; i < conns.Len(); i++ {
			c := conns.Get(i)
			if state(c) != UnhealthyState {
				results = append(results, c)
			}
		}
		return results
	})
	checker.UpdateHealthState(conn3, DegradedState)
	// conns 1 and 2 healthy, 3 is degraded, 4 is still unknown, so all will be usable
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})
	checker.UpdateHealthState(conn1, UnhealthyState)
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 3, 4})
	// now consider healthy and unknown, but nothing else
	oracle.Set(func(conns balancer.Conns, state func(balancer.Conn) HealthState) []balancer.Conn {
		results := make([]balancer.Conn, 0, conns.Len())
		for i := 0; i < conns.Len(); i++ {
			c := conns.Get(i)
			if s := state(c); s == HealthyState || s == UnknownState {
				results = append(results, c)
			}
		}
		return results
	})
	checker.UpdateHealthState(conn3, HealthyState)
	checker.UpdateHealthState(conn4, DegradedState)
	// conn 1 is unhealthy, 2 and 3 are healthy, 4 is degraded
	awaitPickerUpdate(t, pool, true, addrs[1:3], []int{2, 3})

	// let's add some new connections and delete some
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	bal.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 5, 6})
	// 1 is unhealthy, 2 is healthy, the two new ones are unknown
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 5, 6})

	// only consider healthy connections
	oracle.Set(NewOracle(HealthyState))
	checker.SetInitialState(HealthyState)
	checker.UpdateHealthState(conn1, HealthyState)
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
		{HostPort: "1.2.3.20"},
	}
	bal.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 5, 6, 7})
	awaitConns(t, pool, addrs, []int{1, 2, 5, 6, 7})
	// First two are healthy as is last one; other two are still unknown
	expectAddrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.20"},
	}
	awaitPickerUpdate(t, pool, true, expectAddrs, []int{1, 2, 7})

	err := bal.Close()
	require.NoError(t, err)
	checkers := checker.SnapshotConns()
	require.Empty(t, checkers)
	require.Zero(t, activeConnMgrs())
}

func TestDefaultBalancer_WarmDefinition(t *testing.T) {
	t.Parallel()
	connMgrFactory, activeConnMgrs := balancertesting.DeterministicConnManagerFactory(NewConnManagerFactory())
	checker := balancertesting.NewFakeHealthChecker()
	factory := NewFactory(
		WithConnManager(connMgrFactory),
		WithPicker(balancertesting.FakePickerFactory),
		WithHealthChecks(checker, NewOracle(UnknownState)),
		// must have at least 3 or 20% of connections (whichever is greater) warmed and healthy
		WithWarmDefinition(3, 50, HealthyState),
	)
	pool := balancertesting.NewFakeConnPool()
	// map of host:port -> chan struct{} which is closed when that address is warm
	var warmChans sync.Map
	pool.Prewarm = func(c balancer.Conn, ctx context.Context) error {
		ch, ok := warmChans.Load(c.Address().HostPort)
		if !ok {
			return nil
		}
		select {
		case <-ch.(chan struct{}):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	balancer := factory.New(context.Background(), "http", "foo.com", pool)

	checker.SetInitialState(UnknownState)

	warmChan1 := make(chan struct{})
	warmChan2 := make(chan struct{})
	warmChan3 := make(chan struct{})
	warmChan4 := make(chan struct{})
	warmChans.Store("1.2.3.1", warmChan1)
	warmChans.Store("1.2.3.2", warmChan2)
	warmChans.Store("1.2.3.3", warmChan3)
	warmChans.Store("1.2.3.4", warmChan4)
	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 3, 4})
	// all are included, but still not warmed up
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})

	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	checker.UpdateHealthState(conn1, HealthyState)
	checker.UpdateHealthState(conn2, HealthyState)
	checker.UpdateHealthState(conn3, HealthyState)
	// generous time for concurrent activities to complete after health states updated
	time.Sleep(300 * time.Millisecond)
	// should still be unhealthy
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})

	// We need 3 warmed up, so 2 still not enough
	close(warmChan1)
	close(warmChan2)
	// generous time for concurrent activities to complete after health states updated
	time.Sleep(300 * time.Millisecond)
	// should still be unhealthy
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})

	// But third one's the charm
	close(warmChan3)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})

	// Now we'll try the other direction: connections become warm immediately, so we
	// then are waiting for health checks to complete. This time, the minimum is
	// 4 connections per the percent minimum (now there 8; 50% of 8 is 4).
	addrs = []resolver.Address{
		{HostPort: "1.2.3.05"}, // extra zero in last component so they sort lexically
		{HostPort: "1.2.3.06"}, // and get created in exactly this order
		{HostPort: "1.2.3.07"},
		{HostPort: "1.2.3.08"},
		{HostPort: "1.2.3.09"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
		{HostPort: "1.2.3.12"},
	}
	balancer.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{5, 6, 7, 8, 9, 10, 11, 12})
	// We didn't put channels in warmChans map, so they become warm immediately.
	// But we now have 0/4 connections healthy.
	awaitPickerUpdate(t, pool, false, addrs, []int{5, 6, 7, 8, 9, 10, 11, 12})

	conns = pool.SnapshotConns()
	conn5 := balancertesting.FindConn(conns, addrs[0], 5)
	conn6 := balancertesting.FindConn(conns, addrs[1], 6)
	conn7 := balancertesting.FindConn(conns, addrs[2], 7)
	conn8 := balancertesting.FindConn(conns, addrs[3], 8)
	checker.UpdateHealthState(conn5, HealthyState)
	checker.UpdateHealthState(conn6, HealthyState)
	checker.UpdateHealthState(conn7, HealthyState)
	// generous time for concurrent activities to complete after health states updated
	time.Sleep(300 * time.Millisecond)
	// should still be unhealthy
	awaitPickerUpdate(t, pool, false, addrs, []int{5, 6, 7, 8, 9, 10, 11, 12})

	// When the third one turns healthy, pool is warm again
	checker.UpdateHealthState(conn8, HealthyState)
	awaitPickerUpdate(t, pool, true, addrs, []int{5, 6, 7, 8, 9, 10, 11, 12})

	err := balancer.Close()
	require.NoError(t, err)
	checkers := checker.SnapshotConns()
	require.Empty(t, checkers)
	require.Zero(t, activeConnMgrs())
}

func awaitPickerUpdate(t *testing.T, pool *balancertesting.FakeConnPool, warm bool, addrs []resolver.Address, indexes []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var lastState *balancertesting.PickerState
	for {
		state, err := pool.AwaitPickerUpdate(ctx)
		if err != nil {
			require.NotNil(t, lastState, "didn't get picker update after 1 second")
			want := connStatesFromAddrsIndexes(addrs, indexes)
			got := connStatesFromSnapshot(lastState.SnapshotConns)
			require.FailNow(t, "didn't get expected picker update after 1 second", "want %+v\ngot %+v", want, got)
		}
		if checkState(state, warm, addrs, indexes) {
			return // success!
		}
		lastState = &state
	}
}

func awaitConns(t *testing.T, pool *balancertesting.FakeConnPool, addrs []resolver.Address, indexes []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := connStatesFromAddrsIndexes(addrs, indexes)
	var got map[connState]resolver.Attrs
	for {
		snapshot, err := pool.AwaitConnUpdate(ctx)
		if err != nil {
			require.NotNil(t, got, "didn't get connection update after 1 second")
			require.FailNow(t, "didn't get expected active connections after 1 second", "want %+v\ngot %+v", want, got)
		}
		got = connStatesFromSnapshot(snapshot)
		if reflect.DeepEqual(want, got) {
			return
		}
	}
}

func awaitCheckerUpdate(t *testing.T, checker *balancertesting.FakeHealthChecker, addrs []resolver.Address, indexes []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := connStatesFromAddrsIndexes(addrs, indexes)
	var got map[connState]resolver.Attrs
	for {
		snapshot, err := checker.AwaitCheckerUpdate(ctx)
		if err != nil {
			require.NotNil(t, got, "didn't get checker update after 1 second")
			require.FailNow(t, "didn't get expected active connections after 1 second", "want %+v\ngot %+v", want, got)
		}
		got = connStatesFromHealthSnapshot(snapshot)
		if reflect.DeepEqual(want, got) {
			for c := range snapshot {
				_, err = checker.AwaitConnectionInitialized(ctx, c)
				require.NoError(t, err, "health state for %s not initialized after 1 second", c.Address().HostPort)
			}
			return
		}
	}
}

func checkState(state balancertesting.PickerState, warm bool, addrs []resolver.Address, indexes []int) bool {
	if state.IsWarm != warm {
		return false
	}
	want := connStatesFromAddrsIndexes(addrs, indexes)
	got := connStatesFromSnapshot(state.SnapshotConns)
	return reflect.DeepEqual(want, got)
}

type connState struct {
	hostPort string
	index    int
}

func connStatesFromAddrsIndexes(addrs []resolver.Address, indexes []int) map[connState]resolver.Attrs {
	want := map[connState]resolver.Attrs{}
	for i := range addrs {
		want[connState{
			hostPort: addrs[i].HostPort,
			index:    indexes[i],
		}] = addrs[i].Attributes
	}
	return want
}

func connStatesFromSnapshot(snapshot balancer.ConnSet) map[connState]resolver.Attrs {
	got := map[connState]resolver.Attrs{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}

func connStatesFromHealthSnapshot(snapshot balancertesting.ConnHealth) map[connState]resolver.Attrs {
	got := map[connState]resolver.Attrs{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}
