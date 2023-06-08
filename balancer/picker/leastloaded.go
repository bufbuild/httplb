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
	"sync/atomic"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/internal"
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

	_ = heapDuper(&leastLoadedRoundRobin{})
	_ = heapDuper(&leastLoadedRandom{})
)

type leastLoadedRoundRobinFactory struct{}

type leastLoadedRandomFactory struct{}

type leastLoadedBase struct {
	mu sync.Mutex
	// +checklocks:mu
	conns *connHeap
}

type heapDuper interface {
	heapDup() connHeap
}

type leastLoadedRoundRobin struct {
	leastLoadedBase
	// +checkatomic
	counter atomic.Uint64
}

type leastLoadedRandom struct {
	leastLoadedBase
	rng *rand.Rand
}

type connHeap []*connItem

type connItem struct {
	// We can't express via checklocks, but these atomics are intended to be
	// mixed with the picker lock: if the mutex is held, reads do not need to
	// use atomics (but writes still do.)
	// checklocks actually supports this condition, but there's no way to tell
	// it where the lock is, since it's not reachable from this struct.

	// NOTE: The atomics must come first, or else we risk breaking 64-bit
	// alignment on 32-bit platforms.

	// +checkatomic
	load uint64

	// +checkatomic
	tiebreak uint64

	conn conn.Conn

	index int
}

func newLeastLoadedHeap(prev Picker, allConns conn.Connections) *connHeap {
	var newHeap = new(connHeap)
	if prev, ok := prev.(heapDuper); ok {
		prevMap := map[conn.Conn]*connItem{}
		for _, item := range prev.heapDup() {
			prevMap[item.conn] = item
		}
		for i, l := 0, allConns.Len(); i < l; i++ {
			var load, tiebreak uint64
			conn := allConns.Get(i)
			if prev, ok := prevMap[conn]; ok {
				load = atomic.LoadUint64(&prev.load)
				tiebreak = atomic.LoadUint64(&prev.tiebreak)
			}
			newHeap.Push(&connItem{
				conn:     conn,
				load:     load,
				tiebreak: tiebreak,
				index:    i,
			})
		}
	} else {
		for i, l := 0, allConns.Len(); i < l; i++ {
			newHeap.Push(&connItem{
				conn:     allConns.Get(i),
				load:     0,
				tiebreak: 0,
				index:    i,
			})
		}
	}
	return newHeap
}

func (f leastLoadedRoundRobinFactory) New(prev Picker, allConns conn.Connections) Picker {
	newHeap := newLeastLoadedHeap(prev, allConns)
	return &leastLoadedRoundRobin{
		leastLoadedBase: leastLoadedBase{conns: newHeap},
	}
}

func (f leastLoadedRandomFactory) New(prev Picker, allConns conn.Connections) Picker {
	var rng *rand.Rand
	newHeap := newLeastLoadedHeap(prev, allConns)
	if prev, ok := prev.(*leastLoadedRandom); ok {
		rng = prev.rng
	} else {
		rng = internal.NewLockedRand()
	}
	return &leastLoadedRandom{
		leastLoadedBase: leastLoadedBase{conns: newHeap},
		rng:             rng,
	}
}

func (p *leastLoadedBase) pick(nextTieBreak uint64) (conn conn.Conn, whenDone func(), err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry := p.conns.acquire()
	atomic.StoreUint64(&entry.tiebreak, nextTieBreak)

	whenDone = func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		p.conns.release(entry)
	}

	return entry.conn, whenDone, nil
}

func (p *leastLoadedBase) heapDup() connHeap {
	p.mu.Lock()
	defer p.mu.Unlock()

	h := make([]*connItem, len(*p.conns))
	copy(h, *p.conns)
	return connHeap(h)
}

func (p *leastLoadedRoundRobin) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	return p.leastLoadedBase.pick(p.counter.Add(1))
}

func (p *leastLoadedRandom) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	// TODO: will this give a good random distribution?
	// need more thought here.
	return p.leastLoadedBase.pick(p.rng.Uint64())
}

func (h connHeap) Len() int { return len(h) }

// +checklocksignore see note regarding atomics in connItem.
func (h connHeap) Less(i, j int) bool {
	if h[i].load == h[j].load {
		return h[i].tiebreak < h[j].tiebreak
	}
	return h[i].load < h[j].load
}

func (h connHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *connHeap) Push(x any) {
	n := len(*h)
	item, _ := x.(*connItem)
	item.index = n
	*h = append(*h, item)
}

func (h *connHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[0 : n-1]
	return item
}

func (h *connHeap) acquire() *connItem {
	entry := (*h)[0]
	atomic.AddUint64(&entry.load, 1)
	heap.Fix(h, entry.index)
	return entry
}

func (h *connHeap) release(entry *connItem) {
	atomic.AddUint64(&entry.load, ^uint64(0))
	heap.Fix(h, entry.index)
}
