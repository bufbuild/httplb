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
	// RoundTrip sends a request using this connection. This is the same as
	// [http.RoundTripper]'s method of the same name except that it accepts
	// a callback that, if non-nil, is invoked when the request is completed.
	RoundTrip(req *http.Request, whenDone func()) (*http.Response, error)
	// Scheme returns the URL scheme to use with this connection.
	Scheme() string
	// Address is the address used by this connection.
	Address() resolver.Address
	// UpdateAttributes updates the connection's address to have the given attributes.
	UpdateAttributes(attributes attrs.Attributes)
}

// Connections represents a read-only set of connections.
type Connections interface {
	// Len returns the total number of connections in the set.
	Len() int
	// Get returns the connection at index i.
	Get(i int) Conn
}

// Set is a set of connections. Since connections are map keys in the
// underlying type, they are unique. Standard map iteration is used to
// enumerate the contents of the set.
type Set map[Conn]struct{}

// Contains returns true if the set contains the given connection.
func (s Set) Contains(c Conn) bool {
	_, ok := s[c]
	return ok
}

// ConnectionsToSet converts a conn.Connections into a conn.Set.
func ConnectionsToSet(conns Connections) Set {
	set := Set{}
	for i := 0; i < conns.Len(); i++ {
		set[conns.Get(i)] = struct{}{}
	}
	return set
}

// SliceToSet converts a []conn.Conn into a conn.Set.
func SliceToSet(conns []Conn) Set {
	set := Set{}
	for _, c := range conns {
		set[c] = struct{}{}
	}
	return set
}
