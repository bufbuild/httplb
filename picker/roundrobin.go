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
	"net/http"
	"sync/atomic"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/internal"
)

//nolint:gochecknoglobals
var (
	// RoundRobinFactory creates pickers that pick connections in a "round-robin"
	// fashion, that is to say, in sequential order. In order to mitigate the risk
	// of a "thundering herd" scenario, the order of connections is randomized
	// each time the list of hosts changes.
	RoundRobinFactory Factory = roundRobinFactory{}
)

type roundRobinFactory struct{}

type roundRobin struct {
	conns []conn.Conn
	// +checkatomic
	counter atomic.Int64
}

func (f roundRobinFactory) New(_ Picker, allConns conn.Conns) Picker {
	rnd := internal.NewRand()
	numConns := allConns.Len()
	conns := make([]conn.Conn, numConns)
	for i := 0; i < numConns; i++ {
		conns[i] = allConns.Get(i)
	}
	rnd.Shuffle(numConns, func(i, j int) {
		conns[i], conns[j] = conns[j], conns[i]
	})
	picker := &roundRobin{conns: conns}
	picker.counter.Store(-1)
	return picker
}

func (r *roundRobin) Pick(_ *http.Request) (conn conn.Conn, whenDone func(), err error) {
	return r.conns[uint64(r.counter.Add(1))%uint64(len(r.conns))], nil, nil
}
