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

package internal

import (
	"encoding/binary"
	"hash"
	"math/bits"
)

const (
	murmurC1 = 0xCC9E2D51
	murmurC2 = 0x1B873593
)

type MurmurHash3 struct {
	rem, len int
	k1, h1   uint32
}

func NewMurmurHash3(seed uint32) hash.Hash32 {
	return &MurmurHash3{h1: seed}
}

func MurmurHash3Sum(data []byte, seed uint32) uint32 {
	h := MurmurHash3{h1: seed}
	_, _ = h.Write(data)
	return h.Sum32()
}

//nolint:varnamelen // names match reference implementation for clarity
func (h *MurmurHash3) Write(data []byte) (int, error) {
	dataLen := len(data)
	h.len += dataLen
	k1, h1 := h.k1, h.h1

	if h.rem != 0 {
		i, j := h.rem, 0
		for ; i < 4 && j < dataLen; i++ {
			k1 |= uint32(data[j]) << (i << 3)
			j++
		}
		data = data[j:]
		h.rem = i
		h.k1 = k1

		if h.rem < 4 {
			return dataLen, nil
		}

		h1 = round(h1, k1)
	}

	bodyLen := len(data) &^ 3
	for i, l := 0, bodyLen; i < l; i += 4 {
		k1 := uint32(data[i+3])<<24 |
			uint32(data[i+2])<<16 |
			uint32(data[i+1])<<8 |
			uint32(data[i])
		h1 = round(h1, k1)
	}

	data = data[bodyLen:]
	h.rem = len(data)
	k1 = 0
	for i, l := 0, h.rem; i < l; i++ {
		k1 |= uint32(data[i]) << (i << 3)
	}

	h.k1 = k1
	h.h1 = h1

	return dataLen, nil
}

//nolint:varnamelen // names match reference implementation for clarity
func (h *MurmurHash3) Sum32() uint32 {
	k1 := h.k1
	h1 := h.h1

	k1 *= murmurC1
	k1 = bits.RotateLeft32(k1, 15)
	k1 *= murmurC2
	h1 ^= k1

	h1 ^= uint32(h.len)
	h1 ^= h1 >> 16
	h1 *= 0x85EBCA6B
	h1 ^= h1 >> 13
	h1 *= 0xC2B2AE35
	h1 ^= h1 >> 16
	return h1
}

//nolint:varnamelen // names match reference implementation for clarity
func round(h1, k1 uint32) uint32 {
	k1 *= murmurC1
	k1 = bits.RotateLeft32(k1, 15)
	k1 *= murmurC2
	h1 ^= k1
	h1 = bits.RotateLeft32(h1, 13)
	h1 = h1*4 + h1 + 0xE6546B64
	return h1
}

func (h *MurmurHash3) Sum(b []byte) []byte {
	return binary.LittleEndian.AppendUint32(b, h.Sum32())
}

func (h *MurmurHash3) Reset() {
	*h = MurmurHash3{}
}

func (h *MurmurHash3) Size() int {
	return 4
}

func (h *MurmurHash3) BlockSize() int {
	return 4
}
