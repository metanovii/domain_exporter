package collector

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/client"
	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/metanovii/domain_exporter/v2/internal/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// Result is called with the outcome of every live lookup.
type Result func(domain safeconfig.Domain, expiry time.Time, err error, duration time.Duration)

type descs struct {
	expiryDays    *prometheus.Desc
	expiryTime    *prometheus.Desc
	probeSuccess  *prometheus.Desc
	probeDuration *prometheus.Desc
	lastSuccess   *prometheus.Desc
}

func newDescs() descs {
	const namespace = "domain"
	const subsystem = ""
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystem, name), help, []string{"domain"}, nil)
	}
	return descs{
		expiryDays:    desc("expiry_days", "time in days until the domain expires, -1 if unknown"),
		expiryTime:    desc("expiry_time_seconds", "domain expiration time as a unix timestamp"),
		probeSuccess:  desc("probe_success", "whether the probe was successful or not"),
		probeDuration: desc("probe_duration_seconds", "returns how long the probe took to complete in seconds"),
		lastSuccess:   desc("last_success_timestamp_seconds", "when the expiration time was last obtained, as a unix timestamp"),
	}
}

func (d descs) describe(ch chan<- *prometheus.Desc) {
	ch <- d.expiryDays
	ch <- d.expiryTime
	ch <- d.probeSuccess
	ch <- d.probeDuration
	ch <- d.lastSuccess
}

// collect sends the metrics of one domain. expiry is used only if success.
func (d descs) collect(
	ch chan<- prometheus.Metric,
	domain string,
	success bool,
	expiry time.Time,
	duration time.Duration,
	lastSuccess time.Time,
) {
	gauge := func(desc *prometheus.Desc, value float64) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, domain)
	}
	gauge(d.probeSuccess, boolToFloat(success))
	if success {
		gauge(d.expiryDays, math.Floor(time.Until(expiry).Hours()/24))
		gauge(d.expiryTime, float64(expiry.Unix()))
	} else {
		gauge(d.expiryDays, -1)
	}
	gauge(d.probeDuration, duration.Seconds())
	if !lastSuccess.IsZero() {
		gauge(d.lastSuccess, float64(lastSuccess.Unix()))
	}
}

type domainCollector struct {
	descs
	client   client.Client
	domains  []safeconfig.Domain
	timeout  time.Duration
	onResult Result
}

// NewDomainCollector returns a collector that looks up every domain on each
// collection, with timeout per domain. onResult, if not nil, is called with
// every lookup result.
func NewDomainCollector(client client.Client, timeout time.Duration, onResult Result, domains ...safeconfig.Domain) prometheus.Collector {
	return &domainCollector{
		descs:    newDescs(),
		client:   client,
		domains:  domains,
		timeout:  timeout,
		onResult: onResult,
	}
}

// Describe all metrics
func (c *domainCollector) Describe(ch chan<- *prometheus.Desc) {
	c.describe(ch)
}

// Collect all metrics
func (c *domainCollector) Collect(ch chan<- prometheus.Metric) {
	for _, domain := range c.domains {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		start := time.Now()
		date, err := c.client.ExpireTime(ctx, domain.Name, domain.Host)
		duration := time.Since(start)
		cancel()
		if err != nil {
			log.Error().Err(err).Msgf("failed to probe %s", domain)
		}
		if c.onResult != nil {
			c.onResult(domain, date, err, duration)
		}
		var lastSuccess time.Time
		if err == nil {
			lastSuccess = time.Now()
		}
		c.collect(ch, domain.Name, err == nil, date, duration, lastSuccess)
	}
}

// StateCollector reports the stored results and never does a lookup.
type StateCollector struct {
	descs
	store   *state.Store
	ttl     time.Duration
	domains atomic.Pointer[[]safeconfig.Domain]
}

// NewStateCollector returns a collector that reports the stored results and
// never does a lookup. A result older than ttl is reported as a failure.
// Domains never checked yet are not reported.
func NewStateCollector(store *state.Store, ttl time.Duration, domains ...safeconfig.Domain) *StateCollector {
	c := &StateCollector{
		descs: newDescs(),
		store: store,
		ttl:   ttl,
	}
	c.SetDomains(domains...)
	return c
}

// SetDomains replaces the list of reported domains.
func (c *StateCollector) SetDomains(domains ...safeconfig.Domain) {
	c.domains.Store(&domains)
}

// Describe all metrics
func (c *StateCollector) Describe(ch chan<- *prometheus.Desc) {
	c.describe(ch)
}

// Collect all metrics
func (c *StateCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	for _, domain := range *c.domains.Load() {
		entry, ok := c.store.Get(state.Key(domain.Name, domain.Host))
		if !ok || entry.LastAttempt.IsZero() {
			continue
		}
		c.collect(ch, domain.Name, entry.Valid(now, c.ttl), entry.Expiry, entry.Duration, entry.LastSuccess)
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
