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
	"github.com/bufbuild/httplb/balancer"
)

// PickerFactory creates new Picker instances.
type PickerFactory interface {
	// New creates a new picker that will select a connection from the given
	// set.
	//
	// The previous picker is provided so that successive "generations" of
	// pickers can share state. In fact, it is entirely acceptable for this
	// function to *mutate* the previous picker and return it, instead of
	// returning a separate instance. This flexibility is useful for stateful
	// algorithms like least-loaded, where tracking the number of active
	// operations per connection needs to persist from one generation to the
	// next. Note that the previous picker may still be in use, concurrently,
	// while the factory is creating the new one (or modifying the previous
	// one), and even for some small amount of time after this method returns.
	// Also, operations might be started with the previous picker but not
	// completed until after the new picker is put in use. Picker factory
	// implementations need to use care for thread-safety when pickers need
	// to share state.
	//
	// This method will never be called with an empty set of connections. There
	// will always be at least one connection.
	New(prev balancer.Picker, allConns balancer.Conns) balancer.Picker
}
