package client

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/rs/zerolog/log"
)

// StaticClient answers with the configured expiry_date of a domain without a
// lookup, and uses the wrapped client for the rest.
type StaticClient struct {
	client Client
	dates  atomic.Pointer[map[string]time.Time]
}

// NewStaticClient returns a StaticClient for domains.
func NewStaticClient(client Client, domains ...safeconfig.Domain) *StaticClient {
	c := &StaticClient{client: client}
	c.SetDomains(domains...)
	return c
}

// SetDomains replaces the configured expiry dates.
func (c *StaticClient) SetDomains(domains ...safeconfig.Domain) {
	dates := map[string]time.Time{}
	for _, d := range domains {
		if t, ok := d.Expiry(); ok {
			dates[d.Name] = t
		}
	}
	c.dates.Store(&dates)
}

// ExpireTime implements Client.
func (c *StaticClient) ExpireTime(ctx context.Context, domain string, host string) (time.Time, error) {
	if t, ok := (*c.dates.Load())[domain]; ok {
		log.Debug().Msgf("using configured expiry_date for %s", domain)
		return t, nil
	}
	return c.client.ExpireTime(ctx, domain, host)
}
