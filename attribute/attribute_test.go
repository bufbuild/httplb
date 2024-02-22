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

package attribute

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAttributes(t *testing.T) {
	t.Parallel()

	var testAttribute1 = NewKey[string]()
	var testAttribute2 = NewKey[string]()
	var testAttribute3 = NewKey[string]()

	attributes := NewValues(
		testAttribute1.Value("attr value 1"),
		testAttribute2.Value("attr value 2"),
		testAttribute1.Value("attr value 3"),
	)

	// Attr value overwritten by key re-appearing later
	value, ok := GetValue(attributes, testAttribute1)
	assert.True(t, ok)
	assert.Equal(t, "attr value 3", value)

	// Normal attribute value
	value, ok = GetValue(attributes, testAttribute2)
	assert.True(t, ok)
	assert.Equal(t, "attr value 2", value)

	// Attr key not set
	value, ok = GetValue(attributes, testAttribute3)
	assert.False(t, ok)
	assert.Equal(t, "", value)
}
func TestAttributeKeysUniquePointers(t *testing.T) {
	t.Parallel()

	// Tests that NewKey returns distinct pointers. (If Key
	// were inadvertently defined as an empty struct, then
	// NewKey would always return the same pointer. This
	// guards against such a mistake.)
	assert.NotSame(t, NewKey[string](), NewKey[string]()) //nolint:testifylint
}
