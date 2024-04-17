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
	"testing"

	. "github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
)

func TestMinConnections(t *testing.T) {
	t.Parallel()

	refreshCh := make(chan struct{})
	defer close(refreshCh)

	addrFoo := Address{HostPort: "foo"}
	addrBar := Address{HostPort: "bar"}
	addrBaz := Address{HostPort: "baz"}
	addrQux := Address{HostPort: "qux"}
	addresses := []Address{addrFoo, addrBar, addrBaz, addrQux}

	var resolver fakeResolver
	minResolver := MinConnections(&resolver, 6)
	var receiver fakeReceiver
	_ = minResolver.New(context.Background(), "", "", &receiver, refreshCh)

	resolver.receiver.OnResolve([]Address{})
	assert.Empty(t, receiver.addrs)

	resolver.receiver.OnResolve([]Address{addrFoo})
	assert.Equal(t, []Address{ // single address, repeated 6 times
		addrFoo, addrFoo, addrFoo, addrFoo, addrFoo, addrFoo,
	}, receiver.addrs)

	resolver.receiver.OnResolve([]Address{addrFoo, addrBar})
	assert.Equal(t, []Address{ // both addresses, each repeated 3 times
		addrFoo, addrBar, addrFoo, addrBar, addrFoo, addrBar,
	}, receiver.addrs)

	resolver.receiver.OnResolve(append([]Address{}, addresses...))
	assert.Equal(t, []Address{ // all four addresses, each repeated
		addrFoo, addrBar, addrBaz, addrQux, addrFoo, addrBar, addrBaz, addrQux,
	}, receiver.addrs)

	minResolver = MinConnections(&resolver, 3)
	_ = minResolver.New(context.Background(), "", "", &receiver, refreshCh)

	resolver.receiver.OnResolve(append([]Address{}, addresses...))
	assert.Equal(t, []Address{ // all four addresses, no repetition
		addrFoo, addrBar, addrBaz, addrQux,
	}, receiver.addrs)
}
