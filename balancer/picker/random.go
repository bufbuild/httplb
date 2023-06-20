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

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/internal"
)

//nolint:gochecknoglobals
var (
	RandomFactory Factory = &randomFactory{}
)

type randomFactory struct{}

func (r randomFactory) New(_ Picker, allConns conn.Connections) Picker {
	rnd := internal.NewLockedRand()
	return pickerFunc(func(*http.Request) (conn conn.Conn, whenDone func(), err error) {
		return allConns.Get(rnd.Intn(allConns.Len())), nil, nil
	})
}
