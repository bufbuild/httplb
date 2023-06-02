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

package httpbalancer

import "net/http"

// ClientOption is an option used to customize the behavior of an HTTP client.
type ClientOption interface {
	apply(*clientOptions)
}

// NewClient returns a new HTTP client that uses the given options.
func NewClient(options ...ClientOption) *http.Client {
	var opts clientOptions
	for _, opt := range options {
		opt.apply(&opts)
	}
	// TODO: implement me
	return &http.Client{
		Transport:     newTransport(&opts),
		CheckRedirect: opts.redirectFunc,
	}
}

type clientOptions struct {
	redirectFunc func(req *http.Request, via []*http.Request) error
}
