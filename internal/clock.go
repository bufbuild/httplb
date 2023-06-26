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

package internal

import "time"

// Clock is an interface that is compatible with the jonboulle/clockwork package.
// The intent is that clockwork package only be a dependency for tests, not for
// non-test code.
type Clock interface {
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
	Now() time.Time
	Since(t time.Time) time.Duration
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
	AfterFunc(d time.Duration, f func()) Timer
}

// Ticker is an interface covering the behavior of a [time.Ticker].
type Ticker interface {
	Chan() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

// Timer is an interface covering the behavior of a [time.Timer].
type Timer interface {
	Chan() <-chan time.Time
	Reset(d time.Duration) bool
	Stop() bool
}

// NewRealClock returns a Clock implementation where all methods
// delegate to the corresponding function in the [time] package.
func NewRealClock() Clock {
	return realClock{}
}

type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (realClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{time.NewTicker(d)}
}

func (realClock) NewTimer(d time.Duration) Timer {
	return realTimer{time.NewTimer(d)}
}

func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return realTimer{time.AfterFunc(d, f)}
}

type realTicker struct{ *time.Ticker }

func (r realTicker) Chan() <-chan time.Time {
	return r.C
}

type realTimer struct{ *time.Timer }

func (r realTimer) Chan() <-chan time.Time {
	return r.C
}
