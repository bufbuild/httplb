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
	"sort"
	"testing"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/internal/conns"
	"github.com/stretchr/testify/require"
)

func TestLeastLoadedConnHeap(t *testing.T) {
	t.Parallel()
	heap := newConnHeap(conns.FromSlice([]conn.Conn{
		dummyConn{id: "a"},
		dummyConn{id: "b"},
		dummyConn{id: "c"},
		dummyConn{id: "d"},
		dummyConn{id: "e"},
		dummyConn{id: "f"},
	}))
	counts := map[string]uint64{
		"a": 0,
		"b": 0,
		"c": 0,
		"d": 0,
		"e": 0,
		"f": 0,
	}
	verifyHeap(t, heap, counts)

	// No repeats since they all have weight zero.
	verifyPicks(t, heap, counts, "abcdef")
	// Now they all have weight one, so they all repeat. But
	// we don't see any item a third time until we've seen
	// all of 'em 2x.
	verifyPicks(t, heap, counts, "abcdef")

	verifyReleases(t, heap, counts, "aabb")

	// Now a and b have a load of zero, but the others have load 2.
	// So we'll pick them next.
	verifyPicks(t, heap, counts, "aabb")

	snapshot := snapshotHeap(heap)
	// Update state with new connections. We should forget
	// a, b, c, and d and add g and h.
	heap.update(conns.FromSlice([]conn.Conn{
		dummyConn{id: "e"},
		dummyConn{id: "f"},
		dummyConn{id: "g"},
		dummyConn{id: "h"},
	}))
	counts = map[string]uint64{
		"e": 2,
		"f": 2,
		"g": 0,
		"h": 0,
	}
	verifyHeap(t, heap, counts)

	// Releasing items no longer present has no impact.
	heap.release(snapshot["a"])
	heap.release(snapshot["b"])
	heap.release(snapshot["c"])
	heap.release(snapshot["a"])
	verifyHeap(t, heap, counts)

	// g and h have less load, so we favor them.
	verifyPicks(t, heap, counts, "gghh")
	// Now everything has load == 2. So next four picks sees
	// each of the four items.
	verifyPicks(t, heap, counts, "efgh")

	// No-op update
	heap.update(conns.FromSlice([]conn.Conn{
		dummyConn{id: "h"},
		dummyConn{id: "g"},
		dummyConn{id: "f"},
		dummyConn{id: "e"},
	}))
	verifyHeap(t, heap, counts)

	// Update that must grow backing slice
	heap.update(conns.FromSlice([]conn.Conn{
		dummyConn{id: "a"},
		dummyConn{id: "b"},
		dummyConn{id: "c"},
		dummyConn{id: "d"},
		dummyConn{id: "e"},
		dummyConn{id: "f"},
		dummyConn{id: "g"},
		dummyConn{id: "h"},
		dummyConn{id: "i"},
		dummyConn{id: "j"},
		dummyConn{id: "k"},
		dummyConn{id: "l"},
	}))
	counts = map[string]uint64{
		"a": 0,
		"b": 0,
		"c": 0,
		"d": 0,
		"e": 3,
		"f": 3,
		"g": 3,
		"h": 3,
		"i": 0,
		"j": 0,
		"k": 0,
		"l": 0,
	}
	verifyHeap(t, heap, counts)

	// Update with small number of new items. This will go
	// the simple heap.Push route instead of append+re-init.
	heap.update(conns.FromSlice([]conn.Conn{
		dummyConn{id: "a"},
		dummyConn{id: "b"},
		dummyConn{id: "c"},
		dummyConn{id: "d"},
		dummyConn{id: "e"},
		dummyConn{id: "f"},
		dummyConn{id: "g"},
		dummyConn{id: "h"},
		dummyConn{id: "i"},
		dummyConn{id: "j"},
		dummyConn{id: "k"},
		dummyConn{id: "l"},
		dummyConn{id: "m"},
		dummyConn{id: "n"},
	}))
	counts = map[string]uint64{
		"a": 0,
		"b": 0,
		"c": 0,
		"d": 0,
		"e": 3,
		"f": 3,
		"g": 3,
		"h": 3,
		"i": 0,
		"j": 0,
		"k": 0,
		"l": 0,
		"m": 0,
		"n": 0,
	}
	verifyHeap(t, heap, counts)
}

func verifyPicks(t *testing.T, heap *leastLoadedConnHeap, counts map[string]uint64, ids string) {
	t.Helper()
	expected := make([]string, len(ids))
	actuals := make([]string, len(ids))
	for i, ch := range ids {
		expectedID := string(ch)
		item := heap.acquire(0)
		actualID := connID(item.conn)
		expected[i] = expectedID
		actuals[i] = actualID
		counts[actualID]++
		verifyHeap(t, heap, counts)
	}
	// Order can be non-intuitive, due to internal state of the heap, how
	// it models a binary tree inside the slice, and the details of sifting
	// things up and down the tree when state changes. So we just sort the
	// expected and actuals: the identities of which connections were
	// picked is more important than the order of picking.
	sort.Strings(expected)
	sort.Strings(actuals)
	require.Equal(t, expected, actuals)
}

func verifyReleases(t *testing.T, heap *leastLoadedConnHeap, counts map[string]uint64, ids string) {
	t.Helper()
	for _, ch := range ids {
		id := string(ch)
		release(t, heap, id)
		counts[id]--
		verifyHeap(t, heap, counts)
	}
}

func release(t *testing.T, heap *leastLoadedConnHeap, id string) { //nolint:varnamelen
	t.Helper()
	for _, item := range *heap {
		if connID(item.conn) == id {
			heap.release(item)
			return
		}
	}
	t.Fatalf("item %s not found in heap", id)
}

func snapshotHeap(heap *leastLoadedConnHeap) map[string]*leastLoadedConnItem {
	snapshot := make(map[string]*leastLoadedConnItem, len(*heap))
	for _, item := range *heap {
		snapshot[connID(item.conn)] = item
	}
	return snapshot
}

func verifyHeap(t *testing.T, heap *leastLoadedConnHeap, counts map[string]uint64) {
	t.Helper()
	for i, item := range *heap {
		require.Equal(t, i, item.index)
		count, ok := counts[connID(item.conn)]
		require.True(t, ok)
		require.Equal(t, count, item.load)
		if i > 0 {
			// heap invariant
			parent := (i - 1) / 2
			require.LessOrEqual(t, (*heap)[parent].load, item.load)
		}
	}
	backingArray := (*heap)[:cap(*heap)]
	for i := len(*heap); i < len(backingArray); i++ {
		// make sure everything else in the backing array, after
		// the end of the heap, is cleared and not pinning any item
		require.Nil(t, backingArray[i])
	}
}

type dummyConn struct {
	conn.Conn
	id string
}

func connID(cn conn.Conn) string {
	return cn.(dummyConn).id //nolint:errcheck
}
