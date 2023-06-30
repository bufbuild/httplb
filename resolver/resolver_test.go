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

package resolver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/bufbuild/httplb/internal/clocktest"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolverTTL(t *testing.T) {
	t.Parallel()

	refreshCh := make(chan struct{})

	const testTTL = 20 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	testClock := clocktest.NewFakeClock()
	dnsResolver := resolver.NewDNSResolver(net.DefaultResolver, "ip6", testTTL)
	resolver.SetPollingClock(dnsResolver, testClock)

	signal := make(chan struct{})
	task := dnsResolver.New(ctx, "http", "::1", testReceiver{
		onResolve: func(a []resolver.Address) {
			assert.Equal(t, "[::1]:80", a[0].HostPort)
			signal <- struct{}{}
		},
		onResolveError: func(err error) {
			t.Errorf("unexpected resolution error: %v", err)
		},
	}, refreshCh)
	waitForResolve := func() {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("expected call to resolver")
		}
	}

	t.Cleanup(func() {
		close(signal)
		err := task.Close()
		close(refreshCh)
		require.NoError(t, err)
	})

	waitForResolve()
	err := testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)

	// When advancing the clock past the TTL, we should get a new probe.
	testClock.Advance(testTTL)
	waitForResolve()
	err = testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)

	// When we call ResolveNow, we should get a new probe.
	select {
	case refreshCh <- struct{}{}:
	case <-ctx.Done():
		t.Fatalf("cancelled before refresh channel unblocked: %v", ctx.Err())
	}
	waitForResolve()
	err = testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)
}

type testReceiver struct {
	onResolve      func([]resolver.Address)
	onResolveError func(error)
}

func (r testReceiver) OnResolve(addresses []resolver.Address) {
	r.onResolve(addresses)
}

func (r testReceiver) OnResolveError(err error) {
	r.onResolveError(err)
}
