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

package subsetter

import (
	"container/heap"
	"hash/fnv"

	"github.com/bufbuild/go-http-balancer/resolver"
)

type rendezvousSubsetter struct {
	key []byte
	k   int
}

type addressHeap struct {
	addrs []resolver.Address
	ranks []uint64
	key   []byte
}

// NewRendezvous returns a subsetter that uses Rendezvous hashing to pick a
// randomly-distributed but consistent set of k hosts provided a value to use
// as a key. When provided the same selectionKey and k value, it will return
// the same hosts. When a host is removed, all of the requests directed to it
// with this subsetter will be distributed randomly to other hosts in the pool.
func NewRendezvous(selectionKey string, k int) Subsetter {
	return &rendezvousSubsetter{
		key: []byte(selectionKey),
		k:   k,
	}
}

func (s *rendezvousSubsetter) ComputeSubset(addrs []resolver.Address) []resolver.Address {
	if len(addrs) <= s.k {
		return addrs
	}

	n, k := len(addrs), s.k
	addrHeap := newAddressHeap(addrs[:s.k], s.key, s.hash)
	for i := k; i < n; i++ {
		rank := addrHeap.rank(addrs[i])
		if rank > addrHeap.ranks[0] {
			addrHeap.addrs[0] = addrs[i]
			addrHeap.ranks[0] = rank
			heap.Fix(addrHeap, 0)
		}
	}

	return addrHeap.addrs
}

func newAddressHeap(addrs []resolver.Address, key []byte) *addressHeap {
	addrHeap := &addressHeap{
		addrs: addrs,
		ranks: make([]uint64, len(addrs)),
		key:   key,
	}
	for i := range addrHeap.ranks {
		addrHeap.ranks[i] = addrHeap.rank(addrHeap.addrs[i])
	}
	heap.Init(addrHeap)
	return addrHeap
}

func (h addressHeap) rank(addr resolver.Address) uint64 {
	hash := fnv.New64a()
	hash.Write(h.key)
	hash.Write([]byte(addr.HostPort))
	return hash.Sum64()
}

func (h addressHeap) Len() int { return len(h.addrs) }

func (h addressHeap) Less(i, j int) bool {
	return h.ranks[i] < h.ranks[j]
}

func (h addressHeap) Swap(i, j int) {
	h.addrs[i], h.addrs[j] = h.addrs[j], h.addrs[i]
	h.ranks[i], h.ranks[j] = h.ranks[j], h.ranks[i]
}

func (h *addressHeap) Push(any) { panic("Push should not be called") } //nolint:forbidigo // inaccessible code
func (h *addressHeap) Pop() any { panic("Pop should not be called") }  //nolint:forbidigo // inaccessible code
