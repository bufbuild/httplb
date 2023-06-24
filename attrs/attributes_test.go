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

package attrs_test

import (
	"testing"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/stretchr/testify/assert"
)

func TestAttributes(t *testing.T) {
	t.Parallel()

	var testAttribute1 = attrs.NewKey[string]()
	var testAttribute2 = attrs.NewKey[string]()
	var testAttribute3 = attrs.NewKey[string]()

	attributes := attrs.New(
		testAttribute1.Value("attr value 1"),
		testAttribute2.Value("attr value 2"),
		testAttribute1.Value("attr value 3"),
	)

	// Attribute value overwritten by key re-appearing later
	value, ok := attrs.GetValue(attributes, testAttribute1)
	assert.True(t, ok)
	assert.Equal(t, "attr value 3", value)

	// Normal attribute value
	value, ok = attrs.GetValue(attributes, testAttribute2)
	assert.True(t, ok)
	assert.Equal(t, "attr value 2", value)

	// Attribute key not set
	value, ok = attrs.GetValue(attributes, testAttribute3)
	assert.False(t, ok)
	assert.Equal(t, "", value)
}
