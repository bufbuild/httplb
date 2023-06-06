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

// ResolverCallbackFunc is the signature of the resolver callback, which is
// called to report new name resolution results over time.
type ResolverCallbackFunc func(
	addresses []Address,
	ttl time.Duration,
	err error,
)

// Resolver is an interface for types that provide continuous name resolution.
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
	//
	// The resolver may pass a TTL value for the results to the callback.
	// If a TTL value is not available, it will be zero instead.
	Resolve(ctx context.Context, scheme, hostPort string,
		callback ResolverCallbackFunc) io.Closer
}

// SingleShotResolver is an interface for types that provide single-shot name
// resolution.
type SingleShotResolver interface {
	// ResolveOnce resolves the given target name once, returning a slice of
	// addresses corresponding to the provided scheme and hostname.
	ResolveOnce(ctx context.Context, scheme, hostPort string) ([]Address, time.Duration, error)
}

// Address contains a resolved address to a host, and any attributes that may be
// associated with a host/address.
type Address struct {
	// HostPort stores the host:port pair of the resolved address.
	HostPort string

	// Attributes is a collection of arbitrary key/value pairs.
	Attributes attrs.Attributes
}

type dnsSingleShotResolver struct {
	resolver *net.Resolver
	network  string
}

type pollingResolver struct {
	resolver SingleShotResolver
	interval time.Duration
}

type pollingResolverTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
}

type cachingResolver struct {
	resolver   Resolver
	defaultTTL time.Duration
}

// NewDNSResolver creates a new resolver that resolves DNS names.
// You can specify which kind of network addresses to resolve with the network
// parameter. IP addresses of the type specified by network. The network must
// be one of "ip", "ip4" or "ip6".
// Note that because net.Resolver does not expose the record TTL values, this
// resolver does not set the expiry.
func NewDNSResolver(
	resolver *net.Resolver,
	network string,
	interval time.Duration,
) Resolver {
	return NewPollingResolver(
		&dnsSingleShotResolver{
			resolver: resolver,
			network:  network,
		},
		interval,
	)
}

func (r *dnsSingleShotResolver) ResolveOnce(
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

// NewPollingResolver creates a new resolver that polls an underlying
// single-shot resolver on a fixed interval.
func NewPollingResolver(
	resolver SingleShotResolver,
	interval time.Duration,
) Resolver {
	return &pollingResolver{
		resolver: resolver,
		interval: interval,
	}
}

func (r *pollingResolver) Resolve(
	ctx context.Context,
	scheme, hostPort string,
	callback ResolverCallbackFunc,
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

func (t *pollingResolverTask) Close() error {
	t.cancel()
	<-t.doneSignal
	return nil
}

// NewCachingResolver creates a new resolver that wraps another resolver and
// caches the last non-empty result set until it expires. Whenever the resolver
// runs, if it returns empty results, cached results will be used instead,
// until those results expire. If the results do not have a TTL, the defaultTTL
// value will be used instead.
func NewCachingResolver(
	resolver Resolver,
	defaultTTL time.Duration,
) Resolver {
	return &cachingResolver{
		resolver:   resolver,
		defaultTTL: defaultTTL,
	}
}

func (r *cachingResolver) Resolve(
	ctx context.Context,
	scheme, hostPort string,
	callback ResolverCallbackFunc,
) io.Closer {
	last := []Address{}
	expiry := time.Time{}

	return r.resolver.Resolve(ctx, scheme, hostPort, func(addresses []Address, ttl time.Duration, err error) {
		now := time.Now()
		if ttl == 0 {
			ttl = r.defaultTTL
		}
		if len(addresses) > 0 {
			expiry = now.Add(ttl)
			last = addresses
		} else if !now.After(expiry) {
			addresses = last
		}
		callback(addresses, ttl, err)
	})
}
