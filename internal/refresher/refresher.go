// Package refresher checks the configured domains in the background and keeps
// the results in a state.Store.
package refresher

import (
	"context"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/client"
	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/metanovii/domain_exporter/v2/internal/state"
	"github.com/rs/zerolog/log"
)

// jitter spreads refreshes of domains checked at the same time.
const jitter = 0.1

// Options configure a Refresher.
type Options struct {
	// TTL is how long a successful result stays valid.
	TTL time.Duration
	// RefreshRatio: a domain is checked again when less than this share of
	// TTL is left, e.g. 0.3 with a TTL of 5h means after about 3.5h.
	RefreshRatio float64
	// RetryInterval is the delay before checking again after a failure.
	RetryInterval time.Duration
	// Timeout is the timeout of one lookup.
	Timeout time.Duration
	// ScanInterval is how often to look for domains that are due.
	ScanInterval time.Duration
	// Pause between two lookups, to stay below whois servers' rate limits.
	Pause time.Duration
}

// Refresher checks domains when they are due and stores the results.
type Refresher struct {
	client  client.Client
	store   *state.Store
	domains atomic.Pointer[[]safeconfig.Domain]
	opts    Options
	now     func() time.Time
}

// New returns a Refresher.
func New(client client.Client, store *state.Store, opts Options, domains ...safeconfig.Domain) *Refresher {
	r := &Refresher{
		client: client,
		store:  store,
		opts:   opts,
		now:    time.Now,
	}
	r.SetDomains(domains...)
	return r
}

// SetDomains replaces the list of domains to check.
func (r *Refresher) SetDomains(domains ...safeconfig.Domain) {
	r.domains.Store(&domains)
}

// Run checks the due domains now and then every ScanInterval until ctx is done.
func (r *Refresher) Run(ctx context.Context) {
	log.Info().Msg("run refresher")
	ticker := time.NewTicker(r.opts.ScanInterval)
	defer ticker.Stop()
	for {
		r.Refresh(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			log.Info().Msg("refresher is finished")
			return
		}
	}
}

// Refresh checks, one after another, every domain whose next check is due.
func (r *Refresher) Refresh(ctx context.Context) {
	checked := 0
	for _, domain := range *r.domains.Load() {
		entry, _ := r.store.Get(state.Key(domain.Name, domain.Host))
		if r.now().Before(entry.NextCheck) {
			continue
		}
		if checked > 0 {
			select {
			case <-time.After(r.opts.Pause):
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		r.Check(ctx, domain)
		checked++
	}
	if checked > 0 {
		log.Debug().Msgf("refreshed %d domains", checked)
	}
}

// Check looks up one domain now, stores and returns the result.
func (r *Refresher) Check(ctx context.Context, domain safeconfig.Domain) state.Entry {
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	start := r.now()
	expiry, err := r.client.ExpireTime(ctx, domain.Name, domain.Host)
	return r.Record(domain, expiry, err, r.now().Sub(start))
}

// Record stores the result of a lookup of domain done elsewhere (e.g. by
// /probe) and schedules its next check. It returns the stored entry.
func (r *Refresher) Record(domain safeconfig.Domain, expiry time.Time, err error, duration time.Duration) state.Entry {
	key := state.Key(domain.Name, domain.Host)
	entry, _ := r.store.Get(key)
	now := r.now()
	entry.LastAttempt = now
	entry.Duration = duration
	if err != nil {
		log.Error().Err(err).Msgf("failed to get expire time for %s", domain.Name)
		entry.LastError = err.Error()
		entry.NextCheck = now.Add(r.opts.RetryInterval)
	} else {
		entry.Expiry = expiry
		entry.LastSuccess = now
		entry.LastError = ""
		entry.NextCheck = now.Add(r.refreshDelay())
	}
	if err := r.store.Set(key, entry); err != nil {
		log.Error().Err(err).Msg("failed to save state")
	}
	return entry
}

// refreshDelay is TTL*(1-RefreshRatio) with a random +/-10% spread, capped at
// TTL so a result is always refreshed before it becomes invalid.
func (r *Refresher) refreshDelay() time.Duration {
	base := float64(r.opts.TTL) * (1 - r.opts.RefreshRatio)
	delay := time.Duration(base * (1 + jitter*(2*rand.Float64()-1)))
	return min(delay, r.opts.TTL)
}

// Configured returns the configured domain matching name and host.
func (r *Refresher) Configured(name, host string) (safeconfig.Domain, bool) {
	for _, d := range *r.domains.Load() {
		if d.Name == name && (host == "" || host == d.Host) {
			return d, true
		}
	}
	return safeconfig.Domain{}, false
}
