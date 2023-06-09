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
	"github.com/bufbuild/go-http-balancer/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultBalancer_BasicConnManagement(t *testing.T) {
	t.Parallel()
	factory := NewFactory(
		WithConnManager(balancertesting.DeterministicConnManagerFactory(connmanager.NewFactory())),
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
}

//nolint:unparam // warm is always true now, but upcoming test cases will pass false
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

func connStatesFromSnapshot(snapshot map[conn.Conn]struct{}) map[connState]attrs.Attributes {
	got := map[connState]attrs.Attributes{}
	for c := range snapshot {
		got[connState{
			hostPort: c.Address().HostPort,
			index:    c.(*balancertesting.FakeConn).Index, //nolint:forcetypeassert
		}] = c.Address().Attributes
	}
	return got
}
