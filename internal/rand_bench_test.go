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

// These linters don't like that we're not using crypto/rand. They also don't
// like the use of underscores in benchmark names.
//
//nolint:gosec,revive,stylecheck
package internal

import (
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

// The "rand" suffixes use NewRand to create an RNG, or NewLockedRand
// for the concurrent tests. This uses maphash to generate a seed and
// then v1 of math/rand to generate numbers (optionally with source
// protected by mutex, for concurrent tests).
//
// The "randv2pcg" suffixes use the PCG source in math/rand/v2. Similar
// to above, a locked source is used for concurrent tests.
//
// Finally, the "randv2global" suffixes just use the global functions
// in math/rand/v2, which now exposes the runtime's internal thread-safe
// but lock-free RNGs (uses per-thread RNGs). This does not require the
// use of a mutex for the concurrent tests.

func BenchmarkRand_Uint64_rand(b *testing.B) {
	benchmarkRand_Uint64(b, NewRand())
}

func BenchmarkRand_Uint64_randv2pcg(b *testing.B) {
	benchmarkRand_Uint64(b, newRandV2PCG())
}

func BenchmarkRand_Uint64_randv2chacha8(b *testing.B) {
	benchmarkRand_Uint64(b, newRandV2ChaCha8())
}

func BenchmarkRand_Uint64_randv2global(b *testing.B) {
	benchmarkRand_Uint64(b, newRandV2Global())
}

func benchmarkRand_Uint64(b *testing.B, rnd rng) {
	b.Helper()
	for range b.N {
		rnd.Uint64()
	}
}

func BenchmarkRand_Shuffle_rand(b *testing.B) {
	benchmarkRand_Shuffle(b, NewRand())
}

func BenchmarkRand_Shuffle_randv2pcg(b *testing.B) {
	benchmarkRand_Shuffle(b, newRandV2PCG())
}

func BenchmarkRand_Shuffle_randv2chacha8(b *testing.B) {
	benchmarkRand_Shuffle(b, newRandV2ChaCha8())
}

func BenchmarkRand_Shuffle_randv2global(b *testing.B) {
	benchmarkRand_Shuffle(b, newRandV2Global())
}

func benchmarkRand_Shuffle(b *testing.B, rnd rng) {
	b.Helper()
	deck := make([]int, 52)
	for i := range deck {
		deck[i] = i
	}
	b.ResetTimer()
	for range b.N {
		rnd.Shuffle(len(deck), func(i, j int) {
			deck[i], deck[j] = deck[j], deck[i]
		})
	}
}

func BenchmarkRand_ConcurrentIntN_rand(b *testing.B) {
	rng := NewLockedRand()
	benchmarkRand_ConcurrentIntN(b, rng.Intn)
}

func BenchmarkRand_ConcurrentIntN_randv2pcg(b *testing.B) {
	rng := newLockedRandV2PCG()
	benchmarkRand_ConcurrentIntN(b, rng.IntN)
}

func BenchmarkRand_ConcurrentIntN_randv2chacha8(b *testing.B) {
	rng := newLockedRandV2ChaCha8()
	benchmarkRand_ConcurrentIntN(b, rng.IntN)
}

func BenchmarkRand_ConcurrentIntN_randv2global(b *testing.B) {
	rng := newRandV2Global()
	benchmarkRand_ConcurrentIntN(b, rng.IntN)
}

func benchmarkRand_Concurrent(b *testing.B, factory func() func()) {
	b.Helper()
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	for range runtime.GOMAXPROCS(0) {
		ready.Add(1)
		done.Add(1)
		action := factory()
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			for range b.N {
				action()
			}
		}()
	}
	ready.Wait()
	b.ResetTimer()
	close(start)
	done.Wait()
}

func benchmarkRand_ConcurrentIntN(b *testing.B, action func(int) int) {
	b.Helper()
	benchmarkRand_Concurrent(b, func() func() {
		limit := 1
		return func() {
			limit <<= 1
			if limit <= 0 {
				limit = 2
			}
			action(limit)
		}
	})
}

func BenchmarkRand_ConcurrentFloat64_rand(b *testing.B) {
	benchmarkRand_ConcurrentFloat64(b, NewLockedRand())
}

func BenchmarkRand_ConcurrentFloat64_randv2pcg(b *testing.B) {
	benchmarkRand_ConcurrentFloat64(b, newLockedRandV2PCG())
}

func BenchmarkRand_ConcurrentFloat64_randv2chacha8(b *testing.B) {
	benchmarkRand_ConcurrentFloat64(b, newLockedRandV2ChaCha8())
}

func BenchmarkRand_ConcurrentFloat64_randv2global(b *testing.B) {
	benchmarkRand_ConcurrentFloat64(b, newRandV2Global())
}

func benchmarkRand_ConcurrentFloat64(b *testing.B, rnd rng) {
	b.Helper()
	benchmarkRand_Concurrent(b, func() func() { return func() { rnd.Float64() } })
}

type rng interface {
	Uint64() uint64
	Shuffle(n int, swap func(i, j int))
	Float64() float64

	// This method is absent from the interface because the
	// v1 rand.Rand type uses a different spelling. We handle
	// the discrepancy above in the relevant test cases.
	//	IntN(n int) int
}

func newRandV2PCG() *rand.Rand {
	src := rand.NewPCG(uint64(randomSeed()), uint64(randomSeed()))
	return rand.New(src)
}

func newRandV2ChaCha8() *rand.Rand {
	seeds := [4]int64{randomSeed(), randomSeed(), randomSeed(), randomSeed()}
	byteSeeds := *(*[32]byte)(unsafe.Pointer(&seeds))
	src := rand.NewChaCha8(byteSeeds)
	return rand.New(src)
}

func newLockedRandV2PCG() *rand.Rand {
	src := rand.NewPCG(uint64(randomSeed()), uint64(randomSeed()))
	return rand.New(&lockedV2Source{src: src})
}

func newLockedRandV2ChaCha8() *rand.Rand {
	seeds := [4]int64{randomSeed(), randomSeed(), randomSeed(), randomSeed()}
	byteSeeds := *(*[32]byte)(unsafe.Pointer(&seeds))
	src := rand.NewChaCha8(byteSeeds)
	return rand.New(&lockedV2Source{src: src})
}

func newRandV2Global() global {
	return global{}
}

type lockedV2Source struct {
	mu  sync.Mutex
	src rand.Source
}

func (l *lockedV2Source) Uint64() uint64 {
	l.mu.Lock()
	ret := l.src.Uint64()
	l.mu.Unlock()
	return ret
}

type global struct{}

func (g global) Uint64() uint64 {
	return rand.Uint64()
}

func (g global) Shuffle(n int, swap func(i int, j int)) {
	rand.Shuffle(n, swap)
}

func (g global) IntN(n int) int {
	return rand.IntN(n)
}

func (g global) Float64() float64 {
	return rand.Float64()
}
