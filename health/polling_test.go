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

package health

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bufbuild/httplb/attribute"
	"github.com/bufbuild/httplb/conn"
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
	checker := NewPollingChecker(PollingCheckerConfig{}, NewSimpleProber("/"))
	checker.(*pollingChecker).clock = testClock //nolint:errcheck
	tracker := make(fakeHealthTracker, 1)

	// StateUnhealthy (HTTP error)
	connection := make(fakeConnChan)
	close(connection)
	prober := checker.New(ctx, connection, tracker)
	assert.Equal(t, StateUnhealthy, <-tracker)
	require.NoError(t, prober.Close())

	// StateUnhealthy (HTTP 5xx)
	connection = make(fakeConnChan, 1)
	connection <- &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody}
	prober = checker.New(ctx, connection, tracker)
	assert.Equal(t, StateUnhealthy, <-tracker)
	require.NoError(t, prober.Close())
	close(connection)

	// StateHealthy (HTTP 2xx)
	connection = make(fakeConnChan, 1)
	connection <- &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
	prober = checker.New(ctx, connection, tracker)
	assert.Equal(t, StateHealthy, <-tracker)
	require.NoError(t, prober.Close())
	close(connection)
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
	checker.(*pollingChecker).clock = testClock //nolint:errcheck

	connection := make(fakeConnChan)
	tracker := make(fakeHealthTracker)
	process := checker.New(ctx, connection, tracker)
	advance := func(response *http.Response) {
		t.Helper()
		select {
		case connection <- response:
			err := testClock.BlockUntilContext(ctx, 1)
			require.NoError(t, err)
			testClock.Advance(interval)
		case <-tracker:
			t.Fatal("unexpected health state update")
		}
	}
	expectState := func(expected State) {
		select {
		case state := <-tracker:
			assert.Equal(t, expected, state)
		case <-ctx.Done():
			t.Fatal("health state not updated as expected within timeout")
		}
	}

	// Require only one passing check to become healthy initially
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	expectState(StateHealthy)

	// Require three failing checks to become unhealthy
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	expectState(StateUnhealthy)

	// Require two checks to become healthy again
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	close(connection)
	expectState(StateHealthy)

	err := process.Close()
	require.NoError(t, err)
}

func TestPollingCheckerTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	interval := 5 * time.Second
	testClock := clocktest.NewFakeClock()

	checker := NewPollingChecker(PollingCheckerConfig{
		PollingInterval: interval,
		Timeout:         10 * time.Millisecond,
	}, NewSimpleProber("/"))
	checker.(*pollingChecker).clock = testClock //nolint:errcheck

	connection := make(fakeConnChan)
	tracker := make(fakeHealthTracker)
	process := checker.New(ctx, connection, tracker)
	advance := func(response *http.Response) {
		t.Helper()
		testClock.Advance(interval)
		select {
		case connection <- response:
		case <-tracker:
			t.Fatal("unexpected health state update")
		}
	}
	expectState := func(expected State) {
		select {
		case state := <-tracker:
			assert.Equal(t, expected, state)
		case <-ctx.Done():
			t.Fatal("health state not updated as expected within timeout")
		}
	}

	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	expectState(StateHealthy)

	// Trigger a probe but don't send a response
	testClock.Advance(interval)
	// Timeout is handled by Go context so we can't fake it, sleep a bit to trigger.
	// If this test is flaky, raise this sleep and test timeout.
	time.Sleep(100 * time.Millisecond)
	expectState(StateUnhealthy)

	// Next response will bring it back to healthy
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	close(connection)
	expectState(StateHealthy)

	err := process.Close()
	require.NoError(t, err)
}

type fakeConnChan chan *http.Response

func (f fakeConnChan) RoundTrip(req *http.Request, _ func()) (*http.Response, error) {
	var response *http.Response
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case response = <-f:
	}
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

func (f fakeConnChan) UpdateAttributes(_ attribute.Values) {}

func (f fakeConnChan) Prewarm(_ context.Context) error { return nil }

type fakeHealthTracker chan State

func (f fakeHealthTracker) UpdateHealthState(_ conn.Conn, health State) {
	f <- health
}
