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

	. "github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRendezvous(t *testing.T) {
	t.Parallel()

	refreshCh := make(chan struct{})
	defer close(refreshCh)

	addrFoo := Address{HostPort: "foo"}
	addrBar := Address{HostPort: "bar"}
	addrBaz := Address{HostPort: "baz"}
	addrQux := Address{HostPort: "qux"}
	addresses := []Address{addrFoo, addrBar, addrBaz, addrQux}

	var resolver fakeResolver
	_, err := RendezvousHashSubsetter(&resolver, RendezvousConfig{})
	require.ErrorContains(t, err, "NumBackends must be set")

	subsetterResolver, err := RendezvousHashSubsetter(&resolver, RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "foo",
	})
	require.NoError(t, err)
	var receiver fakeReceiver
	_ = subsetterResolver.New(context.Background(), "", "", &receiver, refreshCh)

	resolver.receiver.OnResolve([]Address{addrFoo})
	assert.Equal(t, []Address{addrFoo}, receiver.addrs)

	resolver.receiver.OnResolve([]Address{addrFoo, addrBar})
	assert.Equal(t, []Address{addrFoo, addrBar}, receiver.addrs)

	resolver.receiver.OnResolve(append([]Address{}, addresses...))
	set1 := receiver.addrs
	require.Len(t, set1, 2)
	assert.Contains(t, addresses, set1[0])
	assert.Contains(t, addresses, set1[1])

	subsetterResolver, err = RendezvousHashSubsetter(&resolver, RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "bar",
	})
	require.NoError(t, err)
	_ = subsetterResolver.New(context.Background(), "", "", &receiver, refreshCh)

	resolver.receiver.OnResolve(append([]Address{}, addresses...))
	set2 := receiver.addrs
	assert.NotEqual(t, set1, set2)
}

type fakeResolver struct {
	receiver Receiver
}

func (f *fakeResolver) New(
	_ context.Context,
	_, _ string,
	receiver Receiver,
	_ <-chan struct{},
) io.Closer {
	f.receiver = receiver
	return nil
}

type fakeReceiver struct {
	addrs []Address
	err   error
}

func (r *fakeReceiver) OnResolve(addrs []Address) {
	r.addrs = addrs
}

func (r *fakeReceiver) OnResolveError(err error) {
	r.err = err
}
