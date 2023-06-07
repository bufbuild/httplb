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

package picker_test

import (
	"net/http"
	"testing"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/stretchr/testify/assert"
)

func TestRoundRobinPicker(t *testing.T) {
	t.Parallel()

	// TODO: when possible, it'd be good to test multiple connections to verify
	// that shuffling works and that we get round-robin behavior.

	dummyConn := conn.Conn(nil)
	allConns := dummyConns{[]conn.Conn{
		dummyConn,
	}}

	picker := picker.RoundRobinFactory.New(nil, allConns)
	conn, _, err := picker.Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, conn)
}
