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

// Package clocktest exists to allow interoperability with our Clock interface
// and the Clockwork FakeClock. Compatibility between Go interfaces is shallow,
// since function signatures containing other interfaces within an interface
// will be compared by their exact (nominal) type. Therefore, for the three
// Clock functions returning Timer or Ticker, we need to wrap those into
// functions returning the Clockwork version of the interface instead.
//
// We also expose BlockUntilContext directly for convenience, as it is not
// exposed in Clockwork FakeClock.
package clocktest

import (
	"context"
	"time"

	"github.com/bufbuild/httplb/internal"
	"github.com/jonboulle/clockwork"
)

// FakeClock provides an interface for a clock which can be manually advanced
// through time. For more information, see the documentation for Clockwork.
// https://pkg.go.dev/github.com/jonboulle/clockwork
type FakeClock interface {
	internal.Clock
	Advance(d time.Duration)
	BlockUntil(waiters int)
	BlockUntilContext(ctx context.Context, n int) error
}

type clockworkFakeClock interface {
	clockwork.FakeClock
	BlockUntilContext(ctx context.Context, n int) error
}

// NewFakeClock creates a new FakeClock using Clockwork.
func NewFakeClock() FakeClock {
	return fakeClock{clockwork.NewFakeClock().(clockworkFakeClock)} //nolint:forcetypeassert,errcheck
}

// NewFakeClockAt creates a new FakeClock using Clockwork set to a specific
// time, to provide fully deterministic clock behavior.
func NewFakeClockAt(t time.Time) FakeClock {
	return fakeClock{clockwork.NewFakeClockAt(t).(clockworkFakeClock)} //nolint:forcetypeassert,errcheck
}

// fakeClock wraps the clockwork.FakeClock interface and adapts it to the
// clock.Clock/FakeClock interface. It has two purposes:
//   - To expose BlockUntilContext, which is not exposed in clockwork.FakeClock
//   - To adapt the return types of clockwork.Clock methods that return other
//     interfaces. These function signatures are not compatible by Go rules,
//     even though structurally the underlying interfaces are identical.
type fakeClock struct {
	clockworkFakeClock
}

var _ FakeClock = fakeClock{}

// NewTicker implements clock.Clock by re-boxing the clockwork.Ticker returned
// by clockwork.Clock.NewTicker as a clock.Ticker. See package comment for more
// information on why this is necessary.
func (f fakeClock) NewTicker(d time.Duration) internal.Ticker {
	return f.clockworkFakeClock.NewTicker(d)
}

// NewTimer implements clock.Clock by re-boxing the clockwork.Timer returned by
// clockwork.Clock.NewTimer as a clock.Timer. See package comment for more
// information on why this is necessary.
func (f fakeClock) NewTimer(d time.Duration) internal.Timer {
	t := f.clockworkFakeClock.NewTimer(d)
	if d == 0 {
		// Here we reproduce the pre-1.23 timers behavior since jonboulle/clockwork still have not fixed this yet,
		// see the issue: https://github.com/jonboulle/clockwork/issues/98
		if !t.Stop() {
			<-t.Chan()
		}
	}
	return t
}

// AfterFunc implements clock.Clock by re-boxing the clockwork.Timer returned by
// clockwork.Clock.AfterFunc as a clock.Timer. See package comment for more
// information on why this is necessary.
func (f fakeClock) AfterFunc(d time.Duration, fn func()) internal.Timer {
	return f.clockworkFakeClock.AfterFunc(d, fn)
}
