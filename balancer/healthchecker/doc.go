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

// Package healthchecker provides pluggable health-checking, used by
// the default [Balancer] implementation.
//
// This package defines the core types [Checker], which creates health
// check processes for connections, and [UsabilityOracle], which is how
// a [Balancer] interprets the health check results and decides which
// connections are actually usable. The package also defines the interface
// [HealthTracker], which is how a health check process communicates
// results back to a [Balancer]. (This interface is provided by the
// [Balancer] when health check processes are created.)
//
// The [Checker] interface is very general and allows health-checking
// strategies of many shapes, including those that might consult separate
// ("look aside") data sources for health or even those that use a streaming
// RPC to have results pushed from the server. This package also includes a
// default implementation that just does periodic polling using a given
// [Prober]. The prober can use the actual connection being checked to send
// an HTTP request to the address and examine the response.
//
// This package also includes a default implementation of UsabilityOracle
// that allows for simple threshold-based selection (i.e. all connections
// that are healthy are usable; others are not) or a preference-based
// selection (i.e. all connections in healthy, unknown, or degraded state
// are usable, but prefer ones in healthy state).
//
// [Balancer]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer#Balancer
package healthchecker
