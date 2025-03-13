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
	"io"

	"github.com/bufbuild/httplb/conn"
)

//nolint:gochecknoglobals
var (
	// NopChecker is a checker implementation that does nothing. It assumes
	// the state of all connections is healthy.
	NopChecker Checker = nopChecker{}
)

// Checker manages health checks. It creates new checking processes as new
// connections are created. Each process can be independently stopped.
type Checker interface {
	// New creates a new health-checking process for the given connection.
	// The process should release resources (including stopping any goroutines)
	// when the given context is cancelled or the returned value is closed.
	//
	// The process should use the Tracker to record the results of the
	// health checks. It should NOT directly call Tracker from this
	// method implementation. If the implementation wants to immediately
	// update health state, it must do so from a goroutine.
	New(context.Context, conn.Conn, Tracker) io.Closer
}

// Tracker represents an object that tracks the health state of various connections.
// This is the interface through which a Checker communicates state updates.
type Tracker interface {
	UpdateHealthState(conn.Conn, State)
}

type nopChecker struct{}

func (n nopChecker) New(_ context.Context, conn conn.Conn, tracker Tracker) io.Closer {
	go tracker.UpdateHealthState(conn, StateHealthy)
	return nopCloser{}
}

type nopCloser struct{}

func (n nopCloser) Close() error {
	return nil
}
