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

// Package attrs provides a container for type-safe custom attributes.
// This can be used to add custom metadata to a resolved address. Custom
// attributes are declared using [NewKey] to create a strongly-typed key.
// The values can then be defined using the key's Value method.
//
// The following example declares two custom attributes, a floating point
// "weight", and a string "geographic region". It then constructs a new
// [resolved address] that has values for each of them.
//
//	var (
//		Weight           = attrs.NewKey[float64]()
//		GeographicRegion = attrs.NewKey[string]()
//
//		Address = resolver.Address{
//			HostPort:   "111.222.123.234:5432",
//			Attributes: attrs.New(
//				Weight.Value(1.25),
//				GeographicRegion.Value("us-east1"),
//			),
//		}
//	)
//
// [Custom resolvers] can attach any kind of metadata to an address this way.
// This can be combined with a [custom picker] that uses the metadata, which can
// access the properties in a type-safe way using the [GetValue] function.
//
// Such metadata can be used to implement regional affinity or to implement a
// weighted round-robin or random selection strategy (where a weight could be used
// to send more traffic to an address that has more available resources, such as
// more compute, memory, or network bandwidth).
//
// [resolved address]: https://pkg.go.dev/github.com/bufbuild/httplb/resolver#Address
// [Custom resolvers]: https://pkg.go.dev/github.com/bufbuild/httplb/resolver#Resolver
// [custom picker]: https://pkg.go.dev/github.com/bufbuild/httplb/balancer/picker#Picker
package attrs
