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

package subsetter

import "github.com/bufbuild/go-http-balancer/resolver"

// Subsetter represents logic to compute a static subset of resolve addresses.
// To avoid creating N^2 connections, which happens if a client connects to all
// resolved addresses and can waste resources (and even risk exhausting file
// handles in extreme cases), the Subsetter computes a smaller (often fixed)
// number of addresses to use.
//
// This interface does not support more sophisticated subsetting, like dynamic
// subsets that can change over time based on near-real-time load information
// about a target service. For that, one must implement ConnManager directly.
type Subsetter interface {
	// ComputeSubset returns a static subset of the given addresses. It is
	// allowed to return duplicates, if it wants to return more addresses than
	// are actually given.
	//
	// The given slice is not shared, so it is safe for the subsetter to mutate
	// it.
	ComputeSubset([]resolver.Address) []resolver.Address
}

type subsetterFunc func([]resolver.Address) []resolver.Address

func (f subsetterFunc) ComputeSubset(addrs []resolver.Address) []resolver.Address {
	return f(addrs)
}
