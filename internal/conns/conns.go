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

// Package conns contains internal helpers relating to conn.Conns values.
package conns

import "github.com/bufbuild/httplb/conn"

// Set is a set of connections. Since connections are map keys in the
// underlying type, they are unique. Standard map iteration is used to
// enumerate the contents of the set.
type Set map[conn.Conn]struct{}

// Contains returns true if the set contains the given connection.
func (s Set) Contains(c conn.Conn) bool {
	_, ok := s[c]
	return ok
}

// Equals returns true if s has the same connections as other.
func (s Set) Equals(other Set) bool {
	if len(s) != len(other) {
		return false
	}
	for c := range s {
		_, ok := other[c]
		if !ok {
			return false
		}
	}
	return true
}

// ToSet converts a conn.Conns into a conn.Set.
func ToSet(conns conn.Conns) Set {
	set := Set{}
	for i := 0; i < conns.Len(); i++ {
		set[conns.Get(i)] = struct{}{}
	}
	return set
}

// SetFromSlice converts a []conn.Conn into a conn.Set.
func SetFromSlice(conns []conn.Conn) Set {
	set := Set{}
	for _, c := range conns {
		set[c] = struct{}{}
	}
	return set
}

// FromSlice returns a conn.Conns that
// represents the given slice. Note that no defensive
// copy is made, so changes to the given slice's contents
// will be reflected in the returned value.
func FromSlice(conns []conn.Conn) conn.Conns {
	return connections(conns)
}

// FromSet converts a conn.Set to a conn.Conns.
func FromSet(set Set) conn.Conns {
	sl := make([]conn.Conn, 0, len(set))
	for c := range set {
		sl = append(sl, c)
	}
	return FromSlice(sl)
}

type connections []conn.Conn

func (c connections) Len() int {
	return len(c)
}

func (c connections) Get(i int) conn.Conn {
	return c[i]
}
