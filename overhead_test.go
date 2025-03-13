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

package httplb

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type nopTransport struct{}

func (n nopTransport) NewRoundTripper(string, string, TransportConfig) RoundTripperResult {
	return RoundTripperResult{
		RoundTripper: n,
	}
}

func (nopTransport) RoundTrip(*http.Request) (*http.Response, error) {
	response := new(http.Response)
	response.StatusCode = 200
	response.Body = http.NoBody
	return response, nil
}

func BenchmarkNopTransportHTTPLB(b *testing.B) {
	client := NewClient(
		WithTransport("http", nopTransport{}),
		WithAllowBackendTarget("http", "localhost:0"),
	)
	warmCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := client.prewarm(warmCtx)
	cancel()
	require.NoError(b, err)
	b.SetParallelism(100)
	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, "http://localhost:0/", nil)
			if err != nil {
				b.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				b.Fatal(err)
			}
			response.Body.Close()
		}
	})
}

func BenchmarkNopTransportNetHTTP(b *testing.B) {
	client := new(http.Client)
	client.Transport = nopTransport{}
	b.SetParallelism(100)
	b.ResetTimer()
	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, "http://localhost:0/", nil)
			if err != nil {
				b.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				b.Fatal(err)
			}
			response.Body.Close()
		}
	})
}
