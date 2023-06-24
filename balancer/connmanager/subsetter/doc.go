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

// Package subsetter defines subsetting capabilities, which is a pluggable
// component of the default [ConnManager] implementation.
//
// The Subsetter is a very simple interface that computes a static subset of
// addresses given the authoritative set of addresses from a resolver.
//
// This package includes two implementations of the interface:
//  1. NoOp: A naive "no op" subsetter that doesn't actually compute a subset
//     but instead returns the entire set of addresses. Using this will cause
//     a [ConnManager] to establish connections to every resolved address.
//  2. NewRendezvous: This function returns a subsetter that uses [rendezvous-hashing]
//     to compute a static subset of the given size. The key that represents the
//     client (for picking a range of addresses on a hash ring) is 128-bits that
//     are generated using [crypto/rand].
//
// [ConnManager]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/connmanager#ConnManager
// [rendezvous-hashing]: https://en.wikipedia.org/wiki/Rendezvous_hashing
package subsetter
