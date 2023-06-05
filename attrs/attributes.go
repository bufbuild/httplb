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

package attrs

// An Attributes value contains a map of attribute keys to values.
type Attributes struct {
	attrs map[any]any
}

type Attribute struct {
	key, value any
}

// New creates a new Attributes object with the provided values applied.
//
// Use this function in tandem with [Value], like this:
//
//	var testKey = NewKey[string]()
//	...
//	attrs.New(attrs.Value(testKey, "test!"))
func New(values ...Attribute) Attributes {
	attrs := make(map[any]any)
	for _, attr := range values {
		attrs[attr.key] = attr.value
	}
	return Attributes{
		attrs: attrs,
	}
}

// Value constructs a new Attribute value, which can be passed to the [New].
func Value[T any](key *Key[T], value T) Attribute {
	return Attribute{key: key, value: value}
}

// GetValue retrieves a value from the attributes set. If the key is not
// present, the zero value and false will be returned instead.
func GetValue[T any](attrs Attributes, key *Key[T]) (value T, ok bool) {
	val, ok := attrs.attrs[key]
	if !ok {
		var zero T
		return zero, false
	}
	tval, ok := val.(T)
	return tval, ok
}
