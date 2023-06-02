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
	"context"
	"net"
)

// Resolver provides name resolution functionality to resolve target names into
// backend addresses suitable to pass to the underlying dialer (usually IP
// addresses.)
type Resolver interface {
	// Resolve should return a slice of backends for the provided target name.
	Resolve(ctx context.Context, target string) ([]Backend, error)
}

// Backend stores the hostname and associated metadata for a backend resolved by
// a resolver.
type Backend struct {
	hostPort   string
	attributes map[any]any
}

// NewBackend constructs a new host with the provided host:port value.
func NewBackend(hostPort string) Backend {
	return Backend{hostPort: hostPort}
}

// WithAttributes returns a copy of the backend with attributes applied.
// Note that key should not be a primitive type like string and should
// instead be a custom type, a la context.WithValue.
// Attribute values should not be mutated once the Host is created.
func (h Backend) WithAttributes(newAttributes map[any]any) Backend {
	attributes := h.attributes
	if attributes != nil {
		for key, value := range newAttributes {
			attributes[key] = value
		}
	} else {
		attributes = newAttributes
	}
	return Backend{
		hostPort:   h.hostPort,
		attributes: attributes,
	}
}

// Attribute returns metadata attributes about the backend, such as priority/
// weight/distance/etc. that may be used by load balancing algorithms.
// Note that key should not be a primitive type like string and should
// instead be a custom type, a la context.WithValue.
// Returned attribute values should not be mutated.
func (h Backend) Attribute(key any) any {
	return h.attributes[key]
}

// HostPort returns a host:port suitable for inclusion in a URL.
func (h Backend) HostPort() string {
	return h.hostPort
}

// netResolver implements the Resolver interface with a *net.Resolver.
type netResolver struct {
	network  string
	resolver *net.Resolver
}

// NewNetResolver creates a new Resolver using a net.Resolver.
func NewNetResolver(network string, resolver *net.Resolver) Resolver {
	return &netResolver{
		network:  network,
		resolver: resolver,
	}
}

// Resolve implements Resolver.
func (d *netResolver) Resolve(ctx context.Context, hostPort string) ([]Backend, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, err
	}
	addresses, err := d.resolver.LookupNetIP(ctx, d.network, host)
	if err != nil {
		return nil, err
	}
	result := make([]Backend, len(addresses))
	for i, address := range addresses {
		result[i] = NewBackend(net.JoinHostPort(address.String(), port))
	}
	return result, nil
}
