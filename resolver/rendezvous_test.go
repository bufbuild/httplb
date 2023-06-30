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
	"io"
	"testing"

	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRendezvous(t *testing.T) {
	t.Parallel()

	refreshCh := make(chan struct{})
	defer close(refreshCh)

	addrFoo := resolver.Address{HostPort: "foo"}
	addrBar := resolver.Address{HostPort: "bar"}
	addrBaz := resolver.Address{HostPort: "baz"}
	addrQux := resolver.Address{HostPort: "qux"}
	addresses := []resolver.Address{addrFoo, addrBar, addrBaz, addrQux}

	var rootResolver fakeResolver
	_, err := resolver.RendezvousHashSubsetter(&rootResolver, resolver.RendezvousConfig{})
	assert.ErrorContains(t, err, "NumBackends must be set")

	subsetterResolver, err := resolver.RendezvousHashSubsetter(&rootResolver, resolver.RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "foo",
	})
	require.NoError(t, err)
	var receiver fakeReceiver
	_ = subsetterResolver.New(context.Background(), "", "", &receiver, refreshCh)

	rootResolver.receiver.OnResolve([]resolver.Address{addrFoo})
	assert.Equal(t, receiver.addrs, []resolver.Address{addrFoo})

	rootResolver.receiver.OnResolve([]resolver.Address{addrFoo, addrBar})
	assert.Equal(t, receiver.addrs, []resolver.Address{addrFoo, addrBar})

	rootResolver.receiver.OnResolve(append([]resolver.Address{}, addresses...))
	set1 := receiver.addrs
	require.Len(t, set1, 2)
	assert.Contains(t, addresses, set1[0])
	assert.Contains(t, addresses, set1[1])

	subsetterResolver, err = resolver.RendezvousHashSubsetter(&rootResolver, resolver.RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "bar",
	})
	require.NoError(t, err)
	_ = subsetterResolver.New(context.Background(), "", "", &receiver, refreshCh)

	rootResolver.receiver.OnResolve(append([]resolver.Address{}, addresses...))
	set2 := receiver.addrs
	assert.NotEqual(t, set1, set2)
}

type fakeResolver struct {
	receiver resolver.Receiver
}

func (f *fakeResolver) New(
	_ context.Context,
	_, _ string,
	receiver resolver.Receiver,
	_ <-chan struct{},
) io.Closer {
	f.receiver = receiver
	return nil
}

type fakeReceiver struct {
	addrs []resolver.Address
	err   error
}

func (r *fakeReceiver) OnResolve(addrs []resolver.Address) {
	r.addrs = addrs
}

func (r *fakeReceiver) OnResolveError(err error) {
	r.err = err
}
