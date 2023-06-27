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
	"github.com/bufbuild/httplb/balancer/connmanager/subsetter"
	"github.com/bufbuild/httplb/resolver"
)

// NewFactory returns a new connection manager factory. If no options
// are given, ConnManager instances returned by the factory always
// create connections to every resolved address. See WithSubsetter to
// alter that behavior.
func NewFactory(opts ...NewFactoryOption) Factory {
	factory := &defaultConnManagerFactory{}
	for _, opt := range opts {
		opt.apply(factory)
	}
	factory.applyDefaults()
	return factory
}

// NewFactoryOption is an option for configuring the behavior of a Factory
// return from NewFactory.
type NewFactoryOption interface {
	apply(factory *defaultConnManagerFactory)
}

// WithSubsetter configures a ConnManager to use the given Subsetter to
// decide to what resolved addresses connections should be established.
func WithSubsetter(subsetter subsetter.Subsetter) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultConnManagerFactory) {
		factory.subsetter = subsetter
	})
}

type newFactoryOptionFunc func(factory *defaultConnManagerFactory)

func (o newFactoryOptionFunc) apply(factory *defaultConnManagerFactory) {
	o(factory)
}

type defaultConnManagerFactory struct {
	subsetter subsetter.Subsetter
}

func (d *defaultConnManagerFactory) applyDefaults() {
	if d.subsetter == nil {
		d.subsetter = subsetter.NoOp
	}
}

func (d *defaultConnManagerFactory) New(_ context.Context, _, _ string, updateConns ConnUpdater) ConnManager {
	return &defaultConnManager{
		subsetter: d.subsetter,
		updater:   updateConns,
		conns:     map[string][]conn.Conn{},
	}
}

type defaultConnManager struct {
	subsetter subsetter.Subsetter
	updater   ConnUpdater
	conns     map[string][]conn.Conn
}

func (d *defaultConnManager) ReconcileAddresses(addresses []resolver.Address) {
	// Balancer won't call this concurrently, so we don't need any synchronization.
	subset := d.subsetter.ComputeSubset(addresses)

	var newAddrs []resolver.Address
	var toRemove []conn.Conn
	// We allow subsetter to select the same address more than once. So
	// partition addresses by hostPort, to make reconciliation below easier.
	desired := make(map[string][]resolver.Address, len(subset))
	for _, addr := range subset {
		desired[addr.HostPort] = append(desired[addr.HostPort], addr)
	}
	remaining := make(map[string][]conn.Conn, len(d.conns))

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
