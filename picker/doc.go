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

// Package picker provides functionality for picking a connection.
// This is used by an httplb.Client to actually select a connection for
// use with a given request.
//
// This package defines the core interface, [Picker], as well as the
// interface used by a client to construct a [Picker]: [Factory].
//
// This package also contains numerous implementations, all in the form
// of various [Factory] instances. Each factory produces pickers that
// implement a particular picking algorithm, like round-robin, random,
// or least-loaded.
//
// None of the provided implementations in this package make use of
// custom metadata (resolver.Attrs) for an address. But custom [Picker]
// implementations could, for example to prefer backends in clusters
// that are geographically closer, or to implement custom affinity
// policies, or even to implement weighted selection algorithms in
// the face of heterogeneous backends (where the name resolution/service
// discovery system has information about a backend's capacity).
package picker
