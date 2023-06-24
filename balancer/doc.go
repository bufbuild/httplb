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

// Package balancer provides pluggable load balancing functionality.
// "Load balancing" is a broad and complex topic that includes a few
// different concepts, described below.
//
// # Connection Management and Subsetting
//
// When connecting to a service backed by many servers, the first
// question a load balancing policy must answer is "how many
// connections does the client need" and "to what servers
// does this client make connections?"
//
// The simplest answer is "all of them", and this can be the right
// answer at small or medium scales. This means the client maintains
// connections to *every* server instance, and distribute requests
// across them all. But at large scales, this can create resource
// problems. The total number of connections is quadratic with the
// number of processes (technically N*M, where N is the number of
// client processes; M is the number of server processes). While a
// single connection is cheap, in terms of the resources used, when
// this scales up to millions of connections, it is extremely
// wasteful.
//
// So when a workload grows to a certain scale, we need a connection
// manager that picks a subset of server processes, instead of naively
// trying to connect to all of them.
//
// # Health Checking
//
// In many systems, the "name resolver" only knows the addresses of
// server processes, but does not know anything else about the lifecycle
// or current state of those processes.
//
// There are some systems where this is not the case. In Kubernetes, the
// workload can be configured with readiness checks, which are used to
// decide if a single process is ready to accept requests, and that
// process's address is only published to DNS when it is. If the process
// crashes, the Kubernetes system can remove that address from its DNS.
//
// But in most DNS systems, this is not the case: the addresses for a
// particular domain are more static. If they change, they change slowly
// as servers/VMs are provisioned or decommissioned, and not as the
// process changes states. So there can be resolve entries for which
// there is not an active server process -- like if the process is being
// restarted because a previous process crashed or a new version is being
// deployed.
//
// So the next question a load balancing policy may need to answer is
// "what servers are actually available?". The reason this is typically
// the second question, instead of the first, is the same quadratic
// connection issue described above: it would take a huge number of
// resources for every client to perform health checks for every server
// (at least for large scale workloads).
//
// # Picking a Connection
//
// Now that we have connections to some servers and know which ones are
// available to serve a request, the last question the load balancing
// policy must answer is "to what server do I send *this* request?"
//
// The simplest strategy is to just choose one at random. With enough
// volume and a reasonable RNG, this will result in fairly uniform
// load distribution. The next simplest strategy (and likely most
// common one) is round-robin, which will always result in perfectly
// uniform load distribution.
//
// There are other strategies which may try to optimize latency and/or
// handle heterogeneous servers (where some process have higher capacity
// for handling requests than others). These include local least-loaded
// (where the client sends the request using the connection that has the
// fewest number of outstanding requests), [power-of-two], and [EWMA].
//
// # Warming Up
//
// The balancer also decides when the set of ready connections is "warm",
// which means they are ready to start sending requests. A server program
// may want to wait at startup for all of its outbound connection pools
// to become warm to avoid any added latency for the first requests.
//
// # Re-resolving
//
// A balancer can ask the name resolver to re-resolve, which is a hint that
// it should immediately poll for addresses and provide them to the balancer.
// This can be done in response to, for example, too many addresses becoming
// unhealthy, which could be a sign that the addresses have changed and that
// the balancer is working with stale resolver results.
//
// # The Balancer Interface
//
// This package includes a [Balancer] interface. Things that implement
// this interface are responsible for handling the above concerns. The
// interface is very general, and likely complicated to implement from
// scratch. But it provides ultimate flexibility for a custom load
// balancing approach.
//
// This main interface has two concepts that it uses in order to interact
// with a transport:
//  1. [ConnPool]: This interface represents the underlying transport. The
//     balancer can tell it to create and remove connections, and it can
//     install a [Picker].
//  2. [Picker]: This interface is used by the transport to actually pick
//     a connection on which to send a particular request.
//
// The [Balancer] itself only exposes operations for receiving the results
// of name resolution (the OnResolve and OnResolveError methods) and for
// tearing down any background goroutines and releasing resources (the
// Close method). And it must be thread-safe: it is possible that the
// resolver results could come from different goroutines and the instruction
// to close from yet another goroutine.
//
// The typical implementation will handle the results of name resolution by
// reconciling the incoming list of addresses with its current set of connections.
// This process generally involves using the ConnPool to create new connections
// and remove no longer needed ones. If the balancer uses health checks, it may
// choose to start those at this point. Whenever the balancer's internal view
// of "usable" connections changes ("usable" meaning healthy and ready), it can
// create a new [Picker] and update the ConnPool. But it could instead create a
// [Picker] that shares some of the [Balancer]'s internal state, and only set
// it once. But the [Balancer] *must* call the ConnPool's UpdatePicker method
// at least once. The transport cannot be used without a picker.
//
// Though ConnPool has a GetConns method, it is not guaranteed to be cheap (it
// may have to allocate a slice and compute a snapshot). That and the fact that
// the balancer must be thread-safe and thus have its own internal synchronization
// mean that balancer implementations should generally keep their own data
// structure to track the set of connections, updating it whenever they call the
// NewConn and RemoveConn methods on the ConnPool.
//
// # Default Implementation
//
// This package includes a default implementation of the [Balancer] interface
// which can be constructed using the NewFactory function. This implementation is
// itself heavily customizable and is meant to be the base for custom load
// balancer policies. There may be some sophisticated policies that cannot be
// adequately implemented this way, in which case the [Balancer] interface must
// be implemented directly. But the default implementation is very flexible and
// extensible via options provided to NewFactory.
//
// The default implementation decomposes the above concerns into a few different
// abstractions, all of which can be plugged with user-provided implementations.
//
//   - Connection Management: The responsibility of deciding how many connections
//     to create and to which resolved addresses is delegated to a [ConnManager].
//     The Factory returned from NewFactory can be configured with a connmanager.Factory
//     which is used to create a [ConnManager] for each [Balancer].
//   - Health Checking: How to check the health of addresses is delegated to a
//     [Checker]. A checker reports back the results, telling the balancer which
//     connections are healthy, degraded, or unknown. The job of deciding which
//     connections are acceptable for use is delegated to a [UsabilityOracle].
//     The oracle may decide that only healthy connections are acceptable, or it
//     may have a strategy to allow other connections to be used when none are
//     healthy.
//   - Picking: The job of picking a connection for a request is delegated to a
//     [Picker]. The balancer constructs a new picker every time the set of usable
//     connections changes. The Factory returned from NewFactory can be configured
//     with a picker.Factory, which is used to create each new picker.
//
// So the three things that can be implemented to customize the behavior of the default
// balancer implementation are a connmanager.Factory, a healthchecker.Checker, and a
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
//     This can be implemented via a custom [ConnManager] that adjusts the subset size
//     as it receives data about the total load. It can then use existing subsetting
//     algorithms (like rendezvous-hashing) to choose the subset.
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
//     This is very similar to the provided subsetter and power-of-two picker. But it
//     needs to know the number of client processes, too. So this would likely need to be
//     a custom subsetter implementation that shares state with a custom picker factory,
//     so the picker considers the right weights. If the subsetting needs to be dynamic,
//     where the number of client processes changes over time, and the ring coordinates
//     need to update dynamically when that happens, this would require a custom
//     [ConnManager] implementation.
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
//     To counter this, a custom [ConnManager] implementation can handle creating multiple
//     connections, even in the face of just one resolved address. Multiple connections
//     are likely to be connected to different backends, so this alone would immediately
//     improve load distribution. But over time, physical connections can end up moving
//     and in pathological cases potentially all end up connected to few backends (or even
//     just one!). This could happen during rolling deploys, depending on how quick the
//     roll out is and how the load balancer middle-box handles health checking. So to
//     counter this kind of behavior, the [ConnManager] could periodically rotate one or
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
// [power-of-two]: https://www.nginx.com/blog/nginx-power-of-two-choices-load-balancing-algorithm/
// [EWMA]: https://linkerd.io/2016/03/16/beyond-round-robin-load-balancing-for-latency/
// [Picker]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/picker#Picker
// [ConnManager]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/connmanager#ConnManager
// [Checker]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/healthchecker#Checker
// [UsabilityOracle]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/healthchecker#UsabilityOracle
// [Uber's Dynamic Subsetting]: https://www.uber.com/blog/better-load-balancing-real-time-dynamic-subsetting/
// [Twitter's Deterministic Aperture]: https://blog.twitter.com/engineering/en_us/topics/infrastructure/2019/daperture-load-balancer
// [Layer-4 Load Balancers]: https://www.nginx.com/resources/glossary/layer-4-load-balancing/
// [xDS]: https://medium.com/@rajithacharith/introduction-to-envoys-dynamic-resource-discovery-xds-protocol-d340032a63b4
package balancer
