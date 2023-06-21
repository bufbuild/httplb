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

type fakeConn chan *http.Response

func (f fakeConn) RoundTrip(*http.Request, func()) (*http.Response, error) {
	response := <-f
	if response == nil {
		return nil, errors.New("fake error")
	}
	return response, nil
}

func (f fakeConn) Scheme() string {
	return "http"
}

func (f fakeConn) Address() resolver.Address {
	return resolver.Address{HostPort: "::1"}
}

func (f fakeConn) UpdateAttributes(attrs.Attributes) {}

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
	conn := make(fakeConn)
	close(conn)
	checker.New(ctx, conn, tracker).Close()
	assert.Equal(t, Unhealthy, <-tracker)

	// Unhealthy (HTTP 5xx)
	conn = make(fakeConn, 1)
	conn <- &http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody}
	checker.New(ctx, conn, tracker).Close()
	assert.Equal(t, Unhealthy, <-tracker)
	close(conn)

	// Healthy (HTTP 2xx)
	conn = make(fakeConn, 1)
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

	conn := make(fakeConn)
	tracker := make(fakeHealthTracker)
	process := checker.New(ctx, conn, tracker)
	advance := func(response *http.Response) {
		t.Helper()
		conn <- response
		err := testClock.BlockUntilContext(ctx, 1)
		assert.NoError(t, err)
		testClock.Advance(interval)
	}

	// Require two healthy checks to pass
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusOK, Body: http.NoBody})
	assert.Equal(t, Healthy, <-tracker)

	// Require three unheatlhy checks to fail
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	advance(&http.Response{StatusCode: http.StatusBadGateway, Body: http.NoBody})
	close(conn)
	assert.Equal(t, Unhealthy, <-tracker)

	process.Close()
}
