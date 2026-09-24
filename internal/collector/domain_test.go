package collector

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/client"
	"github.com/metanovii/domain_exporter/v2/internal/rdap"
	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/metanovii/domain_exporter/v2/internal/state"
	"github.com/metanovii/domain_exporter/v2/internal/whois"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"
)

func TestCollectorError(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	rdapClient, err := rdap.NewClient(nil)
	require.NoError(t, err)
	multi := client.NewMultiClient(rdapClient, whois.NewClient())
	testCollector(t, NewDomainCollector(multi, time.Second, nil, safeconfig.Domain{Name: "fake.foo", Host: ""}), func(t *testing.T, status int, body string) {
		require.Equal(t, 200, status)
		require.Contains(t, body, "domain_probe_success{domain=\"fake.foo\"} 0")
		require.Contains(t, body, "domain_expiry_days{domain=\"fake.foo\"} -1")
	})
}

func TestNotExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	rdapClient, err := rdap.NewClient(nil)
	require.NoError(t, err)
	multi := client.NewMultiClient(rdapClient, whois.NewClient())
	testCollector(
		t,
		NewDomainCollector(multi, time.Second, nil, safeconfig.Domain{Name: "goreleaser.com", Host: ""}),
		func(t *testing.T, status int, body string) {
			t.Log(body)
			if strings.Contains(body, "domain_probe_success{domain=\"goreleaser.com\"} 0") {
				t.Skip("request failed")
				return
			}
			require.Equal(t, 200, status)
			require.Contains(t, body, "domain_probe_success{domain=\"goreleaser.com\"} 1")
			require.Regexp(t, `domain_expiry_days{domain=\"goreleaser.com\"} \d+`, body)
		},
	)
}

func testCollector(t *testing.T, collector prometheus.Collector, checker func(t *testing.T, status int, body string)) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	srv := httptest.NewServer(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	checker(t, resp.StatusCode, string(body))
}

func TestStateCollector(t *testing.T) {
	store, err := state.New("")
	require.NoError(t, err)
	now := time.Now()
	expiry := now.Add(100*24*time.Hour + time.Hour)
	require.NoError(t, store.Set("fresh.com", state.Entry{
		Expiry: expiry, LastSuccess: now.Add(-time.Hour), LastAttempt: now, LastError: "last try failed",
	}))
	require.NoError(t, store.Set("stale.com", state.Entry{
		Expiry: expiry, LastSuccess: now.Add(-6 * time.Hour), LastAttempt: now, LastError: "boom",
	}))
	require.NoError(t, store.Set("failed.es@whois.nic.es", state.Entry{LastAttempt: now, LastError: "boom"}))

	testCollector(t, NewStateCollector(store, 5*time.Hour,
		safeconfig.Domain{Name: "fresh.com"},
		safeconfig.Domain{Name: "stale.com"},
		safeconfig.Domain{Name: "failed.es", Host: "whois.nic.es"},
		safeconfig.Domain{Name: "never.com"},
	), func(t *testing.T, status int, body string) {
		require.Equal(t, 200, status)
		// Valid result is reported even though the last attempt failed.
		require.Contains(t, body, `domain_probe_success{domain="fresh.com"} 1`)
		require.Contains(t, body, `domain_expiry_days{domain="fresh.com"} 100`)
		require.Contains(t, body, fmt.Sprintf(`domain_expiry_time_seconds{domain="fresh.com"} %g`, float64(expiry.Unix())))
		// Result older than the TTL is a failure.
		require.Contains(t, body, `domain_probe_success{domain="stale.com"} 0`)
		require.Contains(t, body, `domain_expiry_days{domain="stale.com"} -1`)
		require.NotContains(t, body, `domain_expiry_time_seconds{domain="stale.com"}`)
		require.Contains(t, body, `domain_last_success_timestamp_seconds{domain="stale.com"}`)
		// Never succeeded.
		require.Contains(t, body, `domain_probe_success{domain="failed.es"} 0`)
		require.NotContains(t, body, `domain_last_success_timestamp_seconds{domain="failed.es"}`)
		// Never checked: not reported at all.
		require.NotContains(t, body, `never.com`)
	})
}
