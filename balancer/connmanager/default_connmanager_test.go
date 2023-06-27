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

package connmanager_test

import (
	"context"
	"testing"

	"github.com/bufbuild/httplb/attrs"
	"github.com/bufbuild/httplb/balancer/balancertesting"
	"github.com/bufbuild/httplb/balancer/conn"
	. "github.com/bufbuild/httplb/balancer/connmanager"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultConnManager(t *testing.T) {
	t.Parallel()
	var subsetter balancertesting.SwappableSubsetter
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
	factory, _ := balancertesting.DeterministicConnManagerFactory(NewFactory(WithSubsetter(&subsetter)))
	connMgr := factory.New(context.Background(), "http", "foo.com", testUpdate)
	addrs := []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
		{HostPort: "1.2.3.4"},
		{HostPort: "1.2.3.5"},
		{HostPort: "1.2.3.6"},
	}
	connMgr.ReconcileAddresses(addrs)
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

	// Change subsetter to always returns 10 addresses; duplicates if necessary.
	subsetter.Set(func(allAddrs []resolver.Address) []resolver.Address {
		var subset []resolver.Address
		for {
			for _, addr := range allAddrs {
				subset = append(subset, addr)
				if len(subset) == 10 {
					return subset
				}
			}
		}
	})
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1"},
		{HostPort: "1.2.3.2"},
		{HostPort: "1.2.3.3"},
	}
	connMgr.ReconcileAddresses(addrs)
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

	// Now subsetter returns two of everything, up to 6 total.
	subsetter.Set(func(allAddrs []resolver.Address) []resolver.Address {
		var subset []resolver.Address
		for _, addr := range allAddrs {
			subset = append(subset, addr, addr)
			if len(subset) == 6 {
				break
			}
		}
		return subset
	})
	attrKey1 := attrs.NewKey[int]()
	attrKey2 := attrs.NewKey[string]()
	attrs1 := attrs.New(attrKey1.Value(101))
	attrs2 := attrs.New(attrKey1.Value(201))
	attrs3 := attrs.New(attrKey1.Value(301))
	attrs4 := attrs.New(attrKey1.Value(401), attrKey2.Value("abc"))
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1", Attributes: attrs1},
		{HostPort: "1.2.3.2", Attributes: attrs2},
		{HostPort: "1.2.3.3", Attributes: attrs3},
		{HostPort: "1.2.3.4", Attributes: attrs4},
	}
	// We have extras for 1.2.3.1, 1.2.3.2, and 1.2.3.3 to remove. We won't
	// create any for 1.2.3.4 because the subsetter will reach its limit of
	// 6 before it sees it.
	connMgr.ReconcileAddresses(addrs)
	latestUpdate = getLatestUpdate()
	require.Empty(t, latestUpdate.newAddrs)
	require.Equal(t, []conn.Conn{conn1i8, conn1i9, conn2i11, conn3i13}, latestUpdate.removeConns)
	conns = pool.SnapshotConns()
	require.Equal(t, 6, len(conns))
	// make sure attributes on existing connections were updated to latest
	// values from resolver
	require.Equal(t, attrs1, conn1.Address().Attributes)
	require.Equal(t, attrs1, conn1i7.Address().Attributes)
	require.Equal(t, attrs2, conn2.Address().Attributes)
	require.Equal(t, attrs2, conn2i10.Address().Attributes)
	require.Equal(t, attrs3, conn3.Address().Attributes)
	require.Equal(t, attrs3, conn3i12.Address().Attributes)

	// Last change. Subsetter just returns the first 4.
	subsetter.Set(func(allAddrs []resolver.Address) []resolver.Address {
		if len(allAddrs) <= 4 {
			return allAddrs
		}
		return allAddrs[:4]
	})
	// But creation of new conns only returns 2.
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
	connMgr.ReconcileAddresses(addrs)
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
	connMgr.ReconcileAddresses(addrs)
	latestUpdate = getLatestUpdate()
	require.Equal(t, addrs[3:], latestUpdate.newAddrs)
	require.Empty(t, latestUpdate.removeConns)
}
