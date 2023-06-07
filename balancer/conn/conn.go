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

package conn

import (
	"net/http"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/resolver"
)

// Conn represents a connection to a resolved address. It is a *logical*
// connection. It may actually be represented by zero or more physical
// connections (i.e. sockets).
type Conn interface {
	// Address is the address used by this connection.
	Address() resolver.Address
	// UpdateAttributes updates the connection's address to have the given attributes.
	UpdateAttributes(attributes attrs.Attributes)
	// RoundTripper allows sending an HTTP request on this connection.
	RoundTripper() http.RoundTripper
}

// Connections represents a read-only set of connections.
type Connections interface {
	// Len returns the total number of connections in the set.
	Len() int
	// Get returns the connection at index i.
	Get(i int) Conn
}
