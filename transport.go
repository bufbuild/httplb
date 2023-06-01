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

package httpbalancer

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// TODO: add this info (whatever's relevant/user-visible) to doc.go

// This package uses a hierarchy with three-layers of transports:
//
// 1. mainTransport: This is the "top" of the hierarchy and is the
//    http.RoundTripper used by NewClient. This transport manages a
//    collection of other transports, grouped by URL scheme and
//    hostname:port.
// 2. transportPool: This provides a pool of transports for a single
//    URL schema and hostname:port. This is the layer in which most
//    of the features are implemented: name resolution, health
//    checking, and load balancing (sub-setting and picking). Each
//    transportPool manages a pool of lower-level transports, each
//    to a potentially different resolved address.
// 3. http.RoundTripper: The bottom of the hierarchy is a normal
//    round tripper, such as *http.Transport. This represents a
//    logical connection to a single resolved address. It may
//    actually represent more than one physical connection, like
//    when using HTTP 1.1 and multiple active requests are made
//    to the same address.

type mainTransport struct {
	mu    sync.RWMutex
	pools map[target]*transportPool
}

func newTransport(_ *clientOptions) *mainTransport {
	// TODO
	return &mainTransport{
		pools: map[target]*transportPool{},
	}
}

func (m *mainTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	dest := target{scheme: request.URL.Scheme, hostPort: request.URL.Host}
	pool := m.getPool(dest)
	return pool.RoundTrip(request)
}

func (m *mainTransport) getPool(dest target) http.RoundTripper {
	m.mu.RLock()
	pool := m.pools[dest]
	m.mu.RUnlock()

	if pool != nil {
		return pool
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// double-check in case pool was added while upgrading lock
	pool = m.pools[dest]
	if pool != nil {
		return pool
	}

	pool = newTransportPool(dest.scheme, dest.hostPort, simpleFactory{})
	// TODO: evict unused/idle entries from map (maybe accept "warm set"
	//       in config, for targets to keep warm, that never get evicted)
	m.pools[dest] = pool
	return pool
}

type target struct {
	scheme   string
	hostPort string
}

type transportPool struct {
	scheme              string
	roundTripperFactory RoundTripperFactory
	resolved            <-chan struct{}

	mu sync.RWMutex
	// TODO: we may need better data structures than this to
	//       support "picker" interface and efficient selection
	pool  map[string][]connection
	conns []connection
}

func newTransportPool(scheme, hostPort string, factory RoundTripperFactory) *transportPool {
	// TODO: implement me!!
	resolved := make(chan struct{})

	pool := &transportPool{
		scheme:              scheme,
		roundTripperFactory: factory,
		resolved:            resolved,
		pool:                map[string][]connection{},
	}

	go func() {
		// TODO: real resolution :P
		defer close(resolved)
		roundTripper := factory.New(RoundTripperOptions{})
		conn := connection{
			addr: hostPort,
			conn: roundTripper,
		}
		pool.mu.Lock()
		pool.conns = []connection{conn}
		pool.pool[hostPort] = []connection{conn}
		pool.mu.Unlock()
	}()

	return pool
}

func (t *transportPool) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.getRoundTripper().RoundTrip(request)
}

func (t *transportPool) getRoundTripper() http.RoundTripper {
	t.mu.RLock()
	conns := t.conns
	t.mu.RUnlock()

	if conns == nil {
		// NB: if resolver returns no addresses, that should
		//     be represented via empty but non-nil conns...
		<-t.resolved
		t.mu.RLock()
		conns = t.conns
		t.mu.RUnlock()
	}

	if len(conns) == 0 {
		return noAddressRoundTripper{}
	}
	return t.pick(conns)
}

func (t *transportPool) pick(conns []connection) http.RoundTripper {
	// TODO implement me
	return conns[0].conn
}

type connection struct {
	addr string
	conn http.RoundTripper
}

type RoundTripperFactory interface {
	New(RoundTripperOptions) http.RoundTripper
}

type RoundTripperOptions struct {
	// TODO
}

type simpleFactory struct{}

func (s simpleFactory) New(_ RoundTripperOptions) http.RoundTripper {
	// TODO: make this real (below is just copied from http.DefaultTransport for now...)
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

type noAddressRoundTripper struct{}

func (n noAddressRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	// TODO: probably want more useful info in this error message
	return nil, fmt.Errorf("failed to resolve hostname")
}
