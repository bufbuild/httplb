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
	"testing"

	"github.com/bufbuild/httplb/balancer"
	"github.com/bufbuild/httplb/basebalancer"
	"github.com/bufbuild/httplb/internal/balancertesting"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/require"
)

func TestDefaultConnManager(t *testing.T) {
	t.Parallel()
	type updateReq struct {
		newAddrs    []resolver.Address
		removeConns []balancer.Conn
	}
	updates := make(chan updateReq, 1)
	pool := balancertesting.NewFakeConnPool()
	createConns := func(newAddrs []resolver.Address) []balancer.Conn {
		conns := make([]balancer.Conn, len(newAddrs))
		for i := range newAddrs {
			var ok bool
			conns[i], ok = pool.NewConn(newAddrs[i])
			require.True(t, ok)
		}
		return conns
	}
	testUpdate := func(newAddrs []resolver.Address, removeConns []balancer.Conn) (added []balancer.Conn) {
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
	factory, _ := balancertesting.DeterministicConnManagerFactory(basebalancer.NewConnManagerFactory())
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
	require.Equal(t, []balancer.Conn{conn4, conn5, conn6}, latestUpdate.removeConns)
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
	attrKey := resolver.NewAttrKey[int]()
	attrs1a := resolver.NewAttrs(attrKey.Value(101))
	attrs1b := resolver.NewAttrs(attrKey.Value(102))
	attrs2a := resolver.NewAttrs(attrKey.Value(201))
	attrs2b := resolver.NewAttrs(attrKey.Value(202))
	attrs3a := resolver.NewAttrs(attrKey.Value(301))
	attrs3b := resolver.NewAttrs(attrKey.Value(302))
	addrs = []resolver.Address{
		{HostPort: "1.2.3.1", Attributes: attrs1a},
		{HostPort: "1.2.3.1", Attributes: attrs1b},
		{HostPort: "1.2.3.2", Attributes: attrs2a},
		{HostPort: "1.2.3.2", Attributes: attrs2b},
		{HostPort: "1.2.3.3", Attributes: attrs3a},
		{HostPort: "1.2.3.3", Attributes: attrs3b},
	}
	connMgr.ReconcileAddresses(addrs)
	latestUpdate = getLatestUpdate()
	require.Empty(t, latestUpdate.newAddrs)
	require.Equal(t, []balancer.Conn{conn1i8, conn1i9, conn2i11, conn3i13}, latestUpdate.removeConns)
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
	createConns = func(newAddrs []resolver.Address) []balancer.Conn {
		conns := make([]balancer.Conn, 0, len(newAddrs))
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
	require.Equal(t, []balancer.Conn{conn1, conn1i7, conn2i10, conn3, conn3i12}, latestUpdate.removeConns)

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
