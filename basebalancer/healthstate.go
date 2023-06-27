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

package basebalancer

import "fmt"

// HealthState represents the state of a connection. Their natural ordering is
// for "better" states to be before "worse" states. So HealthyState is the lowest
// value and UnhealthyState is the highest.
type HealthState int

const (
	HealthyState   = HealthState(-1)
	UnknownState   = HealthState(0)
	DegradedState  = HealthState(1)
	UnhealthyState = HealthState(2)
)

//nolint:gochecknoglobals
var (
	statesInOrder = []HealthState{HealthyState, UnknownState, DegradedState, UnhealthyState}
)

func (s HealthState) String() string {
	switch s {
	case HealthyState:
		return "healthy"
	case DegradedState:
		return "degraded"
	case UnhealthyState:
		return "unhealthy"
	case UnknownState:
		return "unknown"
	default:
		return fmt.Sprintf("HealthState(%d)", s)
	}
}
