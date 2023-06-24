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

// Package httplb provides http.Client instances that are suitable
// for use for server-to-server communications, like RPC. This adds features
// on top of the standard net/http library for name/address resolution,
// health checking, connection management and subsetting, and load balancing.
// It also provides more suitable defaults and much simpler support for
// HTTP/2 over plaintext.
//
// To create a new client use the [NewClient] function. This function
// accepts numerous options, many for configuring the behavior of the
// underlying transports. It also provides options for using a custom
// [name resolver] or a custom [load balancer].
//
// The returned client has a notion of being "warmed up", via the [Prewarm]
// function. This function eagerly resolves names, issues health checks
// (if so configured), and awaits a minimum number of ready connections.
// The [WithBackendTarget] option is used to tell the client which
// connections need to be warmed up.
//
// The returned client also has a notion of "closing", via the [Close]
// function. This step will wait for outstanding requests to complete and
// then close all connections and also teardown any other goroutines that
// it may have started to perform name resolution and health checking.
// The client cannot be used after it has been closed.
//
// # Default Behavior
//
// Without any options, the returned client behaves differently than
// http.DefaultClient in the following key ways:
//
//  1. The client will re-resolve addresses in DNS every 5 minutes.
//     The default client does not re-resolve predictably.
//
//  2. The client will route requests in a round-robin fashion to all
//     addresses returned by the DNS system (both A and AAAA records).
//     even with HTTP/2. The default client will use only a single
//     connection if it can, even if DNS resolves many addresses. With
//     HTTP 1.1, it will create additional connections to handle multiple
//     concurrent requests (since an HTTP 1.1 connection can only service
//     one request at a time). But with HTTP/2, it likely will *never*
//     use additional connections: it only creates another connection if
//     the concurrency limit exceeds the server's "max concurrent streams"
//     (which the server provides when the connection is initially
//     established and is typically on the order of 100).
//
// The above options alone should make the client distribute load to
// backends in a much more appropriate way, especially when using HTTP/2.
// But the real power of a client returned by this package is that it
// can be customized with different name resolution and load balancing
// strategies, via the [WithResolver] and [WithBalancer] options,
// including support for health checking.
//
// # Transport Architecture
//
// The clients created by this function use a transport implementation
// that is actually a hierarchy of three layers:
//
//  1. The "root" of the hierarchy (which is the actual Transport field
//     value of an http.Client returned by NewClient) serves as a collection
//     of transport pools, grouped by URL scheme and hostname:port. For a
//     given request, it examines the URL's scheme and hostname:port to
//     decide which transport pool should handle the request.
//  2. The next level of the hierarchy is the transport pool. A transport
//     pool manages multiple transports that all correspond to the same
//     URL scheme and hostname:port. This is the layer in which most of
//     the features are implemented, like name resolution and load balancing.
//     Each transport pool manages a pool of "leaf" transports, each to a
//     potentially different resolved address.
//  3. The bottom of the hierarchy, the "leaf" transports, are just
//     http.RoundTripper implementations that each represent a single,
//     logical connection to a single resolved address. A leaf transport
//     could actually consist of than one *physical* connection, like
//     when using HTTP 1.1 and multiple active requests are made
//     to the same leaf transport. This level of transport is often an
//     *http.Transport, that handles physical connections to the remote
//     host and implements the actual HTTP protocol.
//
// [name resolver]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/resolver
// [load balancer]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer
package httplb
