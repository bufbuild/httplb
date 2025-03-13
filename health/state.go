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

import "fmt"

// State represents the state of a connection. Their natural ordering is
// for "better" states to be before "worse" states. So StateHealthy is the lowest
// value and StateUnhealthy is the highest.
type State int

const (
	StateHealthy   = State(-1)
	StateUnknown   = State(0)
	StateDegraded  = State(1)
	StateUnhealthy = State(2)
)

func (s State) String() string {
	switch s {
	case StateHealthy:
		return "healthy"
	case StateDegraded:
		return "degraded"
	case StateUnhealthy:
		return "unhealthy"
	case StateUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("State(%d)", s)
	}
}
