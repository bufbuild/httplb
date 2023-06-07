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

	"github.com/bufbuild/go-http-balancer/balancer/connmanager"
	"github.com/bufbuild/go-http-balancer/balancer/healthchecker"
	"github.com/bufbuild/go-http-balancer/balancer/picker"
)

// NewFactory returns a new balancer factory. If no options are given,
// the default behavior is as follows:
// * No subsetting. Connections are created for all resolved addresses.
// * No health checks. All connections are considered usable.
// * Round-robin picker.
func NewFactory(opts ...NewFactoryOption) Factory {
	factory := &defaultBalancerFactory{}
	for _, opt := range opts {
		opt.apply(factory)
	}
	return factory
}

type NewFactoryOption interface {
	apply(balancer *defaultBalancerFactory)
}

// WithConnManager configures a balancer factory to use the given
// connmanager.Factory. It will be used to create a connmanager.ConnManager
// for each Balancer. The ConnManager is what decides what connections to
// create or remove.
func WithConnManager(connManager connmanager.Factory) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.connManager = connManager
	})
}

// WithPicker configures a balancer factory to use the given picker.Factory.
// A new picker will be created every time the set of usable connections
// changes for a given balancer.
func WithPicker(picker picker.Factory) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.picker = picker
	})
}

// WithHealthChecks configures a balancer factory to use the given health
// checker. This provides details about which resolved addresses are
// healthy or not. The given oracle is used to interpret the health check
// results and decide which connections are usable.
func WithHealthChecks(checker healthchecker.Checker, oracle healthchecker.UsabilityOracle) NewFactoryOption {
	return newFactoryOption(func(factory *defaultBalancerFactory) {
		factory.healthChecker = checker
		factory.usabilityOracle = oracle
	})
}

type newFactoryOption func(factory *defaultBalancerFactory)

func (o newFactoryOption) apply(factory *defaultBalancerFactory) {
	o(factory)
}

type defaultBalancerFactory struct {
	connManager     connmanager.Factory
	picker          picker.Factory
	healthChecker   healthchecker.Checker
	usabilityOracle healthchecker.UsabilityOracle
}

func (f *defaultBalancerFactory) New(_ context.Context, _, _ string, _ ConnPool) Balancer {
	// TODO: implement me!
	return nil
}
