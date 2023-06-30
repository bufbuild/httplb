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
// # Type-Safe Attributes
//
// This package also contains the type Attrs, which is a container
// for type-safe custom attributes. This can be used to add custom
// metadata to a resolved address. Custom attributes are declared using
// [NewAttrKey] to create a strongly-typed key. The values can then be
// defined using the key's Value method.
//
// The following example declares two custom attributes, a floating point
// "weight", and a string "geographic region". It then constructs a new
// resolved Address that has values for each of them.
//
//	var (
//		Weight           = resolver.NewAttrKey[float64]()
//		GeographicRegion = resolver.NewAttrKey[string]()
//
//		Address = resolver.Address{
//			HostPort:   "111.222.123.234:5432",
//			Attrs: resolver.NewAttrs(
//				Weight.Value(1.25),
//				GeographicRegion.Value("us-east1"),
//			),
//		}
//	)
//
// Custom Resolver implementations can attach any kind of metadata to an
// address this way. This can be combined with a [custom picker] that uses
// the metadata, which can access the properties in a type-safe way using
// the [AttrValue] function.
//
// Such metadata can be used to implement regional affinity or to implement
// a weighted round-robin or random selection strategy (where a weight could
// be used to send more traffic to an address that has more available
// resources, such as more compute, memory, or network bandwidth).
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
