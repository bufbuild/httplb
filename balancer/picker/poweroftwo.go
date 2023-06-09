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
	"math/rand"
	"net/http"
	"sync/atomic"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/internal"
)

//nolint:gochecknoglobals
var (
	// PowerOfTwoFactory creates pickers that pick two connections at random,
	// then select the one with less requests.
	PowerOfTwoFactory Factory = &powerOfTwoFactory{}
)

type powerOfTwoFactory struct{}

type powerOfTwo struct {
	conns []*powerOfTwoConnItem
	rng   *rand.Rand
}

type powerOfTwoConnItem struct {
	conn conn.Conn
	// +checkatomic
	load atomic.Int64
}

func (f powerOfTwoFactory) New(prev Picker, allConns conn.Connections) Picker {
	itemMap := map[conn.Conn]*powerOfTwoConnItem{}

	if prev, ok := prev.(*powerOfTwo); ok {
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

	return &powerOfTwo{
		conns: newConns,
		rng:   internal.NewLockedRand(),
	}
}

func (p *powerOfTwo) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
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
