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

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
	"github.com/bufbuild/go-http-balancer/resolver"
)

// Factory is what creates Balancer instances. A single balancer acts on behalf of a
// single target (schema and host:port). So an HTTP client can be configured with a
// Factory, to create a Balancer for each target.
type Factory interface {
	// New creates a new balancer.
	//
	// As results come in from a corresponding resolver for the given scheme and
	// hostPort, the balancer's OnResolve method is called. It is the balancer's
	// job to use this information to decide what connections to create, by calling
	// the given ConnPool's NewConn and RemoveConn methods. It must also call the
	// ConnPool's UpdatePicker method whenever it should use a different picker.
	// The method must be called at least once, with the initial picker. Whether or
	// not subsequent calls are needed depend on the balancer implementation. For
	// example, a simple picker that is not coupled to the balancer implementation
	// and its internals may need to be rebuilt and updated every time connections
	// change state. But a picker that is highly coupled to the balancer
	// implementation may have all the information it needs by inspecting balancer
	// internals and thus never need to be recreated or updated.
	New(ctx context.Context, scheme, hostPort string, pool ConnPool) Balancer
}

// Balancer is the component that handles load balancing. It must make decisions
// about what connections to maintain as it gets updated addresses from a
// resolver. It also provides the Picker, which is what makes decisions about
// which connection to use for a given request.
type Balancer interface {
	// OnResolve reconciles the current state of connections with the given set
	// of addresses, by creating or removing connections using the ConnPool
	// provided when the balancer was created.
	OnResolve([]resolver.Address)
	// OnResolveError handles resolver errors. Its main responsibility should be
	// to create the initial picker for the associated ConnPool if it's the very
	// first thing received from a resolver. This unblocks requests on the pool,
	// so they can fail with a resolver error.
	OnResolveError(error)
	Close() error
}

var _ resolver.Receiver = Balancer(nil)

// ConnPool is the interface through which a Balancer interacts with the HTTP
// client, creating and destroying "connections" (leaf transports) as needed.
type ConnPool interface {
	// NewConn creates a new connection to the given address, with the given
	// attributes. Does not block for network connections to be established.
	// Second return value will be false if a connection could not be created
	// because the pool is closed or closing.
	NewConn(resolver.Address) (conn.Conn, bool)
	// RemoveConn removes the given connection. The balancer must arrange for
	// the picker to not return the given connection for any operations after
	// this is called. It may, for example, call UpdatePicker with a new picker
	// that doesn't even consider this connection before calling RemoveConn.
	// This returns false if the given connection was not present in the pool.
	RemoveConn(conn.Conn) bool
	// UpdatePicker updates the picker that the connection pool should use. The
	// picker is what selects a connection from the set of existing connections
	// (ones created with NewConn, excluding ones removed with RemoveConn). This
	// is what can implement algorithms like round-robin, least-loaded,
	// power-of-two, EWMA, etc.
	//
	// The balancer *must* call this at least once. The balancer should call it
	// as soon as possible after receiving results from a resolver. Operations
	// will block until the first time it is called.
	//
	// The given isWarm flag is used to decide if the pool is sufficiently warmed
	// up. It should only be set to true if the given picker is immediately usable.
	// (So if setting a picker that always return errors, for fail-fast conditions,
	// it should be set to false.)
	//
	// The concept of "warmed up" is therefore up to the balancer implementation.
	// It usually means some minimum number of connections are healthy and
	// available for use.
	UpdatePicker(picker picker.Picker, isWarm bool)
}
