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
// Though ConnPool has a Conns method, it is not guaranteed to be cheap (it
// may have to allocate a slice and compute a snapshot). That and the fact that
// the balancer must be thread-safe and thus have its own internal synchronization
// mean that balancer implementations should generally keep their own data
// structure to track the set of connections, updating it whenever they call the
// NewConn and RemoveConn methods on the ConnPool.
//
// [power-of-two]: https://www.nginx.com/blog/nginx-power-of-two-choices-load-balancing-algorithm/
// [EWMA]: https://linkerd.io/2016/03/16/beyond-round-robin-load-balancing-for-latency/
// [Picker]: https://pkg.go.dev/github.com/bufbuild/httplb/balancer/picker#Picker
package balancer
