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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TODO: make real tests... this is just a simple "smoke test" that the current
//	     scaffolding for the hierarchy of transports results in a usable client

func TestNewClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	svr := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(([]byte)("got it"))
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		err := svr.Serve(listener)
		require.Equal(t, http.ErrServerClosed, err)
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		err := svr.Shutdown(shutdownCtx)
		require.NoError(t, err)
	}()

	client := NewClient(WithDebugResourceLeaks(func(*http.Request, *http.Response) {
		require.Fail(t, "response from %v was finalized but never consumed/closed")
	}))
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
	defer func() {
		err := resp.Body.Close()
		require.NoError(t, err)
	}()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "got it", string(body))
}
