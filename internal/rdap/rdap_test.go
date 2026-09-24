package rdap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRdapParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	for _, tt := range []struct {
		domain string
		err    string
	}{
		// {domain: "google.ai", err: "No RDAP servers found for 'google.ai'"},
		{domain: "domreg.lt", err: "No RDAP servers found for 'domreg.lt'"},
		{domain: "fakedomain.foo", err: "RDAP server returned 404, object does not exist."},
		{domain: "google.cn", err: "No RDAP servers found for 'google.cn'"},
		{domain: "google.com", err: ""},
		{domain: "google.lu", err: "No RDAP servers found for 'google.lu'"},
		{domain: "google.de", err: "No RDAP servers found for 'google.de'"},
		{domain: "nic.ua", err: ""},
		{domain: "taiwannews.com.tw", err: ""},
		// {domain: "bbc.co.uk", err: "No RDAP servers found for 'bbc.co.uk'"},
		{domain: "google.sg", err: ""},
		{domain: "google.sk", err: "No RDAP servers found for 'google.sk'"},
		{domain: "google.ro", err: "No RDAP servers found for 'google.ro'"},
		{domain: "google.pw", err: ""},
		// {domain: "google.co.id", err: ""}, // random failures
		{domain: "google.kr", err: ""},
		{domain: "google.host", err: ""},
	} {
		t.Run(tt.domain, func(t *testing.T) {
			t.Parallel()
			cli, err := NewClient(nil)
			require.NoError(t, err)
			expiry, err := cli.ExpireTime(context.Background(), tt.domain, "")
			if tt.err == "" {
				require.NoError(t, err)
				require.Less(t, time.Since(expiry).Hours(), 0.0)
			} else {
				require.ErrorContains(t, err, tt.err)
			}
		})
	}
}

func TestRdapServerOverride(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/rdap+json")
		_, _ = w.Write([]byte(`{
			"objectClassName": "domain",
			"ldhName": "example.kz",
			"events": [
				{"eventAction": "registration", "eventDate": "2019-05-06T07:55:56Z"},
				{"eventAction": "expiration", "eventDate": "2027-05-06T07:55:56Z"}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	cli, err := NewClient(map[string]string{"kz": srv.URL})
	require.NoError(t, err)

	expiry, err := cli.ExpireTime(context.Background(), "example.kz", "")
	require.NoError(t, err)
	require.Equal(t, "/domain/example.kz", gotPath)
	require.Equal(t, time.Date(2027, 5, 6, 7, 55, 56, 0, time.UTC), expiry)
}

func TestRdapServerLookup(t *testing.T) {
	cli, err := NewClient(map[string]string{
		"kz":      "https://rdap.nic.kz/",
		".COM.KZ": "https://example.com/rdap",
		"рф":      "https://rdap.example.ru/",
	})
	require.NoError(t, err)
	c := cli

	for domain, expected := range map[string]string{
		"example.kz":            "https://rdap.nic.kz/",
		"EXAMPLE.KZ":            "https://rdap.nic.kz/",
		"sub.example.kz":        "https://rdap.nic.kz/",
		"shop.com.kz":           "https://example.com/rdap",
		"пример.рф":             "https://rdap.example.ru/",
		"xn--e1afmkfd.xn--p1ai": "https://rdap.example.ru/",
		"google.com":            "",
		"kz.com":                "",
	} {
		t.Run(domain, func(t *testing.T) {
			got := ""
			if u := c.server(domain); u != nil {
				got = u.String()
			}
			require.Equal(t, expected, got)
		})
	}
}

func TestRdapServerInvalid(t *testing.T) {
	for _, server := range []string{"rdap.nic.kz", "ftp://rdap.nic.kz/", "https://"} {
		_, err := NewClient(map[string]string{"kz": server})
		require.Error(t, err, server)
	}
}
