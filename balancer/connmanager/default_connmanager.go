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

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/resolver"
)

//nolint:gochecknoglobals
var (
	// NoOpSubsetter doesn't actually do subsetting and instead
	// creates connections to every resolved address.
	NoOpSubsetter Subsetter = subsetterFunc(func(addrs []resolver.Address) []resolver.Address {
		return addrs
	})
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
func WithSubsetter(subsetter Subsetter) NewFactoryOption {
	return newFactoryOptionFunc(func(factory *defaultConnManagerFactory) {
		factory.subsetter = subsetter
	})
}

type Subsetter interface {
	// ComputeSubset returns a static subset of the given addresses. It is
	// allowed to return duplicates, if it wants to return more addresses than
	// are actually given.
	ComputeSubset([]resolver.Address) []resolver.Address
}

type newFactoryOptionFunc func(factory *defaultConnManagerFactory)

func (o newFactoryOptionFunc) apply(factory *defaultConnManagerFactory) {
	o(factory)
}

type subsetterFunc func([]resolver.Address) []resolver.Address

func (f subsetterFunc) ComputeSubset(addrs []resolver.Address) []resolver.Address {
	return f(addrs)
}

type defaultConnManagerFactory struct {
	subsetter Subsetter
}

func (d *defaultConnManagerFactory) applyDefaults() {
	if d.subsetter == nil {
		d.subsetter = NoOpSubsetter
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
	subsetter Subsetter
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
	desired := make(map[string][]resolver.Address, len(addresses))
	for _, addr := range subset {
		desired[addr.HostPort] = append(desired[addr.HostPort], addr)
	}

	for hostPort, got := range d.conns {
		want := desired[hostPort]
		if len(want) > len(got) {
			// sync attributes of existing connection with new values from resolver
			for i := range got {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// and schedule new connections to be created
			newAddrs = append(newAddrs, want[len(got):]...)
		} else {
			// sync attributes of existing connection with new values from resolver
			for i := range want {
				got[i].UpdateAttributes(want[i].Attributes)
			}
			// schedule extra connections to be removed
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

	d.updater(newAddrs, toRemove)
}

func (d *defaultConnManager) Close() error {
	return nil
}
