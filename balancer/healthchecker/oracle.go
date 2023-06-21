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
	"math"

	"github.com/bufbuild/go-http-balancer/balancer/conn"
	"github.com/bufbuild/go-http-balancer/internal"
)

// The UsabilityOracle decides which connections are usable. Given the set
// of all connections and an accessor, for querying the health state of a
// particular connection, it returns a slice of "usable" connections.
//
// Implementations of this function must NOT call the given accessor function
// after they return.
type UsabilityOracle func(conn.Connections, func(conn.Conn) HealthState) []conn.Conn

// NewOracle returns an oracle that considers connections to be usable if
// they are the given state or healthier.
//
// The order of states, from healthiest to least healthy is:
//
//	Healthy, Unknown, Degraded, Unhealthy
func NewOracle(threshold HealthState, opts ...NewOracleOption) UsabilityOracle {
	var options oracleOptions
	for _, opt := range opts {
		opt.apply(&options)
	}
	if !options.preferHealthier {
		// Simple oracle that includes everything that is the given threshold or less
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
	rnd := internal.NewLockedRand()
	// If preferring healthier, then we first need to group connections by state and
	// then consider them in order, best to worst state.
	return func(allCons conn.Connections, state func(conn.Conn) HealthState) []conn.Conn {
		length := allCons.Len()
		connsByState := map[HealthState][]conn.Conn{}
		for i := 0; i < length; i++ {
			connection := allCons.Get(i)
			connState := state(connection)
			connsByState[connState] = append(connsByState[connState], connection)
		}

		minConns := options.minimumCount
		if minPctConns := int(math.Round(options.minimumPercent * float64(length) / 100)); minPctConns > minConns {
			minConns = minPctConns
		}

		var results []conn.Conn
		for _, candidateState := range statesInOrder {
			if candidateState > threshold {
				break
			}
			candidateConns := connsByState[candidateState]
			if len(candidateConns) == 0 {
				continue
			}

			if len(results) == 0 {
				if len(candidateConns) >= minConns {
					// Healthiest connections meet the minimum number, so no more searching.
					return candidateConns
				}
				results = candidateConns
				continue
			}

			if len(results)+len(candidateConns) > minConns {
				// We don't need all of these candidates. Randomly select enough
				// to reach the configured minimum.
				rnd.Shuffle(len(candidateConns), func(i, j int) {
					candidateConns[i], candidateConns[j] = candidateConns[j], candidateConns[i]
				})
				return append(results, candidateConns[:minConns-len(results)]...)
			}

			results = append(results, candidateConns...)
			if len(results) == minConns {
				return results
			}
		}
		return results
	}
}

// NewOracleOption is an option that can be supplied to NewOracle to control
// the behavior of the resulting UsabilityOracle.
type NewOracleOption interface {
	apply(*oracleOptions)
}

// WithMinimumConnections returns an option that will cause an oracle to prefer
// healthier connections and compute a set of usable connections with a minimum
// size. Without this option, all connections in any allowed state are considered
// usable. With this option, only healthier connections will be considered usable.
// If the minimum size is reached, then other less healthy connections will be
// ignored and not considered usable.
//
// The numConns, if not zero, is an absolute minimum number of connections. So
// if this were set to 3 and the oracle's threshold were Unknown, but there were
// only 2 Healthy connections, one Unknown connection will be randomly selected
// to include in the usable set.
//
// The percentConns, if not zero, is a percentage (from 0 to 100) of connections
// to include. So if the total number of connections is 20 and this value is 10.0,
// then the oracle requires at least 2 usable connections (10% * 20 == 2). So if
// the threshold were Unknown and there is only 1 healthy connection, one Unknown
// connection would be randomly included.
//
// If both numConns and percentConns are specified, the minimum number of
// connections will be the maximum of the two. The actual number of connections
// selected by the oracle can still be less than the minimum because (1) the
// minimum number configured could be greater than the actual number of
// connections, and (2) the oracle will never include a connection whose health
// state is worse than its configured threshold.
//
// A simple option like WithMinimumConnections(1, 0) will always consider only
// the healthiest connections, even if there's only one in that healthiest
// state.
//
// The order of states, from healthiest to least healthy is:
//
//	Healthy, Unknown, Degraded, Unhealthy
func WithMinimumConnections(numConns int, percentConns float64) NewOracleOption {
	return oracleOptionFunc(func(options *oracleOptions) {
		options.preferHealthier = true
		options.minimumCount = numConns
		options.minimumPercent = percentConns
	})
}

type oracleOptions struct {
	preferHealthier bool
	minimumCount    int
	minimumPercent  float64
}

type oracleOptionFunc func(*oracleOptions)

func (o oracleOptionFunc) apply(options *oracleOptions) {
	o(options)
}
