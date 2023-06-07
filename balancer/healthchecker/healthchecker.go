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

	"github.com/bufbuild/go-http-balancer/balancer/conn"
)

//nolint:gochecknoglobals
var (
	DefaultUsabilityOracle = func(allCons conn.Connections, state func(conn.Conn) HealthState) []conn.Conn {
		length := allCons.Len()
		usable := make([]conn.Conn, 0, length)
		for i := 0; i < length; i++ {
			connection := allCons.Get(i)
			if state(connection) == HealthStateHealthy {
				usable = append(usable, connection)
			}
		}
		return usable
	}

	NoOpChecker Checker = noOpChecker{}
)

type HealthState int

const (
	HealthStateUnknown = HealthState(iota)
	HealthStateHealthy
	HealthStateDegraded
	HealthStateUnhealthy
)

// Checker manages health checks. It creates new checking processes as new
// connections are created. Each process can be independently stopped.
type Checker interface {
	// New creates a new health-checking process for the given connection.
	// The process should release resources (including stopping any goroutines)
	// when the given context is cancelled or the returned value is closed.
	//
	// The process should use the HealthTracker to record the results of the
	// health checks.
	New(context.Context, conn.Conn, HealthTracker) io.Closer
}

type HealthTracker interface {
	UpdateHealthState(conn.Conn, HealthState)
}

// The UsabilityOracle decides which connections are usable. Given the set
// of all connections and an accessor, for querying the health state of a
// particular connection, it returns a slice of "usable" connections.
type UsabilityOracle func(conn.Connections, func(conn.Conn) HealthState) []conn.Conn

type noOpChecker struct{}

func (n noOpChecker) New(_ context.Context, c conn.Conn, tracker HealthTracker) io.Closer {
	// the no-op checker assumes all connections are healthy
	tracker.UpdateHealthState(c, HealthStateHealthy)
	return noOpCloser{}
}

type noOpCloser struct{}

func (n noOpCloser) Close() error {
	return nil
}
