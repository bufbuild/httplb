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
	"fmt"
	"math/rand"
	"testing"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/stretchr/testify/require"
)

func TestNewOracle_Basic(t *testing.T) {
	t.Parallel()

	conns := make([]conn.Conn, 20)
	states := make(map[conn.Conn]HealthState, 20)
	for i := range conns {
		conns[i] = &fakeConn{}
		// first five will be healthy, then unknown, then degraded, then unhealthy
		states[conns[i]] = HealthState(i/5 - 1)
	}
	rand.Shuffle(len(conns), func(i, j int) {
		conns[i], conns[j] = conns[j], conns[i]
	})

	testCases := []struct {
		threshold   HealthState
		expectCount int
	}{
		{
			threshold:   Healthy,
			expectCount: 5,
		},
		{
			threshold:   Unknown,
			expectCount: 10,
		},
		{
			threshold:   Degraded,
			expectCount: 15,
		},
		{
			threshold:   Unhealthy,
			expectCount: 20,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.threshold.String(), func(t *testing.T) {
			t.Parallel()
			oracle := NewOracle(testCase.threshold)
			usable := oracle(conn.ConnectionsFromSlice(conns), func(c conn.Conn) HealthState {
				return states[c]
			})
			require.Equal(t, testCase.expectCount, len(usable))
			checkConns(t, usable, states, testCase.threshold)
		})
	}
}

func TestNewOracle_MinimumCount(t *testing.T) {
	t.Parallel()

	conns := make([]conn.Conn, 20)
	states := make(map[conn.Conn]HealthState, 20)
	for i := range conns {
		conns[i] = &fakeConn{}
		// first five will be healthy, then unknown, then degraded, then unhealthy
		states[conns[i]] = HealthState(i/5 - 1)
	}
	rand.Shuffle(len(conns), func(i, j int) {
		conns[i], conns[j] = conns[j], conns[i]
	})

	testCases := []struct {
		threshold    HealthState
		minCount     int
		minPercent   float64
		expectCounts []int
	}{
		{
			threshold:    Healthy,
			minCount:     1,
			expectCounts: []int{5},
		},
		{
			threshold:    Healthy,
			minCount:     3,
			minPercent:   10, // 10%*20 = 2
			expectCounts: []int{5},
		},
		{
			threshold:    Healthy,
			minCount:     3,
			minPercent:   25, // 25%*20 = 5
			expectCounts: []int{5},
		},
		{
			threshold:    Healthy,
			minCount:     10,
			expectCounts: []int{5}, // only five healthy
		},
		{
			threshold:    Unknown,
			minCount:     5,
			expectCounts: []int{5},
		},
		{
			threshold:    Unknown,
			minCount:     6,
			expectCounts: []int{5, 1},
		},
		{
			threshold:    Unknown,
			minCount:     10,
			expectCounts: []int{5, 5},
		},
		{
			threshold:    Unknown,
			minPercent:   60,          // 60%*20 = 12
			expectCounts: []int{5, 5}, // only 10 that are unknown or better
		},
		{
			threshold:    Degraded,
			minCount:     3,
			expectCounts: []int{5},
		},
		{
			threshold:    Degraded,
			minCount:     6,
			expectCounts: []int{5, 1},
		},
		{
			threshold:    Degraded,
			minPercent:   60, // 60%*20 = 12
			expectCounts: []int{5, 5, 2},
		},
		{
			threshold:    Unhealthy,
			minPercent:   75, // 75%*20 = 15
			expectCounts: []int{5, 5, 5},
		},
		{
			threshold:    Unhealthy,
			minCount:     4,
			expectCounts: []int{5},
		},
		{
			threshold:    Unhealthy,
			minCount:     7,
			expectCounts: []int{5, 2},
		},
		{
			threshold:    Unhealthy,
			minCount:     18,
			expectCounts: []int{5, 5, 5, 3},
		},
		{
			threshold:    Unhealthy,
			minCount:     100,
			expectCounts: []int{5, 5, 5, 5},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(fmt.Sprintf("%v (%d,%f)", testCase.threshold, testCase.minCount, testCase.minPercent), func(t *testing.T) {
			t.Parallel()
			oracle := NewOracle(testCase.threshold, WithMinimumConnections(testCase.minCount, testCase.minPercent))
			usable := oracle(conn.ConnectionsFromSlice(conns), func(c conn.Conn) HealthState {
				return states[c]
			})
			var totalCount int
			for _, count := range testCase.expectCounts {
				totalCount += count
			}
			require.Equal(t, totalCount, len(usable))
			checkConns(t, usable, states, testCase.threshold)
			// check counts by each health state
			actual := map[HealthState]int{}
			for _, c := range usable {
				actual[states[c]]++
			}
			for i := range testCase.expectCounts {
				state := statesInOrder[i]
				require.Equal(t, testCase.expectCounts[i], actual[state], "wrong number of connections in state %v", state)
			}
		})
	}
}

func checkConns(t *testing.T, usable []conn.Conn, states map[conn.Conn]HealthState, threshold HealthState) {
	t.Helper()
	// Make sure none of the connections have a state that is worse than threshold.
	for _, c := range usable {
		require.LessOrEqual(t, states[c], threshold)
	}
	// And make sure there are no duplicates
	set := conn.SetFromSlice(usable)
	require.Equal(t, len(usable), len(set))
}

type fakeConn struct {
	conn.Conn
}
