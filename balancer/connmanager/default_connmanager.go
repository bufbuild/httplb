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

package connmanager

import (
	"github.com/bufbuild/go-http-balancer/resolver"
)

// NewFactory returns a Factory that consults the given
// Subsetter to decide on the subset of addresses to use.
func NewFactory(_ Subsetter) Factory {
	// TODO: implement me!
	return nil
}

type Subsetter interface {
	// ComputeSubset returns a static subset of the given addresses. It is
	// allowed to return duplicates, if it wants to return more addresses than
	// are actually given.
	ComputeSubset([]resolver.Address) []resolver.Address
}
