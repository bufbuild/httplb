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

// Package attribute provides a type-safe container of custom attributes
// named Values. This can be used to add custom metadata to a resolved
// address. Custom attributes are declared using [NewKey] to create a
// strongly-typed key. The values can then be defined using the key's
// Value method.
//
// The following example declares two custom attributes, a floating point
// "weight", and a string "geographic region". It then constructs a new
// resolved Address that has values for each of them.
//
//	var (
//		Weight           = attribute.NewKey[float64]()
//		GeographicRegion = attribute.NewKey[string]()
//
//		Address = resolver.Address{
//			HostPort:   "111.222.123.234:5432",
//			Attributes: attribute.NewValues(
//				Weight.Value(1.25),
//				GeographicRegion.Value("us-east1"),
//			),
//		}
//	)
//
// Custom [Resolver] implementations can attach any kind of metadata
// to an address this way. This can be combined with a [custom picker]
// that uses the metadata, which can access the properties in a type-safe way
// using the [GetValue] function.
//
// Such metadata can be used to implement regional affinity or to implement
// a weighted round-robin or random selection strategy (where a weight could
// be used to send more traffic to an address that has more available
// resources, such as more compute, memory, or network bandwidth).
//
// [Resolver]: https://pkg.go.dev/github.com/bufbuild/httplb/resolver#Resolver
// [custom picker]: https://pkg.go.dev/github.com/bufbuild/httplb/picker#Picker
package attribute

// Values is a collection of type-safe custom metadata values.
// It contains a mapping of [Key] to value for any number of
// attribute keys.
type Values struct {
	data map[any]any
}

// NewValues creates a new Values object with the provided values.
//
// Use this function in tandem with [Key.Value], like this:
//
//	var testKey = attribute.NewKey[string]()
//	...
//	attribute.NewValues(testKey.Value("test"))
func NewValues(values ...Value) Values {
	data := make(map[any]any)
	for _, attr := range values {
		data[attr.key] = attr.value
	}
	return Values{
		data: data,
	}
}

// Key is an attribute key. Applications should use NewKey to create
// a new key for each distinct attribute. The type T is the type of
// values this attribute can have.
type Key[T any] struct {
	// can't be empty or else pointers won't be distinct
	_ bool
}

// NewKey returns a new key that can have values of type T. Each call
// to NewKey results in a distinct attribute key, even if multiple are
// created for the same type. (Keys are identified by their address.)
func NewKey[T any]() *Key[T] {
	return new(Key[T])
}

// Value constructs a new Attr value, which can be passed to [NewValues].
func (k *Key[T]) Value(value T) Value {
	return Value{key: k, value: value}
}

// Value is a single custom attribute, composed of a key and
// corresponding value.
type Value struct {
	key, value any
}

// GetValue retrieves a single value from the given Values. If the key is not
// present, the zero value and false will be returned instead.
func GetValue[T any](values Values, key *Key[T]) (value T, ok bool) {
	val, ok := values.data[key]
	if !ok {
		var zero T
		return zero, false
	}
	tval, ok := val.(T)
	return tval, ok
}
