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

func (noopRoundtripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response := new(http.Response)
	response.StatusCode = 200
	response.Body = http.NoBody
	return response, nil
}

func (n noopRoundtripper) New(scheme, target string, options httplb.RoundTripperOptions) httplb.RoundTripperResult {
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
			resp, _ := client.Get("http://localhost:0/")
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
			resp, _ := client.Get("http://localhost:0/")
			resp.Body.Close()
		}
	})
}
