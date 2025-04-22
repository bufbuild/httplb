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
	"sync"
)

// NewRand returns a properly seeded *rand.Rand. The seed is computed using
// the "hash/maphash" package, which can be used concurrently and is
// lock-free. Effectively, we're using runtime.fastrand to seed a new
// rand.Rand.
func NewRand() *rand.Rand {
	return rand.New(rand.NewSource(randomSeed())) //nolint:gosec // don't need cryptographic RNG
}

// NewLockedRand is just like NewRand except the returned value uses a
// mutex to enable safe usage from concurrent goroutines.
//
// Despite having mutex overhead, this is better than using the global rand
// because you don't have to worry about other code linked into the same
// program that might be abusing the global rand:
//  1. It is possible for code to call rand.Seed, which could cause issues
//     with the quality of the pseudo-random number sequence.
//  2. It is possible for code to make *heavy* use of the global rand, which
//     can mean extensive lock contention on its mutex, which will slow down
//     clients trying to generate a random number.
//
// By creating a new locked *rand.Rand, nothing else will be using it and
// contending over the mutex except other code that has access to the same
// instance.
func NewLockedRand() *rand.Rand {
	//nolint:forcetypeassert,errcheck // specs say value returned by NewSource implements Source64
	src := rand.NewSource(randomSeed()).(rand.Source64)
	return rand.New(&lockedSource{src: src}) //nolint:gosec // don't need cryptographic RNG
}

type lockedSource struct {
	mu sync.Mutex
	// +checklocks:mu
	src rand.Source64
}

func (l *lockedSource) Int63() int64 {
	l.mu.Lock()
	ret := l.src.Int63()
	l.mu.Unlock()
	return ret
}

func (l *lockedSource) Uint64() uint64 {
	l.mu.Lock()
	ret := l.src.Uint64()
	l.mu.Unlock()
	return ret
}

func (l *lockedSource) Seed(seed int64) {
	l.mu.Lock()
	l.src.Seed(seed)
	l.mu.Unlock()
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
