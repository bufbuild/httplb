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

package basebalancer

import (
	"context"

	"github.com/bufbuild/httplb/balancer"
	"github.com/bufbuild/httplb/resolver"
)

// ConnManagerFactory is used to build ConnManager instances.
type ConnManagerFactory interface {
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
type ConnUpdater func(newAddrs []resolver.Address, removeConns []balancer.Conn) (added []balancer.Conn)

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

// NewConnManagerFactory returns a new connection manager factory. If no options
// are given, ConnManager instances returned by the factory always
// create connections to every resolved address. See WithSubsetter to
// alter that behavior.
func NewConnManagerFactory() ConnManagerFactory {
	return &defaultConnManagerFactory{}
}

type defaultConnManagerFactory struct{}

func (d *defaultConnManagerFactory) New(_ context.Context, _, _ string, updateConns ConnUpdater) ConnManager {
	return &defaultConnManager{
		updater: updateConns,
		conns:   map[string][]balancer.Conn{},
	}
}

type defaultConnManager struct {
	updater ConnUpdater
	conns   map[string][]balancer.Conn
}

func (d *defaultConnManager) ReconcileAddresses(addresses []resolver.Address) {
	// Balancer won't call this concurrently, so we don't need any synchronization.

	var newAddrs []resolver.Address
	var toRemove []balancer.Conn
	// We allow subsetter to select the same address more than once. So
	// partition addresses by hostPort, to make reconciliation below easier.
	desired := make(map[string][]resolver.Address, len(addresses))
	for _, addr := range addresses {
		desired[addr.HostPort] = append(desired[addr.HostPort], addr)
	}
	remaining := make(map[string][]balancer.Conn, len(d.conns))

	for hostPort, got := range d.conns {
		want := desired[hostPort]
		if len(want) > len(got) {
			// sync attributes of existing connection with new values from resolver
			for i := range got {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// and schedule new connections to be created
			remaining[hostPort] = got
			newAddrs = append(newAddrs, want[len(got):]...)
		} else {
			// sync attributes of existing connection with new values from resolver
			for i := range want {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// schedule extra connections to be removed
			remaining[hostPort] = got[:len(want)]
			toRemove = append(toRemove, got[len(want):]...)
		}
	}
	for hostPort, want := range desired {
		if _, ok := d.conns[hostPort]; ok {
			// already checked in loop above
			continue
		}
		newAddrs = append(newAddrs, want...)
	}

	newConns := d.updater(newAddrs, toRemove)
	// add newConns to remaining to compute new set of connections
	for _, c := range newConns {
		hostPort := c.Address().HostPort
		remaining[hostPort] = append(remaining[hostPort], c)
	}
	d.conns = remaining
}

func (d *defaultConnManager) Close() error {
	return nil
}
