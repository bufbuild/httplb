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

package subsetter_test

import (
	"testing"

	"github.com/bufbuild/httplb/balancer/connmanager/subsetter"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRendezvous(t *testing.T) {
	t.Parallel()

	addrFoo := resolver.Address{HostPort: "foo"}
	addrBar := resolver.Address{HostPort: "bar"}
	addrBaz := resolver.Address{HostPort: "baz"}
	addrQux := resolver.Address{HostPort: "qux"}
	addresses := []resolver.Address{addrFoo, addrBar, addrBaz, addrQux}

	_, err := subsetter.NewRendezvous(subsetter.RendezvousConfig{})
	assert.ErrorContains(t, err, "NumBackends must be set")

	sub, err := subsetter.NewRendezvous(subsetter.RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "foo",
	})
	require.NoError(t, err)
	assert.Equal(t,
		[]resolver.Address{addrFoo},
		sub.ComputeSubset([]resolver.Address{addrFoo}))
	assert.Equal(t,
		[]resolver.Address{addrFoo, addrBar},
		sub.ComputeSubset([]resolver.Address{addrFoo, addrBar}))
	set1 := sub.ComputeSubset(append([]resolver.Address{}, addresses...))
	require.Len(t, set1, 2)
	assert.Contains(t, addresses, set1[0])
	assert.Contains(t, addresses, set1[1])

	sub, err = subsetter.NewRendezvous(subsetter.RendezvousConfig{
		NumBackends:  2,
		SelectionKey: "bar",
	})
	require.NoError(t, err)
	set2 := sub.ComputeSubset(append([]resolver.Address{}, addresses...))
	assert.NotEqual(t, set1, set2)
}
