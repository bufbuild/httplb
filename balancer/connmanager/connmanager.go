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

package connmanager

import (
	"context"

	"github.com/bufbuild/httplb/balancer/conn"
	"github.com/bufbuild/httplb/resolver"
)

// Factory is used to build ConnManager instances.
type Factory interface {
	// New creates a new ConnManager for the given scheme and host:port.
	// It should release any resources (including stopping goroutines) if either
	// the given context is cancelled or its Close() method is called.
	//
	// The ConnManager may use the given functions to create and remove connections.
	New(ctx context.Context, scheme, hostPort string, updateConns ConnUpdater) ConnManager
}

// ConnUpdater is a function that adds connections for the given newAddrs, removes
// the given removeConns, and returns the newly created connections. If the
// associated connection pool is closed or closing, this may return fewer new
// connections than requested (likely zero).
type ConnUpdater func(newAddrs []resolver.Address, removeConns []conn.Conn) (added []conn.Conn)

// ConnManager encapsulates part of the logic of a balancer.Balancer. Its
// responsibility is deciding what connections to create or remove as results
// come in from a resolver. It must decide both how many connections to maintain
// and to which resolved addresses connections will be established.
type ConnManager interface {
	// ReconcileAddresses is called when new results are available from a resolver.
	// The ConnManager should create and remove connections to keep the current set
	// of connections reconciled with the given latest results.
	//
	// Balancers created using a factory from balancer.NewFactory will never call
	// this concurrently. So if the ConnManager implementation does not need to
	// create other goroutines, the data structures it uses in this method do not
	// need to be thread-safe.
	//
	// The given slice and its backing array are not shared. So it is okay for
	// the connection manager to mutate it.
	ReconcileAddresses([]resolver.Address)
	// Close releases all resources (including stopping background goroutines if
	// any). All resources should be freed when this method returns.
	Close() error
}
