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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testResolver struct {
	addresses []Address
	err       error
}

func (t *testResolver) ResolveOnce(_ context.Context, _, _ string) ([]Address, error) {
	return t.addresses, t.err
}

func TestCachingResolverDefaultTTL(t *testing.T) {
	t.Parallel()

	arbitraryExpiry := time.Date(2023, 06, 30, 0, 0, 0, 0, time.UTC)
	addresses := []Address{
		{
			HostPort: "with-expiry",
			Expiry:   arbitraryExpiry,
		},
		{
			HostPort: "without-expiry",
		},
	}

	signal := make(chan struct{})
	resolver := NewCachingResolver(
		NewPollingResolver(
			&testResolver{addresses: addresses},
			time.Minute,
		),
		time.Hour,
	)

	callback := func(addresses []Address, err error) {
		assert.Equal(t, arbitraryExpiry, addresses[0].Expiry)
		assert.False(t, addresses[1].Expiry.IsZero())
		close(signal)
	}

	closer := resolver.Resolve(context.Background(), "https", "test", callback)
	<-signal
	closer.Close()
}
