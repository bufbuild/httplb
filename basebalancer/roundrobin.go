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
	"net/http"
	"sync/atomic"

	"github.com/bufbuild/httplb/balancer"
	"github.com/bufbuild/httplb/internal"
)

//nolint:gochecknoglobals
var (
	// RoundRobinPickerFactory creates pickers that pick connections in a "round-robin"
	// fashion, that is to say, in sequential order. In order to mitigate the risk
	// of a "thundering herd" scenario, the order of connections is randomized
	// each time the list of hosts changes.
	RoundRobinPickerFactory PickerFactory = roundRobinPickerFactory{}
)

type roundRobinPickerFactory struct{}

type roundRobinPicker struct {
	conns []balancer.Conn
	// +checkatomic
	counter atomic.Int64
}

func (f roundRobinPickerFactory) New(_ balancer.Picker, allConns balancer.Conns) balancer.Picker {
	rnd := internal.NewRand()
	numConns := allConns.Len()
	conns := make([]balancer.Conn, numConns)
	for i := 0; i < numConns; i++ {
		conns[i] = allConns.Get(i)
	}
	rnd.Shuffle(numConns, func(i, j int) {
		conns[i], conns[j] = conns[j], conns[i]
	})
	picker := &roundRobinPicker{conns: conns}
	picker.counter.Store(-1)
	return picker
}

func (r *roundRobinPicker) Pick(_ *http.Request) (conn balancer.Conn, whenDone func(), err error) {
	return r.conns[uint64(r.counter.Add(1))%uint64(len(r.conns))], nil, nil
}
