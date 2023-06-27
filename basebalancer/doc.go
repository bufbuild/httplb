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

// Package basebalancer includes a default implementation of the [Balancer] interface
// which can be constructed using the NewFactory function. This implementation is
// itself heavily customizable and is meant to be the base for custom load
// balancer policies. There may be some sophisticated policies that cannot be
// adequately implemented this way, in which case the [Balancer] interface must
// be implemented directly. But the base implementation is very flexible and
// extensible via options provided to NewFactory.
//
// The base implementation decomposes the above concerns into a few different
// abstractions, all of which can be plugged with user-provided implementations.
//
//   - Connection Management: The responsibility of deciding how many connections
//     to create and to which resolved addresses is delegated to a ConnManager.
//     The ConnManagerFactory returned from NewFactory can be configured with a
//     ConnManagerFactory which is used to create a ConnManager for each [Balancer].
//   - Health Checking: How to check the health of addresses is delegated to a
//     HealthChecker. A checker reports back the results, telling the balancer which
//     connections are healthy, degraded, or unknown. The job of deciding which
//     connections are acceptable for use is delegated to a UsabilityOracle.
//     The oracle may decide that only healthy connections are acceptable, or it
//     may have a strategy to allow other connections to be used when none are
//     healthy.
//   - Picking: The job of picking a connection for a request is delegated to a
//     [Picker]. The balancer constructs a new picker every time the set of usable
//     connections changes. The ConnManagerFactory returned from NewFactory can be configured
//     with a picker.Factory, which is used to create each new picker.
//
// So the three things that can be implemented to customize the behavior of the default
// balancer implementation are a ConnManagerFactory, a HealthChecker, and a
// picker.Factory. This module provides default implementations of each of these, in
// the corresponding sub-package.
//
// Without any customization, the default balancer uses the following:
//   - A naive connection manager that simply creates connections to every resolved
//     address. Large scale workloads should customize this to use subsetting.
//   - No health checking. All connections will remain in an "unknown" health state.
//     All connections will be considered usable. Environments where the name resolver
//     cannot filter addresses based on backend readiness should override this by
//     providing a checker and a usability oracle.
//   - Round-robin picking. This picker will send requests to addresses with a perfectly
//     uniform distribution.
//
// Each of these aspects can be customized independently.
//
// # Health Checking
//
// The HealthChecker interface is very general and allows health-checking
// strategies of many shapes, including those that might consult separate
// ("look aside") data sources for health or even those that use a streaming
// RPC to have results pushed from the server. This package also includes a
// default implementation that just does periodic polling using a given
// [HealthProber]. The prober can use the actual connection being checked to send
// an HTTP request to the address and examine the response.
//
// This package also includes a default implementation of UsabilityOracle
// that allows for simple threshold-based selection (i.e. all connections
// that are healthy are usable; others are not) or a preference-based
// selection (e.g. all connections in healthy, unknown, or degraded state
// are usable, but prefer ones in healthy state).
//
// # Picker Implementations
//
// This package includes numerous implementations of PickerFactory. These
// factories return pickers that implement numerous load balancer picking
// algorithms, including round-robin, random, least-loaded, and others.
//
// None of the provided implementations in this package make use of
// custom metadata ([Attrs]) for an address. But custom [Picker]
// implementations could, for example to prefer backends in clusters
// that are geographically closer, or to implement custom affinity
// policies, or even to implement weighted selection algorithms in
// the face of heterogeneous backends (where the name resolution/service
// discovery system has information about a backend's capacity).
//
// # Possible "Recipes"
//
// The bullets below describe several possible load balancer policies, other than what
// is already supported by default implementations in this module, and how they can be
// implemented.
//
//   - [Uber's Dynamic Subsetting]: This is where the client consults a separate source to
//     get realtime information on the total load of a service. It can use this information
//     along with its own knowledge of the load it has sent to the service to dynamically
//     resize the subset of addresses it uses. So clients that send a larger portion of
//     the request volume will connect to a wider variety of backends. Clients that send a
//     smaller portion will use fewer addresses.
//
//     This can be implemented via a custom decorator for a [Resolver] that changes the size
//     of the emitted subset of addresses as it gets information about load.
//
//   - [Twitter's Deterministic Aperture]: This is an approach to subsetting that results
//     in more even distribution than random or consistent hashing. It requires that the
//     client workload know the number of other clients, so they can deterministically
//     arrange themselves in a ring to decide how their location maps to the locations
//     of backend addresses. They also use the ring coordinates and aperture size to
//     compute weights for some of the addresses. The picker then considers those weights
//     when randomly picking two hosts, and then using the one with fewer outstanding
//     requests.
//
//     This is very similar to the rendezvous-hashing [Resolver] decorator combined with
//     a power-of-two [Picker], but it also needs to know the number of client processes.
//     Therefore this could be implemented as a custom [Resolver] decorator that shares
//     state with a custom picker factory, so the picker considers the right weights.
//
//   - [Layer-4 Load Balancers]: When load balancers are used, the name resolver typically
//     provides just a single address: a "virtual IP" for the load balancer. Often that is
//     sufficient -- the load balancer middle-box handles all of the messy details so that
//     the client doesn't have to. But for layer-4 load balancers, this is typically not
//     sufficient. A layer-4 load balancer only "balances" load during connection setup.
//     So when a new connection is established, it will bridge the connection to some
//     backend, using its own balancing policy. But that means that all requests to that
//     address are go to a single backend, resulting in no actual load balancing.
//
//     To counter this, a custom ConnManager implementation can handle creating multiple
//     connections, even in the face of just one resolved address. Multiple connections
//     are likely to be connected to different backends, so this alone would immediately
//     improve load distribution. But over time, physical connections can end up moving
//     and in pathological cases potentially all end up connected to few backends (or even
//     just one!). This could happen during rolling deploys, depending on how quick the
//     roll out is and how the load balancer middle-box handles health checking. So to
//     counter this kind of behavior, the ConnManager could periodically rotate one or
//     more connections. For example, every few minutes, it might close one existing
//     connection and open a new one to replace it. That way, even if some problem might
//     cause the connections to inadvertently "gang up" on a single backend, this periodic
//     rotation should undo that.
//
//   - [xDS]: This is Envoy's Dynamic Resource Discovery protocol. This is an extremely
//     broad system with many components. Together these components provide service discovery
//     (i.e. name resolution) as well as load balancing and routing services. The control
//     plane can push changes to many aspects of the network topology, making this a very
//     sophisticated system. Implementing xDS would require a custom implementation that
//     provides both the resolver.Factory and balancer.Factory interfaces.
//
// [Balancer]: https://pkg.go.dev/github.com/bufbuild/httplb/balancer#Balancer
// [Picker]: https://pkg.go.dev/github.com/bufbuild/httplb/picker#Picker
// [Resolver]: https://pkg.go.dev/github.com/bufbuild/httplb/resolver#Resolver
// [Attrs]: https://pkg.go.dev/github.com/bufbuild/httplb/resolver#Attrs
// [Uber's Dynamic Subsetting]: https://www.uber.com/blog/better-load-balancing-real-time-dynamic-subsetting/
// [Twitter's Deterministic Aperture]: https://blog.twitter.com/engineering/en_us/topics/infrastructure/2019/daperture-load-balancer
// [Layer-4 Load Balancers]: https://www.nginx.com/resources/glossary/layer-4-load-balancing/
// [xDS]: https://medium.com/@rajithacharith/introduction-to-envoys-dynamic-resource-discovery-xds-protocol-d340032a63b4
package basebalancer
