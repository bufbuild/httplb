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
	"net"
	"time"

	"github.com/bufbuild/httplb/attribute"
	"github.com/bufbuild/httplb/internal"
)

// AddressFamilyAffinity is an option that allows control over the preference
// for which addresses to consider when resolving, based on their address
// family.
type AddressFamilyAffinity int

const (
	// AllFamilies will result in all addresses being used, regardless of
	// their address family.
	AllFamilies AddressFamilyAffinity = iota

	// PreferIPv4 will result in only IPv4 addresses being used, if any
	// IPv4 addresses are present. If no IPv4 addresses are resolved, then
	// all addresses will be used.
	PreferIPv4

	// PreferIPv6 will result in only IPv6 addresses being used, if any
	// IPv6 addresses are present. If no IPv6 addresses are resolved, then
	// all addresses will be used.
	PreferIPv6
)

// Resolver is an interface for continuous name resolution.
type Resolver interface {
	// New creates a continuous resolver task for the given target name. When
	// the target is resolved into backend addresses, they are provided to the
	// given callback.
	//
	// As new result sets arrive (since the set of addresses may change over
	// time), the callback may be called repeatedly. Each time, the entire set
	// of addresses should be supplied.
	//
	// The resolver may report errors in addition to or instead of addresses,
	// but it should keep trying to resolve (and watch for changes), even in
	// the face of errors, until it is closed or the given context is cancelled.
	//
	// The refresh channel will receive signals from the client hinting that it
	// may need new results. For example, if the client runs out of healthy
	// hosts, it may call this method in order to try to find more healthy
	// hosts. This is particularly likely to happen during e.g. a rolling
	// deployment, wherein the entire pool of hosts could disappear within the
	// span of a TTL. This may be a no-op. The refresh channel will not be
	// closed until after Close() returns.
	//
	// The Close method on the return value should stop all goroutines and free
	// any resources before returning. After close returns, there should be no
	// subsequent calls to callbacks.
	New(
		ctx context.Context,
		scheme, hostPort string,
		receiver Receiver,
		refresh <-chan struct{},
	) io.Closer
}

// Receiver is a client of a resolver and receives the resolved addresses.
type Receiver interface {
	// OnResolve is called when the set of addresses is resolved. It may be called
	// repeatedly as the set of addresses changes over time. Each call must always
	// supply the full set of resolved addresses (no deltas).
	OnResolve([]Address)
	// OnResolveError is called when resolution encounters an error. This can
	// happen at any time, including after addresses are initially resolved. But
	// the errors may be ignored after initial resolution.
	OnResolveError(error)
}

// ResolveProber is an interface for types that provide single-shot name
// resolution.
type ResolveProber interface {
	// ResolveOnce resolves the given target name once, returning a slice of
	// addresses corresponding to the provided scheme and hostname.
	// The second return value specifies the TTL of the result, or 0 if there
	// is no known TTL value.
	//
	// The resolved addresses should have ports if it is needed for the expected
	// target network. For example, in the common case of TCP, if the provided
	// hostPort string does not contain a port, a default port should be added
	// based on the scheme.
	ResolveOnce(
		ctx context.Context,
		scheme,
		hostPort string,
	) (
		results []Address,
		ttl time.Duration,
		err error,
	)
}

// Address contains a resolved address to a host, and any attributes that may be
// associated with a host/address.
type Address struct {
	// HostPort stores the host:port pair of the resolved address.
	HostPort string

	// Attributes is a collection of arbitrary key/value pairs.
	Attributes attribute.Values
}

// NewDNSResolver creates a new resolver that resolves DNS names.
// You can specify which kind of network addresses to resolve with the network
// parameter, and the resolver will return only IP addresses of the type
// specified by network. The network must be one of "ip", "ip4" or "ip6".
// Note that because net.Resolver does not expose the record TTL values, this
// resolver uses the fixed TTL provided in the ttl parameter. The specified
// address family affinity value can be used to prefer using either IPv4 or
// IPv6 addresses only, in cases where there are both A and AAAA records.
func NewDNSResolver(
	resolver *net.Resolver,
	network string,
	ttl time.Duration,
	affinity AddressFamilyAffinity,
) Resolver {
	return NewPollingResolver(
		&dnsResolveProber{
			resolver: resolver,
			network:  network,
			affinity: affinity,
		},
		ttl,
	)
}

// NewPollingResolver creates a new resolver that polls an underlying
// single-shot resolver whenever the result-set TTL expires. If the underlying
// resolver does not return a TTL with the result-set, defaultTTL is used.
func NewPollingResolver(
	prober ResolveProber,
	defaultTTL time.Duration,
) Resolver {
	return &pollingResolver{
		prober:     prober,
		defaultTTL: defaultTTL,
		clock:      internal.NewRealClock(),
	}
}

type dnsResolveProber struct {
	resolver *net.Resolver
	network  string
	affinity AddressFamilyAffinity
}

func (r *dnsResolveProber) ResolveOnce(
	ctx context.Context,
	scheme, hostPort string,
) ([]Address, time.Duration, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		// Assume this is not a host:port pair.
		// There is no possible better heuristic for this, unfortunately.
		host = hostPort
		switch scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	addresses, err := r.resolver.LookupNetIP(ctx, r.network, host)
	if err != nil {
		return nil, 0, err
	}
	switch r.affinity {
	case AllFamilies:
		break
	case PreferIPv4:
		ip4Addresses := addresses[:0]
		for _, address := range addresses {
			if address.Is4() || address.Is4In6() {
				ip4Addresses = append(ip4Addresses, address)
			}
		}
		if len(ip4Addresses) > 0 {
			addresses = ip4Addresses
		}
	case PreferIPv6:
		ip6Addresses := addresses[:0]
		for _, address := range addresses {
			if address.Is6() {
				ip6Addresses = append(ip6Addresses, address)
			}
		}
		if len(ip6Addresses) > 0 {
			addresses = ip6Addresses
		}
	}
	result := make([]Address, len(addresses))
	for i, address := range addresses {
		result[i].HostPort = net.JoinHostPort(address.Unmap().String(), port)
	}
	return result, 0, nil
}

type pollingResolver struct {
	prober     ResolveProber
	defaultTTL time.Duration
	clock      internal.Clock
}

func (pr *pollingResolver) New(
	ctx context.Context,
	scheme, hostPort string,
	receiver Receiver,
	refresh <-chan struct{},
) io.Closer {
	ctx, cancel := context.WithCancel(ctx)
	res := &pollingResolverTask{
		cancel:     cancel,
		doneSignal: make(chan struct{}),
		refreshCh:  refresh,
		resolver:   pr,
	}
	go res.run(ctx, scheme, hostPort, receiver)
	return res
}

type pollingResolverTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
	refreshCh  <-chan struct{}
	resolver   *pollingResolver
}

func (task *pollingResolverTask) Close() error {
	task.cancel()
	<-task.doneSignal
	return nil
}

func (task *pollingResolverTask) run(ctx context.Context, scheme, hostPort string, receiver Receiver) {
	defer close(task.doneSignal)
	defer task.cancel()

	timer := task.resolver.clock.NewTimer(0)
	if !timer.Stop() {
		<-timer.Chan()
	}

	for {
		addresses, ttl, err := task.resolver.prober.ResolveOnce(ctx, scheme, hostPort)
		if err != nil {
			receiver.OnResolveError(err)
		} else {
			receiver.OnResolve(addresses)
		}
		// TODO: exponential backoff on error
		// TODO: should exponential backoff override ResolveNow?

		if ttl == 0 {
			ttl = task.resolver.defaultTTL
		}
		timer.Reset(ttl)

		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.Chan()
			}
			return
		case <-task.refreshCh:
			// We still want to drain the timer in this case:
			// > Reset should be invoked only on stopped or expired timers
			// > with drained channels.
			// https://pkg.go.dev/time#Timer.Reset
			if !timer.Stop() {
				<-timer.Chan()
			}
			// Continue.
		case <-timer.Chan():
			// Continue.
		}
	}
}
