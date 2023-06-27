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
	"net"
	"time"

	"github.com/bufbuild/httplb/attrs"
	"github.com/bufbuild/httplb/internal"
)

// Factory is an interface for creating continuous name resolvers.
type Factory interface {
	// New creates a continuous resolver for the given target name. When the
	// target is resolved into backend addresses, they are provided to the given
	// callback.
	//
	// As new result sets arrive (since the set of addresses may change over
	// time), the callback may be called repeatedly. Each time, the entire set
	// of addresses should be supplied.
	//
	// The resolver may report errors in addition to or instead of addresses,
	// but it should keep trying to resolve (and watch for changes), even in
	// the face of errors, until it is closed or the given context is cancelled.
	New(ctx context.Context, scheme, hostPort string, receiver Receiver) Resolver
}

// Resolver is an interface for continuous name resolution.
type Resolver interface {
	// ResolveNow is a hint sent by the balancer that it may need new results.
	// For example, if the balancer runs out of healthy hosts, it may call this
	// method in order to try to find more healthy hosts. This is particularly
	// likely to happen during e.g. a rolling deployment, wherein the entire
	// pool of hosts could disappear within the span of a TTL.
	//
	// This may be a no-op if it is not possible to "refresh" the result-set,
	// for a given resolver. This method is considered to merely hint to the
	// resolver that it should actively search for new results if it can.
	//
	// This method should not be called after calling Close().
	ResolveNow()

	// Close closes the current resolution process. This will free any resources
	// (including any background goroutines) before returning. No subsequent
	// calls to callback should be made after Close returns. The resolve process
	// should also initiate shutting down if/when the given context is
	// cancelled.
	//
	// Close should always be called, even when the context passed to the
	// resolver is cancelled.
	Close() error
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
	Attributes attrs.Attributes
}

// NewDNSResolverFactory creates a new resolver that resolves DNS names.
// You can specify which kind of network addresses to resolve with the network
// parameter, and the resolver will return only IP addresses of the type
// specified by network. The network must be one of "ip", "ip4" or "ip6".
// Note that because net.Resolver does not expose the record TTL values, this
// resolver uses the fixed TTL provided in the ttl parameter.
func NewDNSResolverFactory(
	resolver *net.Resolver,
	network string,
	ttl time.Duration,
) Factory {
	return NewPollingResolverFactory(
		&dnsResolveProber{
			resolver: resolver,
			network:  network,
		},
		ttl,
	)
}

// NewPollingResolverFactory creates a new resolver that polls an underlying
// single-shot resolver whenever the result-set TTL expires. If the underlying
// resolver does not return a TTL with the result-set, defaultTTL is used.
func NewPollingResolverFactory(
	prober ResolveProber,
	defaultTTL time.Duration,
) Factory {
	return &pollingResolverFactory{
		prober:     prober,
		defaultTTL: defaultTTL,
		clock:      internal.NewRealClock(),
	}
}

type dnsResolveProber struct {
	resolver *net.Resolver
	network  string
}

func (r *dnsResolveProber) ResolveOnce(
	ctx context.Context,
	_, hostPort string,
) ([]Address, time.Duration, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		// Assume this is not a host:port pair.
		// There is no possible better heuristic for this, unfortunately.
		host = hostPort
		port = ""
	}
	if ip := net.ParseIP(host); ip != nil {
		// given host is already a resolved IP address, so return as is
		return []Address{{HostPort: hostPort}}, 0, nil
	}
	addresses, err := r.resolver.LookupNetIP(ctx, r.network, host)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Address, len(addresses))
	for i, address := range addresses {
		if port != "" {
			result[i].HostPort = net.JoinHostPort(address.String(), port)
		} else {
			result[i].HostPort = address.String()
		}
	}
	return result, 0, nil
}

type pollingResolverFactory struct {
	prober     ResolveProber
	defaultTTL time.Duration
	clock      internal.Clock
}

func (f *pollingResolverFactory) New(
	ctx context.Context,
	scheme, hostPort string,
	receiver Receiver,
) Resolver {
	ctx, cancel := context.WithCancel(ctx)
	res := &pollingResolver{
		cancel:     cancel,
		doneSignal: make(chan struct{}),
		refreshCh:  make(chan struct{}, 1),
		factory:    f,
	}
	go res.run(ctx, scheme, hostPort, receiver)
	return res
}

type pollingResolver struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
	refreshCh  chan struct{}
	factory    *pollingResolverFactory
}

func (res *pollingResolver) ResolveNow() {
	select {
	case res.refreshCh <- struct{}{}:
	default:
	}
}

func (res *pollingResolver) Close() error {
	res.cancel()
	close(res.refreshCh)
	<-res.doneSignal
	return nil
}

func (res *pollingResolver) run(ctx context.Context, scheme, hostPort string, receiver Receiver) {
	defer close(res.doneSignal)
	defer res.cancel()
	defer func() {
		// Drain the refresh channel. This will unblock when Close() is
		// called. Close should always be called, even if the context is
		// cancelled first.
		<-res.refreshCh
	}()

	timer := res.factory.clock.NewTimer(0)
	if !timer.Stop() {
		<-timer.Chan()
	}

	for {
		addresses, ttl, err := res.factory.prober.ResolveOnce(ctx, scheme, hostPort)
		if err != nil {
			receiver.OnResolveError(err)
		} else {
			receiver.OnResolve(addresses)
		}
		// TODO: exponential backoff on error
		// TODO: should exponential backoff override ResolveNow?

		if ttl == 0 {
			ttl = res.factory.defaultTTL
		}
		timer.Reset(ttl)

		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.Chan()
			}
			return
		case <-res.refreshCh:
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
