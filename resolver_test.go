package httplb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type testResolver struct {
	addresses []Address
	err       error
}

func (t *testResolver) ResolveOnce(ctx context.Context, scheme, hostPort string) ([]Address, error) {
	return t.addresses, t.err
}

func TestCachingResolverDefaultTTL(t *testing.T) {
	t.Parallel()

	arbitraryExpiry := time.Date(2023, 06, 30, 0, 0, 0, 0, time.UTC)
	addresses := []Address{
		{
			HostPort: "with-expiry",
			Expiry:   arbitraryExpiry,
		},
		{
			HostPort: "without-expiry",
		},
	}

	signal := make(chan struct{})
	resolver := NewCachingResolver(
		NewPollingResolver(
			&testResolver{addresses: addresses},
			time.Minute,
		),
		time.Hour,
	)

	callback := func(addresses []Address, err error) {
		assert.Equal(t, arbitraryExpiry, addresses[0].Expiry)
		assert.False(t, addresses[1].Expiry.IsZero())
		close(signal)
	}

	closer := resolver.Resolve(context.Background(), "https", "test", callback)
	<-signal
	closer.Close()
}
