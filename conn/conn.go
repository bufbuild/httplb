// Copyright 2023-2025 Buf Technologies, Inc.
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
// [github.com/bufbuild/httplb] package. A single connection generally
// wraps a single transport (or [http.RoundTripper]) to a single
// resolved address.
package conn

import (
	"context"
	"net/http"

	"github.com/bufbuild/httplb/attribute"
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
	UpdateAttributes(attributes attribute.Values)
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
