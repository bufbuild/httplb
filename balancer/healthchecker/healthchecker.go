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
	"fmt"
	"io"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
)

//nolint:gochecknoglobals
var (
	// NoOpChecker is a checker implementation that does nothing. It never updates
	// the health states of any connections and uses no resources.
	NoOpChecker Checker = noOpChecker{}
)

// HealthState represents the state of a connection. Their natural ordering is
// for "better" states to be before "worse" states. So Healthy is the lowest
// value and Unhealthy is the highest.
type HealthState int

const (
	Healthy   = HealthState(-1)
	Unknown   = HealthState(0)
	Degraded  = HealthState(1)
	Unhealthy = HealthState(2)
)

func (s HealthState) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Degraded:
		return "degraded"
	case Unhealthy:
		return "unhealthy"
	case Unknown:
		return "unknown"
	default:
		return fmt.Sprintf("HealthState(%d)", s)
	}
}

// Checker manages health checks. It creates new checking processes as new
// connections are created. Each process can be independently stopped.
type Checker interface {
	// New creates a new health-checking process for the given connection.
	// The process should release resources (including stopping any goroutines)
	// when the given context is cancelled or the returned value is closed.
	//
	// The process should use the HealthTracker to record the results of the
	// health checks. It should NOT directly call HealthTracker from this
	// method implementation. If the implementation wants to immediately
	// update health state, it must do so from a goroutine.
	New(context.Context, conn.Conn, HealthTracker) io.Closer
}

type HealthTracker interface {
	UpdateHealthState(conn.Conn, HealthState)
}

// The UsabilityOracle decides which connections are usable. Given the set
// of all connections and an accessor, for querying the health state of a
// particular connection, it returns a slice of "usable" connections.
//
// Implementations of this function must NOT use the given accessor function
// after they return.
type UsabilityOracle func(conn.Connections, func(conn.Conn) HealthState) []conn.Conn

// DefaultUsabilityOracle returns an oracle that considers connections to be
// usable if they are the given state or better.
func DefaultUsabilityOracle(threshold HealthState) UsabilityOracle {
	return func(allConns conn.Connections, state func(conn.Conn) HealthState) []conn.Conn {
		length := allConns.Len()
		usable := make([]conn.Conn, 0, length)
		for i := 0; i < length; i++ {
			connection := allConns.Get(i)
			if state(connection) <= threshold {
				usable = append(usable, connection)
			}
		}
		return usable
	}
}

type noOpChecker struct{}

func (n noOpChecker) New(_ context.Context, _ conn.Conn, _ HealthTracker) io.Closer {
	return noOpCloser{}
}

type noOpCloser struct{}

func (n noOpCloser) Close() error {
	return nil
}
