// Copyright 2023-2025 Buf Technologies, Inc.
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

// Package health provides pluggable health checking for an httplb.Client.
//
// This package defines the core types [Checker], which creates health
// check processes for connections. The package also defines the interface
// [Tracker], which is how a health check process communicates results
// back to a httplb.Client.
//
// The [Checker] interface is very general and allows health-checking
// strategies of many shapes, including those that might consult separate
// ("look aside") data sources for health or even those that use a streaming
// RPC to have results pushed from a server. This package also includes a
// default implementation that just does periodic polling using a given
// [Prober]. The prober can use the actual connection being checked to send
// an HTTP request to the address and examine the response.
package health
