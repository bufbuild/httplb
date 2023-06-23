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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"hash"

	"github.com/bufbuild/go-http-balancer/balancer/internal"
	"github.com/bufbuild/go-http-balancer/resolver"
)

// NewRendezvous returns a subsetter that uses Rendezvous hashing to pick a
// randomly-distributed but consistent set of k hosts provided a value to use
// as a key. When provided the same selectionKey and k value, it will return
// the same hosts. When a host is removed, all of the requests directed to it
// with this subsetter will be distributed randomly to other hosts in the pool.
func NewRendezvous(options RendezvousConfig) (Subsetter, error) {
	if options.SelectionKey == "" {
		randomKey, err := randomKey()
		if err != nil {
			return nil, err
		}
		options.SelectionKey = randomKey
	}
	if options.NumBackends == 0 {
		return nil, errors.New("NumBackends must be set")
	}
	if options.Hash == nil {
		options.Hash = internal.NewMurmurHash3(0)
	}
	return &rendezvousSubsetter{
		key:  []byte(options.SelectionKey),
		k:    options.NumBackends,
		hash: options.Hash,
	}, nil
}

// RendezvousConfig represents the configuration options for use with NewRendezvous.
type RendezvousConfig struct {
	// NumBackends specifies the number of backends to select out of the set of
	// available hosts. This option is required.
	NumBackends int

	// SelectionKey specifies the key used to uniquely select hosts. This value
	// controls which hosts get selected, thus typically you set a unique value
	// for each program instance, using e.g. the machine host name. If not set,
	// a random string will be used.
	SelectionKey string

	// Hash provides a hash function to use. If unspecified, an implementation
	// of MurmurHash3 will be used.
	Hash hash.Hash32
}

type rendezvousSubsetter struct {
	key  []byte
	k    int
	hash hash.Hash32
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

type addressHeap struct {
	addrs []resolver.Address
	ranks []uint32
	key   []byte
	hash  hash.Hash32
}

func newAddressHeap(addrs []resolver.Address, key []byte, hash hash.Hash32) *addressHeap {
	addrHeap := &addressHeap{
		addrs: addrs,
		ranks: make([]uint32, len(addrs)),
		key:   key,
		hash:  hash,
	}
	for i := range addrHeap.ranks {
		addrHeap.ranks[i] = addrHeap.rank(addrHeap.addrs[i])
	}
	heap.Init(addrHeap)
	return addrHeap
}

func (h addressHeap) rank(addr resolver.Address) uint32 {
	h.hash.Reset()
	h.hash.Write(h.key)
	h.hash.Write([]byte(addr.HostPort))
	return h.hash.Sum32()
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

func randomKey() (string, error) {
	data := [16]byte{}
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
