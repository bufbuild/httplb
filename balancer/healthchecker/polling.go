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
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/internal"
)

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
		scaledJitter:       config.Jitter * float64(config.PollingInterval),
		timeout:            config.Timeout,
		healthyThreshold:   config.HealthyThreshold,
		unhealthyThreshold: config.UnhealthyThreshold,
		prober:             prober,
		rnd:                internal.NewLockedRand(),
		clock:              internal.NewRealClock(),
	}
}

// PollingCheckerConfig represents the configuration options when calling
// NewPollingChecker.
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
	// to promote an unhealthy, degraded or unknown backend to healthy. This is
	// not used for the initial health check, so a connection will be healthy
	// immediately if the first health check passes.
	//
	// Defaults to 1.
	HealthyThreshold int

	// UnhealthyThreshold specifies the number of failed health checks needed to
	// demote a healthy connection to unhealthy, degraded, or unknown. This can
	// reduce flapping: Setting this value to two would prevent a single
	// spurious health-check failure from causing a connection to be marked as
	// unhealthy.
	//
	// Defaults to 1.
	UnhealthyThreshold int
}

// A Prober is a type that can perform single-shot healthchecks against a
// connection.
type Prober interface {
	Probe(ctx context.Context, conn conn.Conn) HealthState
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
			return Unknown
		}
		resp, err := conn.RoundTrip(req, nil)
		if err != nil {
			return Unhealthy
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return Unhealthy
		}
		return Healthy
	})
}

type pollingChecker struct {
	interval     time.Duration
	scaledJitter float64
	timeout      time.Duration

	unhealthyThreshold int
	healthyThreshold   int

	prober Prober
	rnd    *rand.Rand
	clock  internal.Clock
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

	state := Unknown

	// Start the counter off at the healthy threshold so that it can transition
	// into healthy state in one passed check. The unhealthy threshold is not
	// a concern since the initial state (Unknown) is already considered to be
	// unhealthy.
	counter := r.healthyThreshold

	go func() {
		defer close(task.doneSignal)
		defer cancel()

		ticker := r.clock.NewTicker(r.calcJitter(r.interval))
		defer ticker.Stop()

		for {
			ctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()

			result := r.prober.Probe(ctx, conn)

			lastState := state
			switch {
			case result == Healthy && (state == Unhealthy || state == Degraded || state == Unknown):
				counter++
				if counter >= r.healthyThreshold {
					state = result
					counter = 0
				}

			case state == Healthy && (result == Unhealthy || result == Degraded || result == Unknown):
				counter++
				if counter >= r.unhealthyThreshold {
					state = result
					counter = 0
				}

			default:
				state = result
				counter = 0
			}

			if lastState != state {
				tracker.UpdateHealthState(conn, result)
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.Chan():
				ticker.Reset(r.calcJitter(r.interval))
			}
		}
	}()
	return task
}

func (r *pollingChecker) calcJitter(interval time.Duration) time.Duration {
	if r.scaledJitter == 0 {
		return interval
	}

	// This may lose precision if your interval is longer than ~104 days.
	return time.Duration(float64(interval) + ((r.rnd.Float64()*2 - 1) * r.scaledJitter))
}

type pollingCheckerTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
}

func (t *pollingCheckerTask) Close() error {
	t.cancel()
	<-t.doneSignal
	return nil
}

type proberFunc func(ctx context.Context, conn conn.Conn) HealthState

func (f proberFunc) Probe(ctx context.Context, conn conn.Conn) HealthState {
	return f(ctx, conn)
}
