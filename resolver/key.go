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

// AttrKey is an attribute key. Applications should use NewAttrKey to create
// a new key for each distinct attribute. The type T is the type of
// values this attribute can have.
type AttrKey[T any] struct {
	// can't be empty or else pointers won't be distinct
	_ bool
}

// Value constructs a new Attr value, which can be passed to [NewAttrs].
func (k *AttrKey[T]) Value(value T) Attr {
	return Attr{key: k, value: value}
}

func NewAttrKey[T any]() *AttrKey[T] {
	return new(AttrKey[T])
}
