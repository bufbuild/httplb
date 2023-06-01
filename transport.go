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

// Package httpbalancer provides http.Client instances that are suitable
// for use for server-to-server communications, like RPC. This adds features
// on top of the standard net/http library for name/address resolution,
// health checking, connection management and subsetting, and load balancing.
// It also provides more suitable defaults and much simpler support for
// HTTP/2 over plaintext.
package httpbalancer

// TODO: Implement me!!
