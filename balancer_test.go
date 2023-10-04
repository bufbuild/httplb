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
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bufbuild/httplb/attribute"
	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal/balancertesting"
	"github.com/bufbuild/httplb/internal/clocktest"
	"github.com/bufbuild/httplb/internal/conns"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnManager(t *testing.T) {
	t.Parallel()
	type updateReq struct {
		newAddrs    []resolver.Address
		removeConns []conn.Conn
	}
	updates := make(chan updateReq, 1)
	pool := balancertesting.NewFakeConnPool()
	createConns := func(newAddrs []resolver.Address) []conn.Conn {
		conns := make([]conn.Conn, len(newAddrs))
		for i := range newAddrs {
			var ok bool
			conns[i], ok = pool.NewConn(newAddrs[i])
			require.True(t, ok)
		}
		return conns
	}
	testUpdate := func(newAddrs []resolver.Address, removeConns []conn.Conn) (added []conn.Conn) {
		// deterministic reconciliation FTW!
		balancertesting.DeterministicReconciler(newAddrs, removeConns)

		select {
		case updates <- updateReq{newAddrs: newAddrs, removeConns: removeConns}:
		default:
			require.FailNow(t, "channel should not be full")
		}
		for _, c := range removeConns {
			require.True(t, pool.RemoveConn(c))
		}
		return createConns(newAddrs)
	}
	getLatestUpdate := func() updateReq {
		select {
		case latest := <-updates:
			return latest
		default:
			require.FailNow(t, "now update available")
			return updateReq{}
		}
	}
	var connMgr connManager
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.5"},
		{HostPort: "1.2.3.6"},
	}
	connMgr.reconcileAddresses(addrs, testUpdate)
	latestUpdate := getLatestUpdate()
	require.Equal(t, addrs, latestUpdate.newAddrs)
	require.Empty(t, latestUpdate.removeConns)
	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	conn4 := balancertesting.FindConn(conns, addrs[3], 4)
	conn5 := balancertesting.FindConn(conns, addrs[4], 5)
	conn6 := balancertesting.FindConn(conns, addrs[5], 6)
	require.Equal(t, len(addrs), len(conns))

	// let's try a different set that includes duplicates
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.3"},
	}
	connMgr.reconcileAddresses(addrs, testUpdate)
	latestUpdate = getLatestUpdate()
	// 10 entries needed, and we start with 3. So we need
	// 2x more of each, but 3x of the first
	newAddrs := []resolver.Address{
		{HostPort: "1.2.3.1"}, // index=7
		{HostPort: "1.2.3.1"}, //       8
		{HostPort: "1.2.3.1"}, //       9
		{HostPort: "1.2.3.2"}, //      10
		{HostPort: "1.2.3.2"}, //      11
		{HostPort: "1.2.3.3"}, //      12
		{HostPort: "1.2.3.3"}, //      13
	}
	require.Equal(t, newAddrs, latestUpdate.newAddrs)
	require.Equal(t, []conn.Conn{conn4, conn5, conn6}, latestUpdate.removeConns)
	conns = pool.SnapshotConns()
	require.Equal(t, 10, len(conns))
	conn1i7 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.1"}, 7)
	conn1i8 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.1"}, 8)
	conn1i9 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.1"}, 9)
	conn2i10 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.2"}, 10)
	conn2i11 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.2"}, 11)
	conn3i12 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.3"}, 12)
	conn3i13 := balancertesting.FindConn(conns, resolver.Address{HostPort: "1.2.3.3"}, 13)

	// Still multiple addresses, but different counts, to make sure the conn manager
	// correctly reconciles.
	attrKey := attribute.NewKey[int]()
	attrs1a := attribute.NewValues(attrKey.Value(101))
	attrs1b := attribute.NewValues(attrKey.Value(102))
	attrs2a := attribute.NewValues(attrKey.Value(201))
	attrs2b := attribute.NewValues(attrKey.Value(202))
	attrs3a := attribute.NewValues(attrKey.Value(301))
	attrs3b := attribute.NewValues(attrKey.Value(302))
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1", Attributes: attrs1a},
		{HostPort: "1.2.3.1", Attributes: attrs1b},
		{HostPort: "1.2.3.2", Attributes: attrs2a},
		{HostPort: "1.2.3.2", Attributes: attrs2b},
		{HostPort: "1.2.3.3", Attributes: attrs3a},
		{HostPort: "1.2.3.3", Attributes: attrs3b},
	}
	connMgr.reconcileAddresses(addrs, testUpdate)
	latestUpdate = getLatestUpdate()
	require.Empty(t, latestUpdate.newAddrs)
	require.Equal(t, []conn.Conn{conn1i8, conn1i9, conn2i11, conn3i13}, latestUpdate.removeConns)
	conns = pool.SnapshotConns()
	require.Equal(t, 6, len(conns))
	// make sure attributes on existing connections were updated to latest
	// values from resolver
	require.Equal(t, attrs1a, conn1.Address().Attributes)
	require.Equal(t, attrs1b, conn1i7.Address().Attributes)
	require.Equal(t, attrs2a, conn2.Address().Attributes)
	require.Equal(t, attrs2b, conn2i10.Address().Attributes)
	require.Equal(t, attrs3a, conn3.Address().Attributes)
	require.Equal(t, attrs3b, conn3i12.Address().Attributes)

	// Last change. Reconciler will return four addresses,
	// but creation of new conns only returns 2.
	createConns = func(newAddrs []resolver.Address) []conn.Conn {
		conns := make([]conn.Conn, 0, len(newAddrs))
		for i := range newAddrs {
			c, ok := pool.NewConn(newAddrs[i])
			require.True(t, ok)
			conns = append(conns, c)
			if len(conns) == 2 {
				break
			}
		}
		return conns
	}
	addrs = []resolver.Address{
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.6"},
		{HostPort: "1.2.3.8"},
	}
	connMgr.reconcileAddresses(addrs, testUpdate)
	// Wanted to create 1.2.3.4, 1.2.3.6, and 1.2.3.8, but only first two created.
	latestUpdate = getLatestUpdate()
	require.Equal(t, addrs[1:], latestUpdate.newAddrs)
	require.Equal(t, []conn.Conn{conn1, conn1i7, conn2i10, conn3, conn3i12}, latestUpdate.removeConns)

	// Repeat with the same addresses. This will try again to create 1.2.3.8.
	addrs = []resolver.Address{
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.6"},
		{HostPort: "1.2.3.8"},
	}
	connMgr.reconcileAddresses(addrs, testUpdate)
	latestUpdate = getLatestUpdate()
	require.Equal(t, addrs[3:], latestUpdate.newAddrs)
	require.Empty(t, latestUpdate.removeConns)
}

func TestBalancer_BasicConnManagement(t *testing.T) {
	t.Parallel()
	pool := balancertesting.NewFakeConnPool()
	balancer := newBalancer(context.Background(), balancertesting.NewFakePicker, health.NoOpChecker, pool)
	balancer.updateHook = balancertesting.DeterministicReconciler
	balancer.start()
	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})

	// Redundant addrs should result in extra conns to those addrs
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

	// Reconcile number of conns to single address
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4, 5})

	// Make sure conns are removed when their addr goes away
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3})

	// Finally, add some new IPs, and expect some new conns
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
}

func TestBalancer_HealthChecking(t *testing.T) {
	t.Parallel()
	checker := balancertesting.NewFakeHealthChecker()
	pool := balancertesting.NewFakeConnPool()
	var warmChans sync.Map
	pool.Prewarm = func(c conn.Conn, ctx context.Context) error {
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
	balancer := newBalancer(context.Background(), balancertesting.NewFakePicker, checker, pool)
	balancer.updateHook = balancertesting.DeterministicReconciler
	balancer.start()

	checker.SetInitialState(health.StateUnknown)
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
	// Everything in unknown state is considered usable, but we're not yet warmed up
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})
	close(warmChan1)
	// One connection is warm, but not yet healthy, so the pool still isn't yet warmed up
	time.Sleep(100 * time.Millisecond) // delay is to make sure concurrent updates can complete
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})

	// We can go ahead and let the others become warm
	close(warmChan2)
	close(warmChan3)
	close(warmChan4)

	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	conn4 := balancertesting.FindConn(conns, addrs[3], 4)
	checker.UpdateHealthState(conn1, health.StateHealthy)
	// Now that conn1 is both warm and healthy, we are warmed up. We still include the
	// connections in unknown state because the threshold is 3 conns or 25%, whichever is greater.
	awaitPickerUpdate(t, pool, true, addrs, []int{1, 2, 3, 4})

	checker.UpdateHealthState(conn2, health.StateHealthy)
	checker.UpdateHealthState(conn3, health.StateHealthy)
	// Now we have three conns healthy, which meets the threshold where we can prefer only
	// healthy connections and disregard the one that is not yet healthy.
	awaitPickerUpdate(t, pool, true, addrs[:3], []int{1, 2, 3})

	checker.UpdateHealthState(conn1, health.StateDegraded)
	// Conn 1 is degraded, so now we prefer the other three.
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 3, 4})
	checker.UpdateHealthState(conn4, health.StateUnhealthy)
	// Conn 4 is now unhealthy, so we'll pull in conn 1 again so that we have three.
	awaitPickerUpdate(t, pool, true, addrs[:3], []int{1, 2, 3, 4})

	// Let's add some new connections and delete some.
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.10"},
		{HostPort: "1.2.3.11"},
	}
	balancer.OnResolve(addrs)
	// Conn 1 is degraded, conn 2 is healthy, and the others are unknown. So we
	// need to use all three states in order to meet the threshold.
	awaitCheckerUpdate(t, checker, addrs, []int{1, 2, 5, 6})
	// 1 is unhealthy, 2 is healthy, the two new ones are unknown
	awaitPickerUpdate(t, pool, true, addrs[1:], []int{2, 5, 6})

	// only consider healthy connections
	checker.SetInitialState(health.StateHealthy)
	checker.UpdateHealthState(conn1, health.StateHealthy)
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
}

func TestDefaultBalancer_Reresolve(t *testing.T) {
	t.Parallel()
	checker := balancertesting.NewFakeHealthChecker()
	clock := clocktest.NewFakeClock()
	pool := balancertesting.NewFakeConnPool()

	balancer := newBalancer(context.Background(), balancertesting.NewFakePicker, checker, pool)
	balancer.updateHook = balancertesting.DeterministicReconciler
	balancer.clock = clock
	balancer.start()

	checker.SetInitialState(health.StateUnknown)

	// Initial resolve
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
	}
	balancer.OnResolve(addrs)
	awaitPickerUpdate(t, pool, false, addrs, []int{1, 2, 3, 4})
	conns := pool.SnapshotConns()
	conn1 := balancertesting.FindConn(conns, addrs[0], 1)
	conn2 := balancertesting.FindConn(conns, addrs[1], 2)
	conn3 := balancertesting.FindConn(conns, addrs[2], 3)
	conn4 := balancertesting.FindConn(conns, addrs[3], 4)
	checker.UpdateHealthState(conn1, health.StateUnhealthy)
	checker.UpdateHealthState(conn2, health.StateUnhealthy)
	awaitResolveNow(t, pool, 1)
	checker.UpdateHealthState(conn3, health.StateUnhealthy)
	clock.Advance(10 * time.Second)
	checker.UpdateHealthState(conn4, health.StateUnhealthy)
	awaitResolveNow(t, pool, 2)

	err := balancer.Close()
	require.NoError(t, err)
	checkers := checker.SnapshotConns()
	require.Empty(t, checkers)
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
	var got map[connState]attribute.Values
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
	var got map[connState]attribute.Values
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

func awaitResolveNow(t *testing.T, pool *balancertesting.FakeConnPool, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := pool.AwaitResolveNow(ctx)
	require.NoError(t, err, "didn't re-resolve after 1 second")
	assert.Equal(t, count, got, "unexpected number of re-resolve requests")
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

func connStatesFromAddrsIndexes(addrs []resolver.Address, indexes []int) map[connState]attribute.Values {
	want := map[connState]attribute.Values{}
	for i := range addrs {
		want[connState{
			hostPort: addrs[i].HostPort,
			index:    indexes[i],
		}] = addrs[i].Attributes
	}
	return want
}

func connStatesFromSnapshot(snapshot conns.Set) map[connState]attribute.Values {
	got := map[connState]attribute.Values{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}

func connStatesFromHealthSnapshot(snapshot balancertesting.ConnHealth) map[connState]attribute.Values {
	got := map[connState]attribute.Values{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index,
		}] = c.Address().Attributes
	}
	return got
}
