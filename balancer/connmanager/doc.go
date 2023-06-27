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

// Package connmanager defines "connection management" capabilities,
// which is a pluggable component of the default [Balancer] implementation.
//
// This package provides the core interfaces [Factory] and [ConnManager]. A
// [Factory] is used by a balancer.Factory to create a ConnManager for each
// [Balancer] instance it creates. The ConnManager then interacts with the
// [Balancer] via the ConnUpdater -- which is a function that allows the
// manager to reconcile connections (create new ones, remove existing ones)
// in batch. (Providing the set of new and removed connections in a single
// call allows for potentially simpler synchronization and less churn in
// creating new pickers.)
//
// The package also provides a default implementation of ConnManager that
// uses delegates to a [Subsetter] to decide the addresses to which connections
// should be made. It then reconciles the subset results against the set of
// existing connections to decide what new ones should be created or which
// should be closed. The [Subsetter] interface is very simple and only allows
// for static subsetting; more sophisticated subsetting would require a new
// implementation of ConnManager.
//
// [Balancer]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer#Balancer
// [Subsetter]: https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer/connmanager/subsetter#Subsetter
package connmanager
