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

	"github.com/bufbuild/httplb/balancer"
	"github.com/bufbuild/httplb/internal"
)

//nolint:gochecknoglobals
var (
	// RandomPickerFactory creates pickers that picks a connections at random.
	RandomPickerFactory PickerFactory = &randomPickerFactory{}
)

type randomPickerFactory struct{}

func (r randomPickerFactory) New(_ balancer.Picker, allConns balancer.Conns) balancer.Picker {
	rnd := internal.NewLockedRand()
	return balancer.PickerFunc(func(*http.Request) (conn balancer.Conn, whenDone func(), err error) {
		return allConns.Get(rnd.Intn(allConns.Len())), nil, nil
	})
}
