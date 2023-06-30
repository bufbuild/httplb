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

package internal_test

import (
	"hash/fnv"
	"testing"

	"github.com/bufbuild/httplb/internal"
	"github.com/stretchr/testify/assert"
)

func TestMurmurHash3(t *testing.T) {
	t.Parallel()

	testMurmurHash3Input(t, []byte{}, 0, 0)
	testMurmurHash3Input(t, []byte{}, 1, 0x514E28B7)
	testMurmurHash3Input(t, []byte{}, 0xFFFFFFFF, 0x81F16F39)
	testMurmurHash3Input(t, []byte{0xFF, 0xFF, 0xFF, 0xFF}, 0, 0x76293B50)
	testMurmurHash3Input(t, []byte{0x21, 0x43, 0x65, 0x87}, 0, 0xF55B516B)
	testMurmurHash3Input(t, []byte{0x21, 0x43, 0x65, 0x87}, 0x5082EDEE, 0x2362F9DE)
	testMurmurHash3Input(t, []byte{0x21, 0x43, 0x65}, 0, 0x7E4A8634)
	testMurmurHash3Input(t, []byte{0x21, 0x43}, 0, 0xA0F7B07A)
	testMurmurHash3Input(t, []byte{0x21}, 0, 0x72661CF4)
	testMurmurHash3Input(t, []byte{0x00, 0x00, 0x00, 0x00}, 0, 0x2362F9DE)
	testMurmurHash3Input(t, []byte{0x00, 0x00, 0x00}, 0, 0x85F0B427)
	testMurmurHash3Input(t, []byte{0x00, 0x00}, 0, 0x30F4C306)
	testMurmurHash3Input(t, []byte{0x00}, 0, 0x514E28B7)
	testMurmurHash3Input(t, []byte("Hello, world!"), 0x9747B28C, 0x24884CBA)
}

func testMurmurHash3Input(t *testing.T, data []byte, seed, expected uint32) {
	t.Helper()

	assert.Equal(t, expected, internal.MurmurHash3Sum(data, seed))
}

func TestMurmurHash3Uneven(t *testing.T) {
	t.Parallel()

	hash := internal.NewMurmurHash3(0x9747b28c)
	_, _ = hash.Write([]byte("Hel"))
	_, _ = hash.Write([]byte("l"))
	_, _ = hash.Write([]byte("o"))
	_, _ = hash.Write([]byte(", wo"))
	_, _ = hash.Write([]byte("rl"))
	_, _ = hash.Write([]byte("d!"))
	assert.Equal(t, uint32(0x24884CBA), hash.Sum32())
}

func BenchmarkMurmurHash3(b *testing.B) {
	var benchmarkString = []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.")
	h := internal.NewMurmurHash3(0)
	for i := 0; i < b.N; i++ {
		_, _ = h.Write(benchmarkString)
		h.Sum32()
		h.Reset()
	}
}

func BenchmarkFNV1a(b *testing.B) {
	var benchmarkString = []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.")
	h := fnv.New32a()
	for i := 0; i < b.N; i++ {
		_, _ = h.Write(benchmarkString)
		h.Sum32()
		h.Reset()
	}
}
