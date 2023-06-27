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

package basebalancer

import (
	"math/rand"
	"net/http"
	"sync/atomic"

	"github.com/bufbuild/httplb/balancer"
	"github.com/bufbuild/httplb/internal"
)

//nolint:gochecknoglobals
var (
	// PowerOfTwoPickerFactory creates pickers that select two connections at random
	// and pick the one with fewer requests. This takes advantage of the
	// [power of two random choices], which provides substantial benefits
	// over a simple random picker and, unlike the least-loaded policy, doesn't
	// need to maintain a heap.
	//
	// [power of two random choices]: http://www.eecs.harvard.edu/~michaelm/postscripts/handbook2001.pdf
	PowerOfTwoPickerFactory PickerFactory = &powerOfTwoPickerFactory{}
)

type powerOfTwoPickerFactory struct{}

func (f powerOfTwoPickerFactory) New(prev balancer.Picker, allConns balancer.Conns) balancer.Picker {
	itemMap := map[balancer.Conn]*powerOfTwoConnItem{}

	if prev, ok := prev.(*powerOfTwoPicker); ok {
		for _, entry := range prev.conns {
			itemMap[entry.conn] = entry
		}
	}

	newConns := make([]*powerOfTwoConnItem, allConns.Len())
	for i := range newConns {
		conn := allConns.Get(i)
		if item, ok := itemMap[conn]; ok {
			newConns[i] = item
		} else {
			newConns[i] = &powerOfTwoConnItem{conn: conn}
		}
	}

	return &powerOfTwoPicker{
		conns: newConns,
		rng:   internal.NewLockedRand(),
	}
}

type powerOfTwoPicker struct {
	conns []*powerOfTwoConnItem
	rng   *rand.Rand
}

func (p *powerOfTwoPicker) Pick(*http.Request) (conn balancer.Conn, whenDone func(), err error) {
	entry1 := p.conns[p.rng.Intn(len(p.conns))]
	entry2 := p.conns[p.rng.Intn(len(p.conns))]

	var entry *powerOfTwoConnItem
	if uint64(entry1.load.Load()) < uint64(entry2.load.Load()) {
		entry = entry1
	} else {
		entry = entry2
	}

	entry.load.Add(1)
	whenDone = func() {
		entry.load.Add(-1)
	}

	return entry.conn, whenDone, nil
}

type powerOfTwoConnItem struct {
	conn balancer.Conn
	// +checkatomic
	load atomic.Int64
}
