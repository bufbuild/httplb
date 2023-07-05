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

package httplb_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/bufbuild/httplb"
)

type noopRoundtripper struct {
}

func (noopRoundtripper) RoundTrip(*http.Request) (*http.Response, error) {
	response := new(http.Response)
	response.StatusCode = 200
	response.Body = http.NoBody
	return response, nil
}

func (n noopRoundtripper) New(string, string, httplb.RoundTripperOptions) httplb.RoundTripperResult {
	return httplb.RoundTripperResult{
		RoundTripper: n,
	}
}

func BenchmarkNoOpTransportHTTPLB(b *testing.B) {
	warmCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	client := httplb.NewClient(
		httplb.WithRoundTripperFactory("http", noopRoundtripper{}),
		httplb.WithBackendTarget("http", "localhost:0"),
	)
	err := client.Prewarm(warmCtx)
	cancel()
	if err != nil {
		b.Fatal("Failed to prewarm client.")
	}
	b.SetParallelism(100)
	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			req, _ := http.NewRequestWithContext(context.TODO(), http.MethodGet, "http://localhost:0/", nil)
			resp, _ := client.Do(req)
			resp.Body.Close()
		}
	})
}

func BenchmarkNoOpTransportNetHTTP(b *testing.B) {
	client := new(http.Client)
	client.Transport = noopRoundtripper{}
	b.SetParallelism(100)
	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			req, _ := http.NewRequestWithContext(context.TODO(), http.MethodGet, "http://localhost:0/", nil)
			resp, _ := client.Do(req)
			resp.Body.Close()
		}
	})
}
