// Copyright 2023-2025 Buf Technologies, Inc.
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

package picker

import (
	"container/heap"
	"math/bits"
	"math/rand/v2"
	"net/http"
	"sync"

	"github.com/bufbuild/httplb/conn"
)

// NewLeastLoadedRoundRobin creates pickers that pick the connection
// with the fewest in-flight requests. When a tie occurs, tied hosts will
// be picked in an arbitrary but sequential order.
func NewLeastLoadedRoundRobin(prev Picker, allConns conn.Conns) Picker {
	if prev, ok := prev.(*leastLoadedRoundRobin); ok {
		prev.mu.Lock()
		defer prev.mu.Unlock()

		prev.conns.update(allConns)
		return prev
	}

	return &leastLoadedRoundRobin{
		leastLoadedBase: leastLoadedBase{
			conns: newConnHeap(allConns),
		},
	}
}

// NewLeastLoadedRandom creates pickers that pick the connection with
// the fewest in-flight requests. When a tie occurs, tied hosts will be
// picked at random.
func NewLeastLoadedRandom(prev Picker, allConns conn.Conns) Picker {
	if prev, ok := prev.(*leastLoadedRandom); ok {
		prev.mu.Lock()
		defer prev.mu.Unlock()

		prev.conns.update(allConns)
		return prev
	}

	return &leastLoadedRandom{
		leastLoadedBase: leastLoadedBase{
			conns: newConnHeap(allConns),
		},
	}
}

type leastLoadedBase struct {
	mu sync.Mutex
	// +checklocks:mu
	conns *leastLoadedConnHeap
}

type leastLoadedRoundRobin struct {
	leastLoadedBase
	// +checklocks:mu
	counter uint64
}

type leastLoadedRandom struct {
	leastLoadedBase
}

//nolint:recvcheck // mix of pointer and non-pointer receiver methods is intentional
type leastLoadedConnHeap []*leastLoadedConnItem

type leastLoadedConnItem struct {
	conn     conn.Conn
	load     uint64
	tieBreak uint64
	index    int
}

// +checklocks:p.mu
func (p *leastLoadedBase) pickLocked(nextTieBreak uint64) (conn conn.Conn, whenDone func(), _ error) { //nolint:unparam
	entry := p.conns.acquire(nextTieBreak)
	return entry.conn,
		func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.conns.release(entry)
		},
		nil
}

func (p *leastLoadedRoundRobin) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.counter++
	return p.leastLoadedBase.pickLocked(p.counter)
}

func (p *leastLoadedRandom) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.leastLoadedBase.pickLocked(rand.Uint64()) //nolint:gosec // don't need crypto/rand here
}

func newConnHeap(allConns conn.Conns) *leastLoadedConnHeap {
	newConns := make([]*leastLoadedConnItem, allConns.Len())
	newHeap := leastLoadedConnHeap(newConns)
	for i := range newConns {
		newConns[i] = &leastLoadedConnItem{
			conn:  allConns.Get(i),
			index: i,
		}
	}
	heap.Init(&newHeap)
	return &newHeap
}

func (h *leastLoadedConnHeap) update(allConns conn.Conns) {
	newMap := map[conn.Conn]struct{}{}
	for i, l := 0, allConns.Len(); i < l; i++ {
		newMap[allConns.Get(i)] = struct{}{}
	}
	j := 0 //nolint:varnamelen
	slice := *h
	// Remove items from slice that aren't in the new set of conns,
	// compacting the slice as we go.
	for i, item := range slice {
		if _, ok := newMap[item.conn]; ok {
			delete(newMap, item.conn)
			if i != j {
				item.index = j
				(*h)[j] = item
			}
			j++
		} else {
			// If there are pending ops with this one, make sure it
			// knows it's been evicted.
			item.index = -1
		}
	}
	newLen := j + len(newMap)
	if j == len(slice) {
		// No items removed, so we haven't broken any heap invariants.
		// If we don't have too many items to add, just heap.Push them
		// and return.
		threshold := newLen / bits.Len(uint(newLen))
		// Push is O(log n). Init (aka heapify) is O(n). So threshold
		// is (n / log n). If there are more items than that, it's
		// better to fall through below and re-init.
		if len(newMap) <= threshold {
			for cn := range newMap {
				h.Push(&leastLoadedConnItem{conn: cn})
			}
			return
		}
	} else if len(slice) > newLen {
		// Make sure we don't leak memory with dangling pointers
		// in unused regions of the slice.
		for i := range slice[newLen:] {
			slice[newLen+i] = nil
		}
	}
	// Now add remaining new connections.
	slice = slice[:j]
	for cn := range newMap {
		slice = append(slice, &leastLoadedConnItem{conn: cn, index: len(slice)})
	}
	*h = slice
	// Re-heapify
	heap.Init(h)
}

func (h *leastLoadedConnHeap) acquire(nextTieBreak uint64) *leastLoadedConnItem {
	entry := (*h)[0]
	entry.load++
	entry.tieBreak = nextTieBreak
	heap.Fix(h, entry.index)
	return entry
}

func (h *leastLoadedConnHeap) release(entry *leastLoadedConnItem) {
	entry.load--
	if entry.index != -1 {
		heap.Fix(h, entry.index)
	}
}

func (h leastLoadedConnHeap) Len() int { return len(h) }

func (h leastLoadedConnHeap) Less(i, j int) bool {
	if h[i].load == h[j].load {
		return h[i].tieBreak < h[j].tieBreak
	}
	return h[i].load < h[j].load
}

func (h leastLoadedConnHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *leastLoadedConnHeap) Push(x any) {
	n := len(*h)
	item := x.(*leastLoadedConnItem) //nolint:forcetypeassert,errcheck
	item.index = n
	*h = append(*h, item)
}

func (h *leastLoadedConnHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[0 : n-1]
	return item
}
