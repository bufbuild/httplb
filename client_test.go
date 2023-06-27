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

package httplb

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestNewClient_Basic(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("got it"))
	}))
	client := makeClient(t, ctx,
		WithBackendTarget("http", addr),
		WithBackendTarget("h2c", addr),
	)

	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr), expectSuccess("got it"))

	t.Logf("Number of goroutines after HTTP client created and used: %d", runtime.NumGoroutine())

	// do it again, using h2c
	sendGetRequest(t, ctx, client, fmt.Sprintf("h2c://%s/foo", addr), expectSuccess("got it"))
}

func TestNewClient_MultipleTargets(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr1 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("tweedle dee"))
	}))
	addr2 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("tweedle dum"))
	}))
	addr3 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("twinkle twinkle little bat")) //nolint:dupword //intentional!
	}))
	client := makeClient(t, ctx,
		WithBackendTarget("http", addr1),
		WithBackendTarget("http", addr2),
		WithBackendTarget("http", addr3),
	)

	var sentGroup sync.WaitGroup
	var finished atomic.Int32
	finishedChan := make(chan struct{})
	cases := map[string]string{
		addr1: "tweedle dee",
		addr2: "tweedle dum",
		addr3: "twinkle twinkle little bat", //nolint:dupword //intentional!
	}
	for addr, expected := range cases {
		addr, expected := addr, expected
		sentGroup.Add(1)
		go func() {
			defer func() {
				if finished.Add(1) == int32(len(cases)) {
					close(finishedChan)
				}
			}()
			var sent bool
			defer func() {
				if !sent {
					sentGroup.Done()
				}
			}()
			sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr), expectSuccess(expected))
		}()
	}
	sentGroup.Wait()
	t.Logf("Number of goroutines after HTTP client created and used: %d", runtime.NumGoroutine())
	err := Close(client)
	require.NoError(t, err)
	// After Close(client) returns, all outstanding requests are done. The finished counter
	// is updated when the goroutine exits, which could happen sometime after the requests
	// are actually completed. So it is possible for it to have not yet reached three at
	// this point. So we wait a short time. (In local test environments, the counter was
	// always already 3 at this point, but this is to avoid timing-based flakes in CI.)
	select {
	case <-finishedChan:
		// everything done on time
	case <-time.After(500 * time.Millisecond):
		// we'll load the counter to see if it reached 3 but the channel not yet closed
		require.Equal(t, int32(len(cases)), finished.Load())
	}
}

func TestNewClient_LoadBalancing(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	var counters [3]atomic.Int32
	addr1 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counters[0].Add(1)
		_, _ = w.Write(([]byte)("got it!"))
	}))
	addr2 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counters[1].Add(1)
		_, _ = w.Write(([]byte)("got it!"))
	}))
	addr3 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counters[2].Add(1)
		_, _ = w.Write(([]byte)("got it!"))
	}))
	client := makeClient(t, ctx,
		WithResolver(fakeResolverFactory{map[string][]string{"foo.com": {addr1, addr2, addr3}}}),
		WithBackendTarget("http", "foo.com"),
	)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sendGetRequest(t, ctx, client, "http://foo.com/foo", expectSuccess("got it!"))
		}()
	}
	wg.Wait()
	// Default picker is round-robin, which should have resulted in all 3 addresses
	// getting even load: 10 requests each.
	require.Equal(t, int32(10), counters[0].Load())
	require.Equal(t, int32(10), counters[1].Load())
	require.Equal(t, int32(10), counters[2].Load())
}

func TestNewClient_RoundTripperOptions(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	var latestOptions sync.Map
	var latestResults sync.Map
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect5":
			http.Redirect(w, r, "/redirect4", http.StatusFound)
		case "/redirect4":
			http.Redirect(w, r, "/redirect3", http.StatusFound)
		case "/redirect3":
			http.Redirect(w, r, "/redirect2", http.StatusFound)
		case "/redirect2":
			http.Redirect(w, r, "/redirect", http.StatusFound)
		case "/redirect":
			http.Redirect(w, r, "/foo", http.StatusFound)
		}
		_, _ = w.Write(([]byte)("got it"))
	})
	addr1 := startServer(t, ctx, handler)
	addr2 := startServer(t, ctx, handler)
	addr3 := startServer(t, ctx, handler)
	var dialCount atomic.Int32
	tlsConf := &tls.Config{} //nolint:gosec
	client := makeClient(t, ctx,
		WithRoundTripperFactory("http", roundTripperFactoryFunc(func(scheme, target string, options RoundTripperOptions) RoundTripperResult {
			latestOptions.Store(target, options)
			result := simpleFactory{}.New(scheme, target, options)
			latestResults.Store(target, result)
			return result
		})),
		WithBackendTarget("http", addr1), // all defaults
		WithBackendTarget("http", addr2,
			WithNoProxy(),
			WithMaxResponseHeaderBytes(10101),
			WithRedirects(FollowRedirects(3)),
			WithIdleConnectionTimeout(time.Second),
			WithTLSConfig(tlsConf, 5*time.Second),
			WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialCount.Add(1)
				return defaultDialer.DialContext(ctx, network, addr)
			}),
		),
	)

	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr1), expectSuccess("got it"))

	// make sure round tripper options were all defaults
	val, ok := latestOptions.Load(addr1)
	require.True(t, ok)
	rtOpts := val.(RoundTripperOptions) //nolint:errcheck
	require.NotNil(t, rtOpts)
	require.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(rtOpts.ProxyFunc).Pointer())
	require.Nil(t, rtOpts.ProxyConnectHeadersFunc)
	require.Nil(t, rtOpts.TLSClientConfig)
	require.Equal(t, 10*time.Second, rtOpts.TLSHandshakeTimeout)
	require.Equal(t, int64(1<<20), rtOpts.MaxResponseHeaderBytes)
	require.Zero(t, rtOpts.IdleConnTimeout)
	require.True(t, rtOpts.KeepWarm)
	// and check that settings were conveyed to round tripper as expected
	val, ok = latestResults.Load(addr1)
	require.True(t, ok)
	transport := val.(RoundTripperResult).RoundTripper.(*http.Transport) //nolint:errcheck
	require.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(transport.Proxy).Pointer())
	require.Nil(t, transport.GetProxyConnectHeader)
	require.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
	require.Equal(t, int64(1<<20), transport.MaxResponseHeaderBytes)
	require.Zero(t, transport.IdleConnTimeout)

	// no redirects
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/redirect", addr1), expectRedirect("/foo"))

	// now try a backend that is not configured (so all defaults)
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr3), expectSuccess("got it"))

	// same defaults as above, except KeepWarm is false
	val, ok = latestOptions.Load(addr3)
	require.True(t, ok)
	rtOpts = val.(RoundTripperOptions) //nolint:errcheck
	require.NotNil(t, rtOpts)
	require.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(rtOpts.ProxyFunc).Pointer())
	require.Nil(t, rtOpts.ProxyConnectHeadersFunc)
	require.Nil(t, rtOpts.TLSClientConfig)
	require.Equal(t, 10*time.Second, rtOpts.TLSHandshakeTimeout)
	require.Equal(t, int64(1<<20), rtOpts.MaxResponseHeaderBytes)
	require.Zero(t, rtOpts.IdleConnTimeout)
	require.False(t, rtOpts.KeepWarm)

	// now try backend with the options
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr2), expectSuccess("got it"))

	// check that the options match what was configured above
	val, ok = latestOptions.Load(addr2)
	require.True(t, ok)
	rtOpts = val.(RoundTripperOptions) //nolint:errcheck
	require.NotNil(t, rtOpts)
	require.NotEqual(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(rtOpts.ProxyFunc).Pointer())
	require.Nil(t, rtOpts.ProxyConnectHeadersFunc)
	require.Same(t, tlsConf, rtOpts.TLSClientConfig)
	require.Equal(t, 5*time.Second, rtOpts.TLSHandshakeTimeout)
	require.Equal(t, int64(10101), rtOpts.MaxResponseHeaderBytes)
	require.Equal(t, time.Second, rtOpts.IdleConnTimeout)
	require.True(t, rtOpts.KeepWarm)
	// and check that settings were conveyed to round tripper as expected
	val, ok = latestResults.Load(addr2)
	require.True(t, ok)
	transport = val.(RoundTripperResult).RoundTripper.(*http.Transport) //nolint:errcheck
	require.Equal(t, reflect.ValueOf(transport.Proxy).Pointer(), reflect.ValueOf(rtOpts.ProxyFunc).Pointer())
	require.Nil(t, transport.GetProxyConnectHeader)
	require.Same(t, tlsConf, transport.TLSClientConfig)
	require.Equal(t, 5*time.Second, transport.TLSHandshakeTimeout)
	require.Equal(t, int64(10101), transport.MaxResponseHeaderBytes)
	require.Equal(t, time.Second, transport.IdleConnTimeout)

	// This one allows redirects
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/redirect3", addr2), expectSuccess("got it"))
	// But not too many
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/redirect4", addr2), expectError("too many redirects (> 3)"))
}

func TestNewClient_CustomRoundTripper(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	client := makeClient(t, ctx,
		WithRoundTripperFactory("foo", roundTripperFactoryFunc(func(scheme, target string, options RoundTripperOptions) RoundTripperResult {
			return RoundTripperResult{
				RoundTripper: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					recorder := httptest.NewRecorder()
					_, _ = recorder.Write([]byte("foo bar"))
					return recorder.Result(), nil
				}),
			}
		})),
	)

	sendGetRequest(t, ctx, client, "foo://anything.com/blah", expectSuccess("foo bar"))
}

func TestNewClient_CloseIdleTransports(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr1 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("got it"))
	}))
	addr2 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("got it"))
	}))
	client := makeClient(t, ctx,
		WithIdleTransportTimeout(200*time.Millisecond),
		WithBackendTarget("http", addr1),
	)

	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr1), expectSuccess("got it"))
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr2), expectSuccess("got it"))
	transport := client.Transport.(*mainTransport) //nolint:errcheck
	transport.mu.Lock()
	_, addr1Present := transport.pools[target{scheme: "http", hostPort: addr1}]
	_, addr2Present := transport.pools[target{scheme: "http", hostPort: addr2}]
	transport.mu.Unlock()
	require.True(t, addr1Present)
	require.True(t, addr2Present)
	// now wait for addr2 to be closed due to being idle
	deadline := time.Now().Add(time.Second)
	for {
		transport.mu.Lock()
		_, addr2Present := transport.pools[target{scheme: "http", hostPort: addr2}]
		transport.mu.Unlock()
		if !addr2Present {
			// No longer there? Success: idle transport was removed!
			break
		}
		require.LessOrEqual(t, time.Since(deadline), time.Duration(0))
	}
	// addr1 should still be present since it's a known backend that is kept warm
	transport.mu.Lock()
	_, addr1Present = transport.pools[target{scheme: "http", hostPort: addr1}]
	transport.mu.Unlock()
	require.True(t, addr1Present)
}

func TestNewClient_Timeouts(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// optional request body indicates number of milliseconds to delay before returning
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", 499)
			return
		}
		var dur int64
		if len(body) > 0 {
			dur, err = strconv.ParseInt(string(body), 10, 64)
			if err != nil {
				http.Error(w, "failed to read request delay", http.StatusBadRequest)
				return
			}
		}
		select {
		case <-r.Context().Done():
			http.Error(w, "request cancelled", 499)
			return
		case <-time.After(time.Duration(dur) * time.Millisecond):
		}
		_, _ = w.Write(([]byte)("got it"))
	}))
	client := makeClient(t, ctx,
		WithResolver(fakeResolverFactory{map[string][]string{
			"foo.com": {addr},
			"bar.com": {addr},
			"baz.com": {addr},
		}}),
		WithBackendTarget("http", "foo.com", WithRequestTimeout(200*time.Millisecond)),
		WithBackendTarget("http", "bar.com", WithDefaultTimeout(200*time.Millisecond)),
		WithBackendTarget("http", "baz.com"), // no timeout
	)

	sendGetRequest(t, ctx, client, "http://foo.com/foo", expectSuccess("got it"))
	sendGetRequest(t, ctx, client, "http://bar.com/foo", expectSuccess("got it"))
	sendGetRequest(t, ctx, client, "http://baz.com/foo", expectSuccess("got it"))
	// With payload that incurs 300ms delay, only third one succeeds due to 200ms timeout for other two.
	sendPostRequest(t, ctx, client, "http://foo.com/foo", "300", expectError("deadline exceeded"))
	sendPostRequest(t, ctx, client, "http://bar.com/foo", "300", expectError("deadline exceeded"))
	sendPostRequest(t, ctx, client, "http://baz.com/foo", "300", expectSuccess("got it"))
	// foo.com uses a hard request deadline, which applies even if request already has a deadline
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Second)
	sendPostRequest(t, timeoutCtx, client, "http://foo.com/foo", "300", expectError("deadline exceeded"))
	timeoutCancel()
	// but bar.com uses a *default* deadline, only applied to requests when a deadline is not already present
	timeoutCtx, timeoutCancel = context.WithTimeout(ctx, time.Second)
	sendPostRequest(t, timeoutCtx, client, "http://bar.com/foo", "300", expectSuccess("got it"))
	timeoutCancel()
}

func TestNewClient_Proxy(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("got it"))
	}))
	proxyAddr, proxyCounter := startProxy(t, ctx)

	client := makeClient(t, ctx,
		WithResolver(fakeResolverFactory{map[string][]string{
			"foo.com": {addr},
			"bar.com": {addr},
		}}),
		WithBackendTarget("http", "foo.com", WithNoProxy()),
		// We can't use default proxy setting, which uses environment vars, because
		// that excludes loopback addresses :(
		// But TestNewClient_RoundTripperOptions already tests that the function is
		// set by default. So we'll test with a custom proxy function.
		WithBackendTarget("http", "bar.com", WithProxy(
			func(req *http.Request) (*url.URL, error) {
				// clear out Host header so that proxy gets the
				// actual target address, not "bar.com"
				req.Host = ""
				// unconditionally use the proxy
				return &url.URL{Scheme: "http", Host: proxyAddr}, nil
			},
			nil,
		)),
	)

	sendGetRequest(t, ctx, client, "http://foo.com/foo", expectSuccess("got it"))
	require.Zero(t, proxyCounter.Load())
	// the other domain, however, will go through our proxy
	sendGetRequest(t, ctx, client, "http://bar.com/foo", expectSuccess("got it"))
	require.Equal(t, int32(1), proxyCounter.Load())
}

func TestNewClient_DisallowUnconfiguredTargets(t *testing.T) {
	ensureGoroutinesCleanedUp(t)

	ctx := context.Background()
	addr1 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("got it"))
	}))
	addr2 := startServer(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(([]byte)("don't got it"))
	}))
	client := makeClient(t, ctx,
		WithBackendTarget("http", addr1),
		WithDisallowUnconfiguredTargets(),
	)

	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr1), expectSuccess("got it"))
	// addr2 is not a configured target
	sendGetRequest(t, ctx, client, fmt.Sprintf("http://%s/foo", addr2), expectError("client does not allow requests to unconfigured target"))
}

//nolint:revive // linter wants ctx first, but t first is okay
func sendGetRequest(t *testing.T, ctx context.Context, client *http.Client, url string, expectations func(*testing.T, *http.Response, error)) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	expectations(t, resp, err)
}

//nolint:revive,unparam // linter wants ctx first, but t first is okay; body is more readable as parameter instead of hard-coded
func sendPostRequest(t *testing.T, ctx context.Context, client *http.Client, url string, body string, expectations func(*testing.T, *http.Response, error)) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := client.Do(req)
	expectations(t, resp, err)
}

func expectSuccess(contents string) func(*testing.T, *http.Response, error) {
	return func(t *testing.T, resp *http.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		err = resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, contents, string(body))
	}
}

func expectError(message string) func(*testing.T, *http.Response, error) {
	return func(t *testing.T, resp *http.Response, err error) {
		t.Helper()
		if assert.ErrorContains(t, err, message) {
			return
		}
		// if no error, we must drain and close body
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		err = resp.Body.Close()
		require.NoError(t, err)
	}
}

func expectRedirect(location string) func(*testing.T, *http.Response, error) {
	return func(t *testing.T, resp *http.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		if assert.Equal(t, http.StatusFound, resp.StatusCode) {
			assert.Equal(t, location, resp.Header.Get("Location"))
		}
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		err = resp.Body.Close()
		require.NoError(t, err)
	}
}

//nolint:revive // linter wants ctx first, but t first is okay
func startServer(t *testing.T, ctx context.Context, handler http.Handler) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	svr := http.Server{
		Handler:           h2c.NewHandler(handler, &http2.Server{}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		err := svr.Serve(listener)
		require.Equal(t, http.ErrServerClosed, err)
	}()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		err := svr.Shutdown(shutdownCtx)
		require.NoError(t, err)
	})
	t.Logf("Number of goroutines after server %s started: %d", listener.Addr().String(), runtime.NumGoroutine())

	return listener.Addr().String()
}

//nolint:revive // linter wants ctx first, but t first is okay
func startProxy(t *testing.T, ctx context.Context) (string, *atomic.Int32) {
	t.Helper()

	var count atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method == http.MethodConnect {
			// setup tunnel on behalf of CONNECT request
			transfer := func(dst io.WriteCloser, src io.ReadCloser) {
				defer func() {
					_ = src.Close()
					_ = dst.Close()
				}()
				_, _ = io.Copy(dst, src)
			}
			dstConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
				return
			}
			srcConn, _, err := hijacker.Hijack()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			go transfer(dstConn, srcConn)
			go transfer(srcConn, dstConn)
			return
		}
		// proxy the request
		r.RequestURI = "" // can't be set in client requests
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() {
			_ = resp.Body.Close()
			http.DefaultClient.CloseIdleConnections()
		}()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	return startServer(t, ctx, handler), &count
}

func ensureGoroutinesCleanedUp(t *testing.T) {
	t.Helper()
	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial number of goroutines: %d", initialGoroutines)
	t.Cleanup(func() {
		awaitGoroutinesExiting(t, initialGoroutines)
		t.Logf("Final number of goroutines, after everything stopped: %d", runtime.NumGoroutine())
	})
}

//nolint:revive // linter wants ctx first, but t first is okay
func makeClient(t *testing.T, ctx context.Context, opts ...ClientOption) *http.Client {
	t.Helper()
	client := NewClient(opts...)
	t.Cleanup(func() {
		err := Close(client)
		require.NoError(t, err)
	})
	err := Prewarm(ctx, client)
	require.NoError(t, err)
	return client
}

func awaitGoroutinesExiting(t *testing.T, expectedGoroutines int) {
	t.Helper()

	deadline := time.Now().Add(time.Second * 5)
	currentGoroutines := 0
	for deadline.After(time.Now()) {
		currentGoroutines = runtime.NumGoroutine()
		if currentGoroutines <= expectedGoroutines {
			// number of goroutines returned to previous level: no leak!
			return
		}
		time.Sleep(time.Millisecond * 50)
	}
	buf := make([]byte, 1024*1024)
	n := runtime.Stack(buf, true)
	t.Errorf("%d goroutines leaked:\n%s", currentGoroutines-expectedGoroutines, string(buf[:n]))
}

type fakeResolverFactory struct {
	addresses map[string][]string
}

func (f fakeResolverFactory) New(_ context.Context, _, hostPort string, receiver resolver.Receiver) resolver.Resolver {
	addrStrs, ok := f.addresses[hostPort]
	if !ok {
		go receiver.OnResolveError(fmt.Errorf("unknown host: %s", hostPort))
		return fakeResolver{}
	}
	addrs := make([]resolver.Address, len(addrStrs))
	for i, addr := range addrStrs {
		addrs[i] = resolver.Address{HostPort: addr}
	}
	go receiver.OnResolve(addrs)
	return fakeResolver{}
}

type fakeResolver struct{}

func (n fakeResolver) ResolveNow() {}

func (n fakeResolver) Close() error { return nil }

type roundTripperFactoryFunc func(scheme, target string, options RoundTripperOptions) RoundTripperResult

func (f roundTripperFactoryFunc) New(scheme, target string, options RoundTripperOptions) RoundTripperResult {
	return f(scheme, target, options)
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
