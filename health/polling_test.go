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

package health_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal/clocktest"
	"github.com/bufbuild/httplb/resolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollingChecker(t *testing.T) {
	t.Parallel()

	testClock := clocktest.NewFakeClock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	checker := health.NewPollingChecker(health.PollingCheckerConfig{}, health.NewSimpleProber("/"))
	health.SetPollingClock(checker, testClock)
	tracker := make(fakeHealthTracker, 1)

	// StateUnhealthy (HTTP error)
	connection := make(fakeConnChan)
	close(connection)
	err := checker.New(ctx, connection, tracker).Close()
	require.NoError(t, err)
	assert.Equal(t, health.StateUnhealthy, <-tracker)

	// StateUnhealthy (HTTP 5xx)
	connection = make(fakeConnChan, 1)
	connection <- &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody}
	err = checker.New(ctx, connection, tracker).Close()
	require.NoError(t, err)
	assert.Equal(t, health.StateUnhealthy, <-tracker)
	close(connection)

	// StateHealthy (HTTP 2xx)
	connection = make(fakeConnChan, 1)
	connection <- &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
	err = checker.New(ctx, connection, tracker).Close()
	require.NoError(t, err)
	assert.Equal(t, health.StateHealthy, <-tracker)
	close(connection)
}

func TestPollingCheckerThresholds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	interval := 5 * time.Second
	testClock := clocktest.NewFakeClock()

	checker := health.NewPollingChecker(health.PollingCheckerConfig{
		PollingInterval:    interval,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
	}, health.NewSimpleProber("/"))
	health.SetPollingClock(checker, testClock)

	connection := make(fakeConnChan)
	tracker := make(fakeHealthTracker)
	process := checker.New(ctx, connection, tracker)
	advance := func(response *http.Response) {
		t.Helper()
		select {
		case connection <- response:
			err := testClock.BlockUntilContext(ctx, 1)
			assert.NoError(t, err)
			testClock.Advance(interval)
		case <-tracker:
			t.Fatal("unexpected health state update")
		}
	}
	expectState := func(expected health.State) {
		select {
		case state := <-tracker:
			assert.Equal(t, expected, state)
		case <-ctx.Done():
			t.Fatal("health state not updated as expected within timeout")
		}
	}

	// Require only one passing check to become healthy initially
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	expectState(health.StateHealthy)

	// Require three failing checks to become unhealthy
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	expectState(health.StateUnhealthy)

	// Require two checks to become healthy again
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	close(connection)
	expectState(health.StateHealthy)

	err := process.Close()
	require.NoError(t, err)
}

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

func (f fakeConnChan) UpdateAttributes(_ resolver.Attrs) {}

func (f fakeConnChan) Prewarm(_ context.Context) error { return nil }

type fakeHealthTracker chan health.State

func (f fakeHealthTracker) UpdateHealthState(_ conn.Conn, health health.State) {
	f <- health
}
