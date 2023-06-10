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
)

//nolint:gochecknoglobals
var (
	// ChooseFirstFactory returns very dumb pickers that always
	// pick the same connection.
	ChooseFirstFactory Factory = chooseFirstFactory{}
)

// Picker implements connection selection. For a given request, it returns
// the connection to use. It also returns a callback that, if non-nil, will
// be invoked when the operation is complete. (This happens when the HTTP
// response is returned and its body fully consumed or closed.) Such a
// callback can be used, for example, to track the number of active requests
// for a least-loaded implementation.
type Picker interface {
	Pick(req *http.Request) (conn conn.Conn, whenDone func(), err error)
}

// Factory creates new Picker instances.
type Factory interface {
	// New creates a new picker that will select a connection from the given
	// set.
	//
	// The previous picker is provided so that successive "generations" of
	// pickers can share state. In fact, it is entirely acceptable for this
	// function to *mutate* the previous picker and return it, instead of
	// returning a separate instance. This flexibility is useful for stateful
	// algorithms like least-loaded, where tracking the number of active
	// operations per connection needs to persist from one generation to the
	// next. Note that the previous picker may still be in use, concurrently,
	// while the factory is creating the new one (or modifying the previous
	// one), and even for some small amount of time after this method returns.
	// Also, operations might be started with the previous picker but not
	// completed until after the new picker is put in use. Picker factory
	// implementations need to use care for thread-safety when pickers need
	// to share state.
	//
	// This method will never be called with an empty set of connections. There
	// will always be at least one connection.
	New(prev Picker, allConns conn.Connections) Picker
}

// ErrorPicker returns a picker that always fails with the given error.
func ErrorPicker(err error) Picker {
	return pickerFunc(func(*http.Request) (conn.Conn, func(), error) {
		return nil, nil, err
	})
}

type pickerFunc func(*http.Request) (conn conn.Conn, whenDone func(), err error)

func (f pickerFunc) Pick(req *http.Request) (conn conn.Conn, whenDone func(), err error) {
	return f(req)
}

type chooseFirstFactory struct{}

func (c chooseFirstFactory) New(_ Picker, allConns conn.Connections) Picker {
	choice := allConns.Get(0)
	return pickerFunc(func(*http.Request) (conn.Conn, func(), error) {
		return choice, nil, nil
	})
}
