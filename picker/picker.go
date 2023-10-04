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

	"github.com/bufbuild/httplb/conn"
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
