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

type dummyConns struct{ conns []balancer.Conn }

func (c dummyConns) Len() int                { return len(c.conns) }
func (c dummyConns) Get(i int) balancer.Conn { return c.conns[i] }

func TestRandomPicker(t *testing.T) {
	t.Parallel()

	dummyConn := balancer.Conn(nil)
	conn, _, err := RandomPickerFactory.New(nil, dummyConns{[]balancer.Conn{dummyConn}}).Pick(&http.Request{})
	assert.NoError(t, err)
	assert.Equal(t, dummyConn, conn)
}
