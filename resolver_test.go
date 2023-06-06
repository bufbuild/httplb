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
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type resolverResult struct {
	addresses []Address
	ttl       time.Duration
	err       error
}

type testResolver struct {
	done    chan struct{}
	results []resolverResult
}

func newTestResolver(results []resolverResult) *testResolver {
	return &testResolver{
		done:    make(chan struct{}),
		results: results,
	}
}

func (t *testResolver) wait() {
	<-t.done
}

func (t *testResolver) Resolve(
	_ context.Context,
	_, _ string,
	cb ResolverCallbackFunc,
) io.Closer {
	for _, result := range t.results {
		cb(result.addresses, result.ttl, result.err)
	}
	close(t.done)
	return io.NopCloser(nil)
}

func TestCachingResolver(t *testing.T) {
	t.Parallel()

	testResolver := newTestResolver([]resolverResult{
		{addresses: []Address{{HostPort: "1.2.3.4"}}},
		{err: errors.New("unexpected error")},
	})

	cacheResolver := NewCachingResolver(testResolver, time.Hour)

	cb := func(addresses []Address, ttl time.Duration, err error) {
		assert.Contains(t, addresses, Address{HostPort: "1.2.3.4"})
	}
	closer := cacheResolver.Resolve(context.Background(), "https", "test", cb)
	testResolver.wait()
	closer.Close()
}
