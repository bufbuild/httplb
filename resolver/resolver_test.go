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

package resolver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/bufbuild/go-http-balancer/internal/clocktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testReceiver struct {
	onResolve      func([]Address)
	onResolveError func(error)
}

func (r testReceiver) OnResolve(addresses []Address) {
	r.onResolve(addresses)
}

func (r testReceiver) OnResolveError(err error) {
	r.onResolveError(err)
}

func TestResolverTTL(t *testing.T) {
	t.Parallel()

	const testTTL = 20 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	testClock := clocktest.NewFakeClock()
	resolver := NewDNSResolver(net.DefaultResolver, "ip6", testTTL)
	resolver.(*pollingResolver).clock = testClock

	counter := 0
	worker := resolver.Resolve(ctx, "http", "::1", testReceiver{
		onResolve: func(a []Address) {
			assert.Equal(t, "::1", a[0].HostPort)
			counter++
		},
		onResolveError: func(err error) {
			t.Errorf("unexpected resolution error: %v", err)
		},
	})

	t.Cleanup(func() {
		err := worker.Close()
		require.NoError(t, err)
	})

	err := testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, counter)

	testClock.Advance(testTTL)

	err = testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)
	assert.Equal(t, 2, counter)
}
