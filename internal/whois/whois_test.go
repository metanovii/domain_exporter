package whois

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWhoisParsing(t *testing.T) {
	for _, tt := range []struct {
		domain  string
		host    string
		err     string
		expired bool
		broken  string // known problem, the test is skipped
	}{
		{domain: "google.ai", host: "", err: ""},
		{domain: "google.lt", host: "", err: ""},
		{domain: "fakedomain.foo", host: "", err: "no whois server found"},
		{domain: "google.cn", host: "", err: ""},
		{domain: "google.com", host: "", err: ""},
		{domain: "google.lu", host: "", err: "could not parse whois response"},
		{domain: "dns.lu", host: "", err: "could not parse whois response"},
		{domain: "google.de", host: "", err: "could not parse whois response"},
		{domain: "nic.ua", host: "", err: ""},
		{domain: "mod.gov.ua", host: "", err: ""},
		{domain: "google.com.tw", host: "", err: "", broken: "query goes to whois.apnic.net instead of whois.twnic.net.tw"},
		{domain: "bbc.co.uk", host: "", err: ""},
		{domain: "google.sg", host: "", err: ""},
		{domain: "google.sk", host: "", err: ""},
		{domain: "google.ro", host: "", err: "could not parse whois response"}, // ROTLD whois has no expiry date
		{domain: "google.pt", host: "", err: ""},
		// {domain: "microsoft.it", host: "whois.nic.it", err: "", expired: true}, TODO: fix
		{domain: "google.pw", host: "", err: ""},
		{domain: "google.co.id", host: "", err: ""},
		{domain: "google.kr", host: "", err: ""},
		{domain: "google.jp", host: "", err: ""},
		{domain: "microsoft.im", host: "", err: ""},
		{domain: "google.rs", host: "", err: ""},
		{domain: "мвд.рф", host: "", err: ""},
		{domain: "МВД.РФ", host: "", err: ""},
		{domain: "GOOGLE.RS", host: "", err: ""},
		{domain: "google.co.th", host: "", err: ""},
		{domain: "google.fi", host: "", err: ""},
		{domain: "google.com.hk", host: "", err: "", broken: "whois.hkirc.hk answers \"not available for registration\" to the exporter"},
		{domain: "hknic.hk", host: "", err: ""},
		{domain: "test.idv.hk", host: "", err: "", broken: "whois.hkirc.hk answers \"not available for registration\" to the exporter"},
		{domain: "test.org.hk", host: "", err: "", broken: "whois.hkirc.hk answers \"not available for registration\" to the exporter"},
		{domain: "hkirc.香港", host: "", err: "", broken: "whois.hkirc.hk answers \"CMM174\""},
		{domain: "google.vn", host: "whois.net.vn", err: "", broken: "whois.net.vn web API used by the adapter no longer answers"},
		{domain: "google.com.tr", host: "", err: ""},
		{domain: "google.com.ru", host: "whois.nic.ru", err: ""},
		{domain: "nic.kz", host: "", err: "could not parse whois response"}, // .kz whois has no expiry date, use RDAP
		{domain: "google.io", host: "whois.nic.io", err: ""},                // no built-in whois server for .io
		{domain: "google.ph", host: "whois.dot.ph", err: "", broken: "whois.dot.ph answers \"Domain not found\" or times out"},
		{domain: "google.com", host: "whois.dot.ph", err: "Domain not found or parsing error"},
		{domain: "google.uz", host: "", err: ""},
		{domain: "google.cl", host: "", err: ""},
		{domain: "google.ru", host: "", err: ""},
	} {
		t.Run(tt.domain, func(t *testing.T) {
			if testing.Short() {
				t.Skip("network test")
			}
			if tt.broken != "" {
				t.Skip("known problem: " + tt.broken)
			}
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			t.Cleanup(cancel)

			expiry, err := NewClient().ExpireTime(ctx, tt.domain, tt.host)
			if err != nil {
				errs := err.Error()
				if strings.Contains(errs, "i/o timeout") || strings.Contains(errs, "deadline exceeded") {
					t.Skip("timeout")
				}
				if strings.Contains(errs, "Too may requests") {
					t.Skip("rate limit")
				}
			}
			if tt.err == "" {
				require.NoError(t, err)
				if tt.expired {
					require.Greater(t, time.Since(expiry).Hours(), 0.0)
				} else {
					require.Less(t, time.Since(expiry).Hours(), 0.0)
				}
			} else {
				require.ErrorContains(t, err, tt.err)
				t.Log(err)
			}
		})
	}
}

func TestParseExpiry(t *testing.T) {
	for file, expected := range map[string]string{
		"google.com.txt":         "2028-09-14",
		"google.ee.txt":          "2026-11-09",
		"google.jp.txt":          "2027-05-31",
		"google.lt.txt":          "2026-12-08",
		"google.ru.txt":          "2027-03-04",
		"xn--b1aew.xn--p1ai.txt": "2026-11-24",
	} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(filepath.Join("testdata", file))
			require.NoError(t, err)

			expiry, err := parseExpiry(string(body))
			require.NoError(t, err)
			require.Equal(t, expected, expiry.Format(time.DateOnly))
		})
	}
}

func TestPreferParsable(t *testing.T) {
	registry, err := os.ReadFile(filepath.Join("testdata", "google.com.txt"))
	require.NoError(t, err)
	empty := "No matching domain found.\n"
	other := "Registry Expiry Date: 2030-01-02T03:04:05Z\n"

	require.Equal(t, string(registry), preferParsable(string(registry), empty))
	require.Equal(t, other, preferParsable(string(registry), other))
	require.Equal(t, empty, preferParsable("nothing here", empty))
}
