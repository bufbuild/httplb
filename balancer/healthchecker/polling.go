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
	"net/url"
	"time"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
)

type pollingChecker struct {
	interval time.Duration
	jitter   float64
	timeout  time.Duration

	unhealthyThreshold int
	healthyThreshold   int

	prober Prober
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

type PollingCheckerConfig struct {
	// How often the probe is performed. Defaults to 15 seconds if zero.
	PollingInterval time.Duration

	// How much to jitter the period. This should be a value between 0 and 1.
	// Zero means no jitter. One means the jitter could perturb the period up
	// to 100% (so the actual period could be between 0 and twice the configured
	// period).
	Jitter float64

	// The time limit for a probe. If it takes longer than this, the health
	// check is assumed to fail as unhealthy. Defaults to the polling period.
	Timeout time.Duration

	// HealthyThreshold specifies the number of successful health checks needed
	// to promote an unhealthy or degraded backend to healthy. A connection will
	// initially be in the unknown state, and thus will be considered healthy or
	// unhealthy after the first valid result regardless of this setting.
	// Defaults to 1.
	HealthyThreshold int

	// UnhealthyThreshold specifies the number of failed health checks needed to
	// demote a healthy connection to unhealthy. Setting this to larger than one
	// can reduce flapping. If configured to two, for example, a healthy backend
	// is only demoted to unhealthy after two unhealthy probe results in a row.
	// Defaults to 1.
	UnhealthyThreshold int
}

// NewPollingChecker creates a new checker that calls a single-shot prober
// on a fixed interval.
func NewPollingChecker(config PollingCheckerConfig, prober Prober) Checker {
	if config.PollingInterval == 0 {
		config.PollingInterval = 15 * time.Second
	}
	if config.Timeout == 0 {
		config.Timeout = config.PollingInterval
	}
	if config.UnhealthyThreshold == 0 {
		config.UnhealthyThreshold = 1
	}
	if config.HealthyThreshold == 0 {
		config.HealthyThreshold = 1
	}
	return &pollingChecker{
		interval:           config.PollingInterval,
		jitter:             config.Jitter,
		timeout:            config.Timeout,
		healthyThreshold:   config.HealthyThreshold,
		unhealthyThreshold: config.UnhealthyThreshold,
		prober:             prober,
	}
}

// NewSimpleProber creates a new prober that performs an HTTP GET request to the
// provided path. If it returns a successful status (status codes from 200-299),
// the connection is considered healthy. Otherwise, it is considered unhealthy.
func NewSimpleProber(path string) Prober {
	return proberFunc(func(ctx context.Context, conn conn.Conn) HealthState {
		url := url.URL{
			Scheme: conn.Scheme(),
			Host:   conn.Address().HostPort,
			Path:   path,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), http.NoBody)
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

	state := HealthStateUnknown
	counter := 0

	go func() {
		defer close(task.doneSignal)
		defer cancel()

		ticker := time.NewTicker(jitter(r.interval, r.jitter))
		defer ticker.Stop()

		for {
			ctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()

			result := r.prober.Probe(ctx, conn)

			lastState := state
			switch {
			case result == HealthStateHealthy && (state == HealthStateUnhealthy || state == HealthStateDegraded):
				counter++
				if counter >= r.healthyThreshold {
					state = result
					counter = 0
				}

			case state == HealthStateHealthy && (result == HealthStateUnhealthy || result == HealthStateDegraded):
				counter++
				if counter >= r.unhealthyThreshold {
					state = result
					counter = 0
				}

			case result != HealthStateUnknown:
				state = result
				counter = 0
			}

			if lastState != state {
				tracker.UpdateHealthState(conn, result)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ticker.Reset(jitter(r.interval, r.jitter))
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

func jitter(d time.Duration, amount float64) time.Duration {
	if amount == 0 {
		return d
	}

	// This may lose precision if your interval is longer than ~104 days.
	return time.Duration(float64(d) * (amount + 1))
}
