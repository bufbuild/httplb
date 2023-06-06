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

	"github.com/bufbuild/go-http-balancer/attrs"
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
		callback func(addresses []Address, err error)) io.Closer
}

// ResolveProber is an interface for types that provide single-shot name
// resolution.
type ResolveProber interface {
	// ResolveOnce resolves the given target name once, returning a slice of
	// addresses corresponding to the provided scheme and hostname.
	// The second return value specifies the TTL of the result, or 0 if there
	// is no known TTL value.
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
	resolver   ResolveProber
	defaultTTL time.Duration
}

type pollingResolverTask struct {
	cancel     context.CancelFunc
	doneSignal chan struct{}
}

// NewDNSResolver creates a new resolver that resolves DNS names.
// You can specify which kind of network addresses to resolve with the network
// parameter, and the resolver will return only IP addresses of the type
// specified by network. The network must be one of "ip", "ip4" or "ip6".
// Note that because net.Resolver does not expose the record TTL values, this
// resolver uses the fixed TTL provided in the ttl parameter.
func NewDNSResolver(
	resolver *net.Resolver,
	network string,
	ttl time.Duration,
) Resolver {
	return NewPollingResolver(
		&dnsSingleShotResolver{
			resolver: resolver,
			network:  network,
		},
		ttl,
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
// single-shot resolver whenever the result-set TTL expires. If the underlying
// resolver does not return a TTL with the result-set, defaultTTL is used.
func NewPollingResolver(
	resolver ResolveProber,
	defaultTTL time.Duration,
) Resolver {
	return &pollingResolver{
		resolver:   resolver,
		defaultTTL: defaultTTL,
	}
}

func (r *pollingResolver) Resolve(
	ctx context.Context,
	scheme, hostPort string,
	callback func(addresses []Address, err error),
) io.Closer {
	ctx, cancel := context.WithCancel(ctx)
	task := &pollingResolverTask{
		cancel:     cancel,
		doneSignal: make(chan struct{}),
	}
	go func() {
		defer close(task.doneSignal)
		defer cancel()

		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}

		for {
			addresses, ttl, err := r.resolver.ResolveOnce(ctx, scheme, hostPort)
			callback(addresses, err)

			if ttl == 0 {
				ttl = r.defaultTTL
			}
			timer.Reset(ttl)

			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
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
