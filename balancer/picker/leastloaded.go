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

package picker

import (
	"container/heap"
	"math/rand"
	"net/http"
	"sync"

	"github.com/bufbuild/httplb/balancer/conn"
	"github.com/bufbuild/httplb/internal"
)

//nolint:gochecknoglobals
var (
	// LeastLoadedRoundRobinFactory creates pickers that pick the connection
	// with the least in-flight requests. When a tie occurs, tied hosts will be
	// picked in an arbitrary but sequential order.
	LeastLoadedRoundRobinFactory Factory = &leastLoadedRoundRobinFactory{}

	// LeastLoadedRandomFactory creates pickers that pick the connection with
	// the least in-flight requests. When a tie occurs, tied hosts will be
	// picked at random.
	LeastLoadedRandomFactory Factory = &leastLoadedRandomFactory{}
)

type leastLoadedRoundRobinFactory struct{}

type leastLoadedRandomFactory struct{}

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
	// +checklocks:mu
	rng *rand.Rand
}

type leastLoadedConnHeap []*leastLoadedConnItem

type leastLoadedConnItem struct {
	conn     conn.Conn
	load     uint64
	tiebreak uint64
	index    int
}

func (f leastLoadedRoundRobinFactory) New(prev Picker, allConns conn.Connections) Picker {
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

func (f leastLoadedRandomFactory) New(prev Picker, allConns conn.Connections) Picker {
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
		rng: internal.NewRand(),
	}
}

// +checklocks:p.mu
func (p *leastLoadedBase) pickLocked(nextTieBreak uint64) (conn conn.Conn, whenDone func(), _ error) { //nolint:unparam
	entry := p.conns.acquire()
	entry.tiebreak = nextTieBreak

	whenDone = func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		p.conns.release(entry)
	}

	return entry.conn, whenDone, nil
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

	return p.leastLoadedBase.pickLocked(p.rng.Uint64())
}

func newConnHeap(allConns conn.Connections) *leastLoadedConnHeap {
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

func (h *leastLoadedConnHeap) update(allConns conn.Connections) {
	newMap := map[conn.Conn]struct{}{}
	for i, l := 0, allConns.Len(); i < l; i++ {
		newMap[allConns.Get(i)] = struct{}{}
	}

	for i, l := 0, len(*h); i < l; i++ {
		item := (*h)[i]
		if _, ok := newMap[item.conn]; ok {
			delete(newMap, item.conn)
		} else {
			heap.Remove(h, item.index)
		}
	}

	for conn := range newMap {
		heap.Push(h, &leastLoadedConnItem{conn: conn})
	}
}

func (h *leastLoadedConnHeap) acquire() *leastLoadedConnItem {
	entry := (*h)[0]
	entry.load++
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
		return h[i].tiebreak < h[j].tiebreak
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
