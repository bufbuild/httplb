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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bufbuild/go-http-balancer/attrs"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/internal/clocktest"
	"github.com/bufbuild/go-http-balancer/resolver"
	"github.com/stretchr/testify/assert"
)

type fakeConnChan chan *http.Response

func (f fakeConnChan) RoundTrip(_ *http.Request, _ func()) (*http.Response, error) {
	response := <-f
	if response == nil {
		return nil, errors.New("fake error")
	}
	return response, nil
}

func (f fakeConnChan) Scheme() string {
	return "http"
}

func (f fakeConnChan) Address() resolver.Address {
	return resolver.Address{HostPort: "::1"}
}

func (f fakeConnChan) UpdateAttributes(_ attrs.Attributes) {}

func (f fakeConnChan) Prewarm(_ context.Context) error { return nil }

type fakeHealthTracker chan HealthState

func (f fakeHealthTracker) UpdateHealthState(_ conn.Conn, health HealthState) {
	f <- health
}

func TestPollingChecker(t *testing.T) {
	t.Parallel()

	testClock := clocktest.NewFakeClock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	checker := NewPollingChecker(PollingCheckerConfig{}, NewSimpleProber("/"))
	checker.(*pollingChecker).clock = testClock
	tracker := make(fakeHealthTracker, 1)

	// Unhealthy (HTTP error)
	conn := make(fakeConnChan)
	close(conn)
	checker.New(ctx, conn, tracker).Close()
	assert.Equal(t, Unhealthy, <-tracker)

	// Unhealthy (HTTP 5xx)
	conn = make(fakeConnChan, 1)
	conn <- &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody}
	checker.New(ctx, conn, tracker).Close()
	assert.Equal(t, Unhealthy, <-tracker)
	close(conn)

	// Healthy (HTTP 2xx)
	conn = make(fakeConnChan, 1)
	conn <- &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
	checker.New(ctx, conn, tracker).Close()
	assert.Equal(t, Healthy, <-tracker)
	close(conn)
}

func TestPollingCheckerThresholds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	interval := 5 * time.Second
	testClock := clocktest.NewFakeClock()

	checker := NewPollingChecker(PollingCheckerConfig{
		PollingInterval:    interval,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
	}, NewSimpleProber("/"))
	checker.(*pollingChecker).clock = testClock

	conn := make(fakeConnChan)
	tracker := make(fakeHealthTracker)
	process := checker.New(ctx, conn, tracker)
	advance := func(response *http.Response) {
		t.Helper()
		select {
		case conn <- response:
			err := testClock.BlockUntilContext(ctx, 1)
			assert.NoError(t, err)
			testClock.Advance(interval)
		case <-tracker:
			t.Fatal("unexpected health state update")
		}
	}
	expectState := func(expected HealthState) {
		select {
		case state := <-tracker:
			assert.Equal(t, expected, state)
		case <-ctx.Done():
			t.Fatal("health state not updated as expected within timeout")
		}
	}

	// Require only one passing check to become healthy initially
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	expectState(Healthy)

	// Require three failing checks to become unhealthy
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	expectState(Unhealthy)

	// Require two checks to become healthy again
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	close(conn)
	expectState(Healthy)

	process.Close()
}
