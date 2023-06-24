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

// Key is an attribute key. Applications should use NewKey to create
// a new key for each distinct attribute. The type T is the type of
// values this attribute can have.
type Key[T any] struct {
	// can't be empty or else pointers won't be distinct
	_ bool
}

// Value constructs a new Attribute value, which can be passed to [New].
func (k *Key[T]) Value(value T) Attribute {
	return Attribute{key: k, value: value}
}

func NewKey[T any]() *Key[T] {
	return new(Key[T])
}
