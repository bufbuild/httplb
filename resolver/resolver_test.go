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
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bufbuild/httplb/internal/clocktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

func TestResolverTTL(t *testing.T) {
	t.Parallel()

	refreshCh := make(chan struct{})

	const testTTL = 20 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	testClock := clocktest.NewFakeClock()
	resolver := NewDNSResolver(net.DefaultResolver, RequireIPv6, testTTL)
	resolver.(*pollingResolver).clock = testClock //nolint:errcheck

	signal := make(chan struct{})
	task := resolver.New(ctx, "http", "::1", testReceiver{
		onResolve: func(a []Address) {
			assert.Equal(t, "[::1]:80", a[0].HostPort)
			signal <- struct{}{}
		},
		onResolveError: func(err error) {
			t.Errorf("unexpected resolution error: %v", err)
		},
	}, refreshCh)
	waitForResolve := func() {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("expected call to resolver")
		}
	}

	t.Cleanup(func() {
		close(signal)
		err := task.Close()
		close(refreshCh)
		require.NoError(t, err)
	})

	waitForResolve()
	err := testClock.BlockUntilContext(ctx, 1)
	require.NoError(t, err)

	// When advancing the clock past the TTL, we should get a new probe.
	testClock.Advance(testTTL)
	waitForResolve()
	err = testClock.BlockUntilContext(ctx, 1)
	require.NoError(t, err)

	// When we call ResolveNow, we should get a new probe.
	select {
	case refreshCh <- struct{}{}:
	case <-ctx.Done():
		t.Fatalf("cancelled before refresh channel unblocked: %v", ctx.Err())
	}
	waitForResolve()
	err = testClock.BlockUntilContext(ctx, 1)
	assert.NoError(t, err)
}

func TestAddressFamilyPolicy(t *testing.T) {
	t.Parallel()

	ip4Header := dnsmessage.ResourceHeader{
		Name:  dnsmessage.MustNewName("example.com."),
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
	}
	ip6Header := dnsmessage.ResourceHeader{
		Name:  dnsmessage.MustNewName("example.com."),
		Type:  dnsmessage.TypeAAAA,
		Class: dnsmessage.ClassINET,
	}
	ip4Address1 := net.ParseIP("10.0.0.100")
	ip4Address2 := net.ParseIP("10.0.0.101")
	ip6Address1 := net.ParseIP("fe80::1")
	ip6Address2 := net.ParseIP("fe80::2")
	ip4Address1Resource := dnsmessage.Resource{
		Header: ip4Header,
		Body:   &dnsmessage.AResource{A: [4]byte(ip4Address1.To4())},
	}
	ip4Address2Resource := dnsmessage.Resource{
		Header: ip4Header,
		Body:   &dnsmessage.AResource{A: [4]byte(ip4Address2.To4())},
	}
	ip6Address1Resource := dnsmessage.Resource{
		Header: ip6Header,
		Body:   &dnsmessage.AAAAResource{AAAA: [16]byte(ip6Address1)},
	}
	ip6Address2Resource := dnsmessage.Resource{
		Header: ip6Header,
		Body:   &dnsmessage.AAAAResource{AAAA: [16]byte(ip6Address2)},
	}

	// Mixed A/AAAA records
	mixedDNSResolver := newFakeDNSResolver(t, []dnsmessage.Resource{
		ip4Address1Resource,
		ip6Address1Resource,
		ip4Address2Resource,
		ip6Address2Resource,
	})
	resolver := NewDNSResolver(mixedDNSResolver, PreferIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})
	resolver = NewDNSResolver(mixedDNSResolver, RequireIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})
	resolver = NewDNSResolver(mixedDNSResolver, PreferIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})
	resolver = NewDNSResolver(mixedDNSResolver, RequireIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})
	resolver = NewDNSResolver(mixedDNSResolver, UseBothIPv4AndIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2, ip6Address1, ip6Address2})

	// A records only
	ip4DNSResolver := newFakeDNSResolver(t, []dnsmessage.Resource{
		ip4Address1Resource,
		ip4Address2Resource,
	})
	resolver = NewDNSResolver(ip4DNSResolver, PreferIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})
	resolver = NewDNSResolver(ip4DNSResolver, RequireIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})
	resolver = NewDNSResolver(ip4DNSResolver, PreferIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})
	resolver = NewDNSResolver(ip4DNSResolver, RequireIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{})
	resolver = NewDNSResolver(ip4DNSResolver, UseBothIPv4AndIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip4Address1, ip4Address2})

	// AAAA records only
	ip6DNSResolver := newFakeDNSResolver(t, []dnsmessage.Resource{
		ip6Address1Resource,
		ip6Address2Resource,
	})
	resolver = NewDNSResolver(ip6DNSResolver, PreferIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})
	resolver = NewDNSResolver(ip6DNSResolver, RequireIPv4, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{})
	resolver = NewDNSResolver(ip6DNSResolver, PreferIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})
	resolver = NewDNSResolver(ip6DNSResolver, RequireIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})
	resolver = NewDNSResolver(ip6DNSResolver, UseBothIPv4AndIPv6, 1)
	testResolveAddresses(t, resolver, "example.com", []net.IP{ip6Address1, ip6Address2})

	// IPv4 embedded in IPv6
	// This is needed because Go will do this for all IPv4 addresses that
	// are passed into the resolver. Even if Go's behavior changes, we
	// should behave consistently in the face of this quirk.
	resolver = NewDNSResolver(net.DefaultResolver, RequireIPv4, 1)
	loopback := net.ParseIP("127.0.0.1")
	testResolveAddresses(t, resolver, "127.0.0.1", []net.IP{loopback})
	testResolveAddresses(t, resolver, "::ffff:127.0.0.1", []net.IP{loopback})
}

func testResolveAddresses(
	t *testing.T,
	resolver Resolver,
	target string,
	expectedAddresses []net.IP,
) {
	t.Helper()

	refreshCh := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	testClock := clocktest.NewFakeClock()
	resolver.(*pollingResolver).clock = testClock //nolint:errcheck

	resolved := make(chan []Address)
	task := resolver.New(ctx, "http", target, testReceiver{
		onResolve: func(resolvedAddresses []Address) {
			resolved <- resolvedAddresses
		},
		onResolveError: func(err error) {
			if len(expectedAddresses) > 0 {
				t.Errorf("unexpected resolution error: %v", err)
			} else {
				dnsErr := &net.DNSError{}
				if assert.ErrorAs(t, err, &dnsErr) {
					assert.True(t, dnsErr.IsNotFound)
					resolved <- []Address{}
				}
			}
		},
	}, refreshCh)

	t.Cleanup(func() {
		close(resolved)
		err := task.Close()
		close(refreshCh)
		require.NoError(t, err)
	})

	select {
	case resolvedAddresses := <-resolved:
		actualAddresses := make([]net.IP, len(resolvedAddresses))
		for i, address := range resolvedAddresses {
			actualHost, _, err := net.SplitHostPort(address.HostPort)
			require.NoError(t, err)
			actualAddresses[i] = net.ParseIP(actualHost)
		}
		assert.ElementsMatch(t, expectedAddresses, actualAddresses)
	case <-ctx.Done():
		t.Fatal("expected call to resolver")
	}
}

type testReceiver struct {
	onResolve      func([]Address)
	onResolveError func(error)
}

func (r testReceiver) OnResolve(addresses []Address) {
	r.onResolve(addresses)
}

func (r testReceiver) OnResolveError(err error) {
	r.onResolveError(err)
}

type fakeDNSResolver struct {
	t       *testing.T
	answers []dnsmessage.Resource
}

func (r *fakeDNSResolver) Dial(context.Context, string, string) (net.Conn, error) {
	clientConn, serverConn := net.Pipe()
	go func() {
		var requestLength uint16
		if err := binary.Read(serverConn, binary.BigEndian, &requestLength); err != nil {
			r.t.Errorf("error reading dns request length: %v", err)
			return
		}
		requestData := make([]byte, requestLength)
		if _, err := io.ReadFull(serverConn, requestData); err != nil {
			r.t.Errorf("error reading dns request: %v", err)
			return
		}
		request := &dnsmessage.Message{}
		if err := request.Unpack(requestData); err != nil {
			r.t.Errorf("error unpacking dns request: %v", err)
			return
		}
		answers := []dnsmessage.Resource{}
		for _, answer := range r.answers {
			if answer.Header.Type == request.Questions[0].Type {
				answers = append(answers, answer)
			}
		}
		response := &dnsmessage.Message{
			Header: dnsmessage.Header{
				ID:            request.ID,
				Response:      true,
				RCode:         dnsmessage.RCodeSuccess,
				Authoritative: true,
			},
			Questions: request.Questions,
			Answers:   answers,
		}
		responseData, err := response.Pack()
		if err != nil {
			r.t.Errorf("error packing dns response: %v", err)
			return
		}
		responseLength := uint16(len(responseData))
		if err := binary.Write(serverConn, binary.BigEndian, &responseLength); err != nil {
			r.t.Errorf("error writing dns response length: %v", err)
			return
		}
		if _, err := serverConn.Write(responseData); err != nil {
			r.t.Errorf("error writing dns response: %v", err)
			return
		}
		if err := serverConn.Close(); err != nil {
			r.t.Errorf("error closing dns server connection: %v", err)
			return
		}
	}()
	return clientConn, nil
}

func newFakeDNSResolver(t *testing.T, answers []dnsmessage.Resource) *net.Resolver {
	t.Helper()

	dialer := fakeDNSResolver{
		t:       t,
		answers: answers,
	}
	return &net.Resolver{
		PreferGo: true,
		Dial:     dialer.Dial,
	}
}
