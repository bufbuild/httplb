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

package resolver

import (
	"context"
	"io"
)

// MinAddresses decorates the given resolver so it sends a set of addresses that has at
// least as many entries as the given minimum. If the given resolver provides a smaller
// set of addresses, it replicates those addresses until the give minimum is reached.
//
// This will cause a client to effectively make redundant connections to the same address
// which is particularly useful when the addresses are virtual IPs, which  actually have
// multiple servers behind them. This is appropriate for environments like Kubernetes
// (which uses virtual IPs for non-headless services) and for use with services that have
// hardware/cloud load balancers in front.
//
// To avoid "hot spotting", where one backend address gets more load than others, this
// always fully replicates the set. So it will always report at least minAddresses, but
// could report nearly twice as many: in the case where the set from the underlying
// resolver has minAddresses-1 entries, this will provide (minAddresses-1)*2 entries.
func MinAddresses(other Resolver, minAddresses int) Resolver {
	return &minAddrsResolver{res: other, min: minAddresses}
}

type minAddrsResolver struct {
	res Resolver
	min int
}

func (m *minAddrsResolver) New(ctx context.Context, scheme, hostPort string, receiver Receiver, refresh <-chan struct{}) io.Closer {
	return m.res.New(ctx, scheme, hostPort, &minAddrsReceiver{rcvr: receiver, min: m.min}, refresh)
}

type minAddrsReceiver struct {
	rcvr Receiver
	min  int
}

func (m *minAddrsReceiver) OnResolve(addresses []Address) {
	if len(addresses) >= m.min || len(addresses) == 0 {
		// Already enough addresses; OR zero addresses, in which case, no amount of replication can help.
		m.rcvr.OnResolve(addresses)
		return
	}
	multiplier := m.min / len(addresses)
	if len(addresses)*multiplier < m.min {
		multiplier++ // div rounded down
	}
	scaledAddrs := make([]Address, 0, len(addresses)*multiplier)
	for i := 0; i < multiplier; i++ {
		scaledAddrs = append(scaledAddrs, addresses...)
	}
	m.rcvr.OnResolve(scaledAddrs)
}

func (m *minAddrsReceiver) OnResolveError(err error) {
	m.rcvr.OnResolveError(err)
}
