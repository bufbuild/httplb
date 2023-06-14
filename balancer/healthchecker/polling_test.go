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

package healthchecker_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/bufbuild/go-http-balancer/balancer/balancertesting"
	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/balancer/healthchecker"
	"github.com/bufbuild/go-http-balancer/resolver"
	"github.com/stretchr/testify/assert"
)

type fakeHealthTracker func(conn conn.Conn, health healthchecker.HealthState)

func (f fakeHealthTracker) UpdateHealthState(conn conn.Conn, health healthchecker.HealthState) {
	f(conn, health)
}

type fakeConn struct {
	conn.Conn
	response *http.Response
}

func (f *fakeConn) RoundTrip(*http.Request, func()) (*http.Response, error) {
	return f.response, nil
}

func expectHealth(t *testing.T, expected healthchecker.HealthState, done func()) healthchecker.HealthTracker { //nolint:thelper
	return fakeHealthTracker(func(conn conn.Conn, health healthchecker.HealthState) {
		assert.Equal(t, expected, health)
		done()
	})
}

func TestPollingChecker(t *testing.T) {
	t.Parallel()

	pool := balancertesting.NewFakeConnPool()
	newConn, _ := pool.NewConn(resolver.Address{})
	checker := healthchecker.NewPollingChecker(healthchecker.PollingCheckerConfig{}, healthchecker.NewSimpleProber("/"))

	// Unhealthy (HTTP error)
	waitGroup := new(sync.WaitGroup)
	waitGroup.Add(1)
	process := checker.New(
		context.Background(),
		newConn,
		expectHealth(t, healthchecker.Unhealthy, waitGroup.Done),
	)
	waitGroup.Wait()
	process.Close()

	// Unhealthy (HTTP 500)
	waitGroup = new(sync.WaitGroup)
	waitGroup.Add(1)
	response := &http.Response{StatusCode: http.StatusInternalServerError, Body: http.NoBody}
	process = checker.New(
		context.Background(),
		&fakeConn{Conn: newConn, response: response},
		expectHealth(t, healthchecker.Unhealthy, waitGroup.Done),
	)
	waitGroup.Wait()
	process.Close()

	// Healthy (HTTP 200)
	waitGroup = new(sync.WaitGroup)
	waitGroup.Add(1)
	response = &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
	process = checker.New(
		context.Background(),
		&fakeConn{Conn: newConn, response: response},
		expectHealth(t, healthchecker.Healthy, waitGroup.Done),
	)
	waitGroup.Wait()
	process.Close()
}
