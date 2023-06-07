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
	"errors"
	"math/rand"
	"net/http"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
)

type randomPickerFactory struct {
}

type randomPicker struct {
	conns conn.Connections
}

func NewRandomPickerFactory() Factory {
	return randomPickerFactory{}
}

func (r randomPickerFactory) New(_ Picker, allConns conn.Connections) Picker {
	return randomPicker{conns: allConns}
}

func (r randomPicker) Pick(*http.Request) (conn conn.Conn, whenDone func(), err error) {
	if r.conns.Len() == 0 {
		// TODO: if returning an error is the way to go, should we make a
		// standard error value for this?
		return nil, nil, errors.New("no hosts available")
	}

	return r.conns.Get(rand.Intn(r.conns.Len())), nil, nil //nolint:gosec
}
