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
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// TODO: make real tests... this is just a simple "smoke test" that the current
//	     scaffolding for the hierarchy of transports results in a usable client

func TestNewClient(t *testing.T) {
	t.Parallel()

	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial number of goroutines: %d", initialGoroutines)
	t.Cleanup(func() {
		awaitGoroutinesExiting(t, initialGoroutines)
		t.Logf("Final number of goroutines, after everything stopped: %d", runtime.NumGoroutine())
	})

	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	svr := http.Server{
		Handler: h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(([]byte)("got it"))
		}), &http2.Server{}),
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
	t.Logf("Number of goroutines after server started: %d", runtime.NumGoroutine())

	client := NewClient(
		WithDebugResourceLeaks(func(*http.Request, *http.Response) {
			require.Fail(t, "response from %v was finalized but never consumed/closed")
		}),
		WithBackendTarget("http", listener.Addr().String()),
		WithBackendTarget("h2c", listener.Addr().String()),
	)
	t.Cleanup(func() {
		err := Close(client)
		require.NoError(t, err)
	})
	err = Prewarm(ctx, client)
	require.NoError(t, err)

	url := fmt.Sprintf("http://%s/foo", listener.Addr().String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "got it", string(body))
	err = resp.Body.Close()
	require.NoError(t, err)

	t.Logf("Number of goroutines after HTTP client created and used: %d", runtime.NumGoroutine())

	// do it again, using h2c
	url = fmt.Sprintf("h2c://%s/foo", listener.Addr().String())
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "got it", string(body))
	err = resp.Body.Close()
	require.NoError(t, err)
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
