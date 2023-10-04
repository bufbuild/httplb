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
// It contains the core interface ([Resolver]) that can be implemented
// to create a custom name resolution strategy. The interface is general
// enough that it can support any form of resolver, including ones that
// are backed by push mechanisms (like "watching" nodes in ZooKeeper or
// etcd or "watching" resources in Kubernetes).
//
// # Default Implementation
//
// This package contains a default implementation that uses periodic
// polling via a [ResolveProber]. The one prober implementation included
// uses DNS to query addresses for a name using a [net.Resolver].
//
// To create a new resolver implementation that uses periodic polling,
// you need only implement the [ResolveProber] interface and use your
// implementation with NewPollingResolver. To create a more sophisticated
// implementation, you would need to implement the [Resolver] interface,
// which creates a new task for each service or domain that a client needs.
//
// # Subsetting
//
// Subsetting can be achieved via a Receiver decorator that intercepts the
// addresses, selects a subset, and sends the subset to the underlying Receiver.
//
// This decorator pattern is implemented by RendezvousHashSubsetter, which
// is a Resolver decorator. When the resolver's New method is called, it wraps
// the given Receiver with a decorator that computes the subset using
// rendezvous-hashing. It then passes that decorated Receiver to the underlying
// Resolver.
//
// For decorators that use resources (such as background goroutines) and need
// to be explicitly shut down to release those resources, the Resolver interface
// should be decorated, just as is done by RendezvousHashSubsetter. That way,
// any tasks created by the Resolver can also be decorated, in order to hook into
// their Close method. When the task for a corresponding Receiver is closed,
// the decorator should shut down and release any resources.
package resolver
