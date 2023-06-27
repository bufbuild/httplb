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

// Package conn provides the representation of a logical connection.
// A connection is the primitive used for load balancing by the
// [github.com/bufbuild/httplb/balancer] package. A single
// connection generally wraps a single transport (or [http.RoundTripper])
// to a single resolved address.
//
// This package also contains some basic collections of connections:
// a read-only array ([Connections]) and a [Set].
package conn

import (
	"context"
	"net/http"

	"github.com/bufbuild/httplb/attrs"
	"github.com/bufbuild/httplb/resolver"
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
	// Address is the address to which this value is connected.
	Address() resolver.Address
	// UpdateAttributes updates the connection's address to have the given attributes.
	UpdateAttributes(attributes attrs.Attributes)
	// Prewarm attempts to pre-warm this connection. Not all transports support
	// this operation. For those that do not, calling this function is a no-op.
	// It returns an error if the given context is cancelled or times out before
	// the connection can finish warming up.
	Prewarm(context.Context) error
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

// Equals returns true if s has the same connections as other.
func (s Set) Equals(other Set) bool {
	if len(s) != len(other) {
		return false
	}
	for c := range s {
		_, ok := other[c]
		if !ok {
			return false
		}
	}
	return true
}

// SetFromConnections converts a conn.Connections into a conn.Set.
func SetFromConnections(conns Connections) Set {
	set := Set{}
	for i := 0; i < conns.Len(); i++ {
		set[conns.Get(i)] = struct{}{}
	}
	return set
}

// SetFromSlice converts a []conn.Conn into a conn.Set.
func SetFromSlice(conns []Conn) Set {
	set := Set{}
	for _, c := range conns {
		set[c] = struct{}{}
	}
	return set
}

// ConnectionsFromSlice returns a conn.Connections that
// represents the given slice. Note that no defensive
// copy is made, so changes to the given slice's contents
// will be reflected in the returned value.
func ConnectionsFromSlice(conns []Conn) Connections {
	return connections(conns)
}

// ConnectionsFromSet converts a conn.Set to a conn.Connections.
func ConnectionsFromSet(set Set) Connections {
	sl := make([]Conn, 0, len(set))
	for c := range set {
		sl = append(sl, c)
	}
	return ConnectionsFromSlice(sl)
}

type connections []Conn

func (c connections) Len() int {
	return len(c)
}

func (c connections) Get(i int) Conn {
	return c[i]
}
