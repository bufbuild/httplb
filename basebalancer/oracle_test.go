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

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/bufbuild/httplb/balancer"
	"github.com/stretchr/testify/require"
)

func TestNewOracle_Basic(t *testing.T) {
	t.Parallel()

	conns := make([]balancer.Conn, 20)
	states := make(map[balancer.Conn]HealthState, 20)
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
			threshold:   HealthyState,
			expectCount: 5,
		},
		{
			threshold:   UnknownState,
			expectCount: 10,
		},
		{
			threshold:   DegradedState,
			expectCount: 15,
		},
		{
			threshold:   UnhealthyState,
			expectCount: 20,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.threshold.String(), func(t *testing.T) {
			t.Parallel()
			oracle := NewOracle(testCase.threshold)
			usable := oracle(balancer.ConnsFromSlice(conns), func(c balancer.Conn) HealthState {
				return states[c]
			})
			require.Equal(t, testCase.expectCount, len(usable))
			checkConns(t, usable, states, testCase.threshold)
		})
	}
}

func TestNewOracle_MinimumCount(t *testing.T) {
	t.Parallel()

	conns := make([]balancer.Conn, 20)
	states := make(map[balancer.Conn]HealthState, 20)
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
			threshold:    HealthyState,
			minCount:     1,
			expectCounts: []int{5},
		},
		{
			threshold:    HealthyState,
			minCount:     3,
			minPercent:   10, // 10%*20 = 2
			expectCounts: []int{5},
		},
		{
			threshold:    HealthyState,
			minCount:     3,
			minPercent:   25, // 25%*20 = 5
			expectCounts: []int{5},
		},
		{
			threshold:    HealthyState,
			minCount:     10,
			expectCounts: []int{5}, // only five healthy
		},
		{
			threshold:    UnknownState,
			minCount:     5,
			expectCounts: []int{5},
		},
		{
			threshold:    UnknownState,
			minCount:     6,
			expectCounts: []int{5, 1},
		},
		{
			threshold:    UnknownState,
			minCount:     10,
			expectCounts: []int{5, 5},
		},
		{
			threshold:    UnknownState,
			minPercent:   60,          // 60%*20 = 12
			expectCounts: []int{5, 5}, // only 10 that are unknown or better
		},
		{
			threshold:    DegradedState,
			minCount:     3,
			expectCounts: []int{5},
		},
		{
			threshold:    DegradedState,
			minCount:     6,
			expectCounts: []int{5, 1},
		},
		{
			threshold:    DegradedState,
			minPercent:   60, // 60%*20 = 12
			expectCounts: []int{5, 5, 2},
		},
		{
			threshold:    UnhealthyState,
			minPercent:   75, // 75%*20 = 15
			expectCounts: []int{5, 5, 5},
		},
		{
			threshold:    UnhealthyState,
			minCount:     4,
			expectCounts: []int{5},
		},
		{
			threshold:    UnhealthyState,
			minCount:     7,
			expectCounts: []int{5, 2},
		},
		{
			threshold:    UnhealthyState,
			minCount:     18,
			expectCounts: []int{5, 5, 5, 3},
		},
		{
			threshold:    UnhealthyState,
			minCount:     100,
			expectCounts: []int{5, 5, 5, 5},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(fmt.Sprintf("%v (%d,%f)", testCase.threshold, testCase.minCount, testCase.minPercent), func(t *testing.T) {
			t.Parallel()
			oracle := NewOracle(testCase.threshold, WithMinimumConnections(testCase.minCount, testCase.minPercent))
			usable := oracle(balancer.ConnsFromSlice(conns), func(c balancer.Conn) HealthState {
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

func checkConns(t *testing.T, usable []balancer.Conn, states map[balancer.Conn]HealthState, threshold HealthState) {
	t.Helper()
	// Make sure none of the connections have a state that is worse than threshold.
	for _, c := range usable {
		require.LessOrEqual(t, states[c], threshold)
	}
	// And make sure there are no duplicates
	set := balancer.ConnSetFromSlice(usable)
	require.Equal(t, len(usable), len(set))
}

type fakeConn struct {
	balancer.Conn
}
