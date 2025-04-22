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
	"hash/maphash"
	"math/rand"
)

// NewRand returns a properly seeded *rand.Rand. The seed is computed using
// the "hash/maphash" package, which can be used concurrently and is
// lock-free. Effectively, we're using the runtime's internal per-thread
// RNG to seed a new rand.Rand.
//
// The returned value is not thread-safe. If you need a thread-safe random
// number generator, use the top-level functions of the "math/rand/v2"
// package. They are not as fast as the Go 1 ("math/rand") generator, but
// they are much faster if access to the Go 1 generator has to be
// synchronized with a mutex.
func NewRand() *rand.Rand {
	return rand.New(rand.NewSource(randomSeed())) //nolint:gosec // don't need cryptographic RNG
}

// randomSeed generates a high-quality (random) seed that can be used to
// create new instances of *rand.Rand, while avoiding the global rand's
// synchronization overhead. This solution comes from a discussion in a
// Reddit thread:
//
//	https://www.reddit.com/r/golang/comments/m9b0yp/comment/grotn1f/
func randomSeed() int64 {
	var hash maphash.Hash
	return int64(hash.Sum64())
}
