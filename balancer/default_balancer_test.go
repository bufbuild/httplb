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
	"reflect"
	"testing"
	"time"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/balancer/balancertesting"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/connmanager"
	"github.com/bufbuild/go-http-balancer/balancer/healthchecker"
	"github.com/bufbuild/go-http-balancer/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultBalancer_BasicConnManagement(t *testing.T) {
	t.Parallel()
	connMgrFactory, activeConnMgrs := balancertesting.DeterministicConnManagerFactory(connmanager.NewFactory())
	factory := NewFactory(
		WithConnManager(connMgrFactory),
		WithPicker(balancertesting.FakePickerFactory),
	)
	pool := balancertesting.NewFakeConnPool()
	balancer := factory.New(context.Background(), "http", "foo.com", pool)
	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
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
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4, 5, 6})

	// reconcile number of conns to single address
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4, 5})

	// make sure conns are removed when their addr goes away
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3})

	// finally, add some new IPs, and expect some new conns
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 7, 8})

	// Also make sure the set of all conns (not just ones that the picker sees) arrives at the right state
	awaitConns(t, pool, addrs, []int{1, 2, 7, 8})

	err := balancer.Close()
	require.NoError(t, err)
	require.Zero(t, activeConnMgrs())
}

func TestDefaultBalancer_HealthChecking(t *testing.T) {
	t.Parallel()
	connMgrFactory, activeConnMgrs := balancertesting.DeterministicConnManagerFactory(connmanager.NewFactory())
	checker := balancertesting.NewFakeHealthChecker()
	var oracle balancertesting.SwappableUsabilityOracle
	factory := NewFactory(
		WithConnManager(connMgrFactory),
		WithPicker(balancertesting.FakePickerFactory),
		WithHealthChecks(checker, oracle.Do),
	)
	pool := balancertesting.NewFakeConnPool()
	balancer := factory.New(context.Background(), "http", "foo.com", pool)

	oracle.Set(healthchecker.NewOracle(healthchecker.Healthy)) // only consider healthy connections usable
	checker.SetInitialState(healthchecker.Unknown)

	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 3, 4})
	awaitPickerUpdate(t, pool, false, nil, nil)

	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	conn4 := balancertesting.FindConn(conns, addrs[3], 4)
	checker.UpdateHealthState(conn1, healthchecker.Healthy)
	awaitPickerUpdate(t, pool, true, addrs[:1], []int{1})
	checker.UpdateHealthState(conn2, healthchecker.Healthy)
	awaitPickerUpdate(t, pool, true, addrs[:2], []int{1, 2})

	// now consider anything other than unhealthy to be usable
	oracle.Set(func(conns conn.Connections, state func(conn.Conn) healthchecker.HealthState) []conn.Conn {
		results := make([]conn.Conn, 0, conns.Len())
		for i := 0; i < conns.Len(); i++ {
			c := conns.Get(i)
			if state(c) != healthchecker.Unhealthy {
				results = append(results, c)
			}
		}
		return results
	})
	checker.UpdateHealthState(conn3, healthchecker.Degraded)
	// conns 1 and 2 healthy, 3 is degraded, 4 is still unknown, so all will be usable
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})
	checker.UpdateHealthState(conn1, healthchecker.Unhealthy)
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 3, 4})
	// now consider healthy and unknown, but nothing else
	oracle.Set(func(conns conn.Connections, state func(conn.Conn) healthchecker.HealthState) []conn.Conn {
		results := make([]conn.Conn, 0, conns.Len())
		for i := 0; i < conns.Len(); i++ {
			c := conns.Get(i)
			if s := state(c); s == healthchecker.Healthy || s == healthchecker.Unknown {
				results = append(results, c)
			}
		}
		return results
	})
	checker.UpdateHealthState(conn3, healthchecker.Healthy)
	checker.UpdateHealthState(conn4, healthchecker.Degraded)
	// conn 1 is unhealthy, 2 and 3 are healthy, 4 is degraded
	awaitPickerUpdate(t, pool, true, addrs[1:3], []int{2, 3})

	// let's add some new connections and delete some
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	balancer.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 5, 6})
	// 1 is unhealthy, 2 is healthy, the two new ones are unknown
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 5, 6})

	// only consider healthy connections
	oracle.Set(healthchecker.NewOracle(healthchecker.Healthy))
	checker.SetInitialState(healthchecker.Healthy)
	checker.UpdateHealthState(conn1, healthchecker.Healthy)
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
		{HostPort: "1.2.3.20"},
	}
	balancer.OnResolve(addrs)
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 5, 6, 7})
	awaitConns(t, pool, addrs, []int{1, 2, 5, 6, 7})
	// First two are healthy as is last one; other two are still unknown
	expectAddrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.20"},
	}
	awaitPickerUpdate(t, pool, true, expectAddrs, []int{1, 2, 7})

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
	var got map[connState]attrs.Attributes
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
	var got map[connState]attrs.Attributes
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

func connStatesFromSnapshot(snapshot conn.Set) map[connState]attrs.Attributes {
	got := map[connState]attrs.Attributes{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}

func connStatesFromHealthSnapshot(snapshot balancertesting.ConnHealth) map[connState]attrs.Attributes {
	got := map[connState]attrs.Attributes{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}
