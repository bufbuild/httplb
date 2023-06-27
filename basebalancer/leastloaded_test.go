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

package basebalancer_test

import (
	"net/http"
	"testing"

	"github.com/bufbuild/httplb/balancer"
	. "github.com/bufbuild/httplb/basebalancer"
	"github.com/stretchr/testify/assert"
)

func TestLeastLoadedRoundRobinPicker(t *testing.T) {
	t.Parallel()

	// TODO: when possible, need to test with multiple connections
	// need to ensure that order repeats as expected

	dummyConn := balancer.Conn(nil)
	pick := LeastLoadedRoundRobinPickerFactory.New(nil, dummyConns{[]balancer.Conn{dummyConn}})
	connection, _, err := pick.Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, connection)

	// TODO: test (whitebox?) that state is retained between pickers

	pick = LeastLoadedRoundRobinPickerFactory.New(pick, dummyConns{[]balancer.Conn{dummyConn}})
	connection, _, err = pick.Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, connection)
}

func TestLeastLoadedRandomPicker(t *testing.T) {
	t.Parallel()

	// TODO: when possible, need to test with multiple connections

	dummyConn := balancer.Conn(nil)
	pick := LeastLoadedRandomPickerFactory.New(nil, dummyConns{[]balancer.Conn{dummyConn}})
	connection, _, err := pick.Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, connection)

	// TODO: test (whitebox?) that state is retained between pickers

	pick = LeastLoadedRandomPickerFactory.New(pick, dummyConns{[]balancer.Conn{dummyConn}})
	connection, _, err = pick.Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, connection)
}
