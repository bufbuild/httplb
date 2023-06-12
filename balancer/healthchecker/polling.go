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

package healthchecker

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
)

type pollingChecker struct {
	interval time.Duration
	prober   Prober
}

type pollingCheckerTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
}

// A Prober is a type that can perform single-shot healthchecks against a
// connection.
type Prober interface {
	Probe(ctx context.Context, conn conn.Conn) HealthState
}

type proberFunc func(ctx context.Context, conn conn.Conn) HealthState

// NewPollingChecker creates a new checker that calls a single-shot prober
// on a fixed interval.
func NewPollingChecker(interval time.Duration, prober Prober) Checker {
	return &pollingChecker{
		interval: interval,
		prober:   prober,
	}
}

// NewSimpleProber creates a new prober that performs an HTTP GET request to the
// provided path. If it returns a successful status (status codes from 200-299),
// the connection is considered healthy. Otherwise, it is considered unhealthy.
func NewSimpleProber(url string) Prober {
	return proberFunc(func(ctx context.Context, conn conn.Conn) HealthState {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return HealthStateUnknown
		}
		resp, err := conn.RoundTrip(req, nil)
		if err != nil {
			return HealthStateUnhealthy
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return HealthStateUnhealthy
		}
		return HealthStateHealthy
	})
}

func (r *pollingChecker) New(
	ctx context.Context,
	conn conn.Conn,
	tracker HealthTracker,
) io.Closer {
	ctx, cancel := context.WithCancel(ctx)
	task := &pollingCheckerTask{
		cancel:     cancel,
		doneSignal: make(chan struct{}),
	}

	go func() {
		defer close(task.doneSignal)
		defer cancel()

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			tracker.UpdateHealthState(conn, r.prober.Probe(ctx, conn))

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return task
}

func (t *pollingCheckerTask) Close() error {
	t.cancel()
	<-t.doneSignal
	return nil
}

func (f proberFunc) Probe(ctx context.Context, conn conn.Conn) HealthState {
	return f(ctx, conn)
}
