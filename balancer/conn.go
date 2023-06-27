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

package balancer

import (
	"context"
	"net/http"

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
	UpdateAttributes(attributes resolver.Attrs)
	// Prewarm attempts to pre-warm this connection. Not all transports support
	// this operation. For those that do not, calling this function is a no-op.
	// It returns an error if the given context is cancelled or times out before
	// the connection can finish warming up.
	Prewarm(context.Context) error
}

// Conns represents a read-only set of connections.
type Conns interface {
	// Len returns the total number of connections in the set.
	Len() int
	// Get returns the connection at index i.
	Get(i int) Conn
}

// ConnSet is a set of connections. Since connections are map keys in the
// underlying type, they are unique. Standard map iteration is used to
// enumerate the contents of the set.
type ConnSet map[Conn]struct{}

// Contains returns true if the set contains the given connection.
func (s ConnSet) Contains(c Conn) bool {
	_, ok := s[c]
	return ok
}

// Equals returns true if s has the same connections as other.
func (s ConnSet) Equals(other ConnSet) bool {
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

// ConnSetFromConns converts a conn.Conns into a conn.ConnSet.
func ConnSetFromConns(conns Conns) ConnSet {
	set := ConnSet{}
	for i := 0; i < conns.Len(); i++ {
		set[conns.Get(i)] = struct{}{}
	}
	return set
}

// ConnSetFromSlice converts a []conn.Conn into a conn.ConnSet.
func ConnSetFromSlice(conns []Conn) ConnSet {
	set := ConnSet{}
	for _, c := range conns {
		set[c] = struct{}{}
	}
	return set
}

// ConnsFromSlice returns a conn.Conns that
// represents the given slice. Note that no defensive
// copy is made, so changes to the given slice's contents
// will be reflected in the returned value.
func ConnsFromSlice(conns []Conn) Conns {
	return connections(conns)
}

// ConnsFromConnSet converts a conn.ConnSet to a conn.Conns.
func ConnsFromConnSet(set ConnSet) Conns {
	sl := make([]Conn, 0, len(set))
	for c := range set {
		sl = append(sl, c)
	}
	return ConnsFromSlice(sl)
}

type connections []Conn

func (c connections) Len() int {
	return len(c)
}

func (c connections) Get(i int) Conn {
	return c[i]
}
