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

package resolver

// Attrs is a collection of type-safe custom metadata.
// It contains a mapping of [AttrKey] to value for any number of
// attribute keys.
type Attrs struct {
	attrs map[any]any
}

// Attr is a single custom attribute, defined by a
// key and corresponding value.
type Attr struct {
	key, value any
}

// NewAttrs creates a new Attrs object with the provided values applied.
//
// Use this function in tandem with [Value], like this:
//
//	var testKey = resolver.NewAttrKey[string]()
//	...
//	resolver.NewAttrs(testKey.Value("test!"))
func NewAttrs(values ...Attr) Attrs {
	attrs := make(map[any]any)
	for _, attr := range values {
		attrs[attr.key] = attr.value
	}
	return Attrs{
		attrs: attrs,
	}
}

// AttrValue retrieves a value from the attributes set. If the key is not
// present, the zero value and false will be returned instead.
func AttrValue[T any](attrs Attrs, key *AttrKey[T]) (value T, ok bool) {
	val, ok := attrs.attrs[key]
	if !ok {
		var zero T
		return zero, false
	}
	tval, ok := val.(T)
	return tval, ok
}
