// Copyright 2023-2025 Buf Technologies, Inc.
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
	"math/rand"
	randv2 "math/rand/v2"
)

// NewRand returns a properly seeded *rand.Rand.
//
// The returned value is not thread-safe. If you need a thread-safe random
// number generator, use the top-level functions of the "math/rand/v2"
// package. They are not as fast as the Go 1 ("math/rand") generator, but
// they are much faster if access to the Go 1 generator must be guarded
// by a mutex.
func NewRand() *rand.Rand {
	// The top-level functions in "math/rand/v2" use the same per-thread,
	// lock-free RNGs as the "hash/maphash" package. So we use that to
	// generate a seed. Thereafter, we use the Go 1 generator in "math/rand"
	// since its RNG is a bit faster than the offerings in "math/rand/v2".
	return rand.New(rand.NewSource(randv2.Int64())) //nolint:gosec // don't need cryptographic RNG
}
