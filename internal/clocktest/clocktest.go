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

	"github.com/bufbuild/go-http-balancer/internal/clock"
	"github.com/jonboulle/clockwork"
)

type FakeClock interface {
	clock.Clock
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
	return fakeClock{clockwork.NewFakeClock().(clockworkFakeClock)} //nolint:forcetypeassert
}

// NewFakeClockAt creates a new FakeClock using Clockwork set to a specific
// time, to provide fully deterministic clock behavior.
func NewFakeClockAt(t time.Time) FakeClock {
	return fakeClock{clockwork.NewFakeClockAt(t).(clockworkFakeClock)} //nolint:forcetypeassert
}

type fakeClock struct {
	clockworkFakeClock
}

var _ FakeClock = fakeClock{nil}

func (f fakeClock) NewTicker(d time.Duration) clock.Ticker {
	return f.clockworkFakeClock.NewTicker(d)
}

func (f fakeClock) NewTimer(d time.Duration) clock.Timer {
	return f.clockworkFakeClock.NewTimer(d)
}

func (f fakeClock) AfterFunc(d time.Duration, fn func()) clock.Timer {
	return f.clockworkFakeClock.AfterFunc(d, fn)
}
