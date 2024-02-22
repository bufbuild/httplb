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

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/picker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPowerOfTwoPicker(t *testing.T) {
	t.Parallel()

	// TODO: when possible, need to test with multiple connections
	// need to ensure that order repeats as expected

	dummyConn := conn.Conn(nil)
	pick := picker.NewPowerOfTwo(nil, dummyConns{[]conn.Conn{dummyConn}})
	connection, _, err := pick.Pick(&http.Request{})
	require.NoError(t, err)
	assert.Equal(t, dummyConn, connection)

	// TODO: test (whitebox?) that state is retained between pickers

	pick = picker.NewPowerOfTwo(pick, dummyConns{[]conn.Conn{dummyConn}})
	connection, _, err = pick.Pick(&http.Request{})
	require.NoError(t, err)
	assert.Equal(t, dummyConn, connection)
}
