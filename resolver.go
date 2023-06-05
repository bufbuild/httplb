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

package httplb

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/bufbuild/go-http-balancer/attrs"
)

// Resolver is an interface for types that provide continous name resolution.
type Resolver interface {
	// Resolve the given target name. When the target is resolved into
	// backend addresses, they are provided to the given callback.
	//
	// As new results arrive (since the set of addresses may change
	// over time), the callback may be called repeatedly. Each time,
	// the entire set of addresses should be supplied.
	//
	// The returned closer will be used to stop the resolve process.
	// Calling its Close method should free any resources (including
	// any background goroutines) before returning. No subsequent
	// calls to callback should be made after Close returns. The
	// resolve process should also initiate shutting down if/when the
	// given context is cancelled.
	//
	// The resolver may report errors in addition to or instead of
	// addresses. But it should keep trying to resolve (and watch
	// for changes), even in the face of errors, until it is closed or
	// the given context is cancelled.
	Resolve(ctx context.Context, scheme, hostPort string,
		callback func([]Address, error)) io.Closer
}

// ResolveOncer is an interface for types that provide one-shot name resolution.
type ResolveOncer interface {
	// ResolveOnce resolves the given target name once, returning a slice of
	// addresses corresponding to the provided scheme and hostname.
	ResolveOnce(ctx context.Context, scheme, hostPort string) ([]Address, error)
}

// Address contains a resolved address to a host, and any attributes that may be
// associated with a host/address.
type Address struct {
	// HostPort stores the host:port pair of the resolved address.
	HostPort string

	// Expiry is the moment at which this record should be considered stale.
	// If there is no known TTL or expiry for a record, this should be set to
	// the zero value.
	Expiry time.Time

	// Attributes is a collection of arbitrary key/value pairs.
	Attributes attrs.Attributes
}

// A DNSResolver uses a net.Resolver to resolve hostnames using the domain name
// system. It is a one-shot resolver, since DNS requests are also one-shot.
type DNSResolver struct {
	resolver *net.Resolver
	network  string
}

// A PollingResolver will call an underlying ResolveOncer repeatedly at a set
// interval.
type PollingResolver struct {
	resolver ResolveOncer
	interval time.Duration
}

type pollingResolverTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
}

// A CachingResolver will call an underlying Resolver, and cache valid results
// until their TTL expires.
type CachingResolver struct {
	resolver   Resolver
	defaultTTL time.Duration
}

// NewDNSResolver creates a new one-shot resolver that resolves DNS names.
// You can specify which kind of network addresses to resolve with the network
// parameter. IP addresses of the type specified by network. The network must
// be one of "ip", "ip4" or "ip6".
// Note that because net.Resolver does not expose the record TTL values, this
// resolver does not set the expiry.
func NewDNSResolver(resolver *net.Resolver, network string) *DNSResolver {
	return &DNSResolver{
		resolver: resolver,
		network:  network,
	}
}

// ResolveOnce resolves a DNS name, implementing ResolveOncer.
func (r *DNSResolver) ResolveOnce(
	ctx context.Context,
	scheme, hostPort string,
) ([]Address, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, err
	}
	addresses, err := r.resolver.LookupNetIP(ctx, r.network, host)
	if err != nil {
		return nil, err
	}
	result := make([]Address, len(addresses))
	for i, address := range addresses {
		result[i].HostPort = net.JoinHostPort(address.String(), port)
	}
	return result, nil
}

// NewPollingResolver creates a new PollingResolver, which will periodically
// call an underlying ResolveOncer.
func NewPollingResolver(
	resolver ResolveOncer,
	interval time.Duration,
) *PollingResolver {
	return &PollingResolver{
		resolver: resolver,
		interval: interval,
	}
}

// Resolve starts a polling resolver using the provided parameters.
func (r *PollingResolver) Resolve(
	ctx context.Context,
	scheme, hostPort string,
	callback func([]Address, error),
) io.Closer {
	ctx, cancel := context.WithCancel(ctx)
	task := &pollingResolverTask{
		cancel:     cancel,
		doneSignal: make(chan struct{}),
	}
	ticker := time.NewTicker(r.interval)
	go func() {
		defer close(task.doneSignal)
		defer cancel()

		for {
			callback(r.resolver.ResolveOnce(ctx, scheme, hostPort))

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Continue.
			}
		}
	}()
	return task
}

// Close closes the resolver task.
func (t *pollingResolverTask) Close() error {
	t.cancel()
	<-t.doneSignal
	return nil
}

// NewCachingResolver creates a new CachingResolver, which will wrap another
// resolver and cache valid results until they expire. If an underlying
// address has no expiry, defaultTTL will be used to set one.
func NewCachingResolver(
	resolver Resolver,
	defaultTTL time.Duration,
) *CachingResolver {
	return &CachingResolver{
		resolver:   resolver,
		defaultTTL: defaultTTL,
	}
}

// Resolve starts the underlying resolver, providing caching functionality.
func (r *CachingResolver) Resolve(
	ctx context.Context,
	scheme, hostPort string,
	callback func([]Address, error),
) io.Closer {
	cache := map[string]Address{}

	return r.resolver.Resolve(ctx, scheme, hostPort, func(addresses []Address, err error) {
		now := time.Now()

		if err != nil {
			// If an error occurred and we have no results, return cached
			// results. Resolvers are allowed to return errors when they
			// still return some addresses, so we don't always need cached
			// addresses.
			if len(addresses) == 0 && len(cache) > 0 {
				for key, address := range cache {
					if now.After(address.Expiry) {
						delete(cache, key)
					} else {
						addresses = append(addresses, address)
					}
				}
			}
		} else {
			for key, address := range cache {
				if now.After(address.Expiry) {
					delete(cache, key)
				}
			}
			for i := range addresses {
				if addresses[i].Expiry.IsZero() {
					addresses[i].Expiry = now.Add(r.defaultTTL)
				}
				cache[addresses[i].HostPort] = addresses[i]
			}
			for _, address := range addresses {
				cache[address.HostPort] = address
			}
		}

		callback(addresses, err)
	})
}
