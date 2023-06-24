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

// Package resolver provides functionality for custom name resolution.
// Name resolution is the process of resolving service or domain names
// into one or more addresses -- where an address is a host:port (and
// optionally custom metadata) of a server that provides the service.
//
// It contains core interfaces ([Factory], [Resolver]) that can be
// implemented to create a custom name resolution strategy. The interface
// is general enough that it can support any form of resolver, including
// ones that are backed by push mechanisms (like "watching" nodes in
// ZooKeeper ord etcd or "watching" resources in Kubernetes).
//
// It also contains a default implementation that uses periodic polling
// via a [ResolveProber]. The one prober implementation included uses
// DNS to query addresses for a name using a [net.Resolver].
//
// To create a new resolver implementation that uses periodic polling,
// you need only implement the [ResolveProber] interface and use your
// implementation with NewPollingResolverFactory. To create a more
// sophisticated implementation, you would need to implement the
// [Factory] interface, which creates a new [Resolver] for each service
// or domain that a client needs.
package resolver
