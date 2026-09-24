package rdap

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/client"
	"github.com/openrdap/rdap"
	"github.com/openrdap/rdap/bootstrap"
	"github.com/rs/zerolog/log"
	"golang.org/x/net/idna"
)

// nolint: gochecknoglobals
var (
	formats = []string{
		time.RFC3339,
		time.ANSIC,
		time.UnixDate,
		time.RubyDate,
		time.RFC822,
		time.RFC822Z,
		time.RFC850,
		time.RFC1123,
		time.RFC1123Z,
		time.RFC3339Nano,
		"20060102",                 // .com.br
		"2006-01-02",               // .lt
		"2006-01-02 15:04:05-07",   // .ua
		"2006-01-02 15:04:05",      // .ch
		"2006-01-02T15:04:05Z",     // .name
		"2006-01-02T15:04:05.0Z",   // .host
		"January  2 2006",          // .is
		"02.01.2006",               // .cz
		"02/01/2006",               // .fr
		"02-January-2006",          // .ie
		"2006.01.02 15:04:05",      // .pl
		"02-Jan-2006",              // .co.uk
		"02-Jan-2006 15:04:05",     // .sg
		"2006-01-02T15:04:05Z",     // .co
		"2006/01/02",               // .ca
		"2006-01-02 (YYYY-MM-DD)",  // .tw
		"(dd/mm/yyyy): 02/01/2006", // .pt
		"02-Jan-2006 15:04:05 UTC", // .id, .co.id
		": 2006. 01. 02.",          // .kr
	}
)

// Client is an RDAP client. Its RDAP server overrides can be replaced at
// runtime with SetServers.
type Client struct {
	servers *atomic.Pointer[map[string]*url.URL]
	// One client for the whole process, so the IANA bootstrap registry is
	// downloaded once and then kept in its cache (24h by default).
	// openrdap's client is not safe for concurrent use, hence the mutex.
	mu     *sync.Mutex
	client *rdap.Client
}

var _ client.Client = (*Client)(nil)

// NewClient returns a new RDAP client.
// servers maps a TLD or domain suffix (e.g. "kz") to the RDAP server base URL
// to use instead of the IANA bootstrap registry.
func NewClient(servers map[string]string) (*Client, error) {
	c := &Client{
		servers: &atomic.Pointer[map[string]*url.URL]{},
		mu:      &sync.Mutex{},
		client:  &rdap.Client{HTTP: &http.Client{}, Bootstrap: &bootstrap.Client{}},
	}
	if err := c.SetServers(servers); err != nil {
		return nil, err
	}
	return c, nil
}

// SetServers validates and replaces the RDAP server overrides.
func (c *Client) SetServers(servers map[string]string) error {
	parsed := make(map[string]*url.URL, len(servers))
	for suffix, server := range servers {
		key, err := normalize(suffix)
		if err != nil {
			return fmt.Errorf("invalid rdap server suffix %q: %w", suffix, err)
		}
		u, err := url.Parse(server)
		if err != nil {
			return fmt.Errorf("invalid rdap server url for %q: %w", suffix, err)
		}
		if u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
			return fmt.Errorf("invalid rdap server url for %q: %q", suffix, server)
		}
		parsed[key] = u
	}
	c.servers.Store(&parsed)
	return nil
}

func normalize(name string) (string, error) {
	return idna.ToASCII(strings.Trim(strings.ToLower(name), "."))
}

// server returns the configured RDAP server for the longest matching
// suffix of domain, or nil to use the IANA bootstrap registry.
func (c *Client) server(domain string) *url.URL {
	servers := *c.servers.Load()
	if len(servers) == 0 {
		return nil
	}
	name, err := normalize(domain)
	if err != nil {
		return nil
	}
	for {
		if u, ok := servers[name]; ok {
			return u
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return nil
		}
		name = name[i+1:]
	}
}

// ExpireTime implements client.Client.
func (c *Client) ExpireTime(ctx context.Context, domain string, host string) (time.Time, error) {
	log.Debug().Msgf("trying rdap client for %s", domain)
	req := &rdap.Request{
		Type:  rdap.DomainRequest,
		Query: domain,
	}
	if server := c.server(domain); server != nil {
		log.Debug().Msgf("using rdap server %s for %s", server, domain)
		// openrdap modifies the server URL in place, so pass a copy.
		u := *server
		req = req.WithServer(&u)
	}
	req = req.WithContext(ctx)

	c.mu.Lock()
	resp, err := c.client.Do(req)
	c.mu.Unlock()
	if err != nil {
		return time.Now(), fmt.Errorf("failed to do rdap request: %w", err)
	}

	body, ok := resp.Object.(*rdap.Domain)
	if !ok {
		return time.Now(), fmt.Errorf("failed to cast rdap domain object: %w", err)
	}

	for _, event := range body.Events {
		if event.Action == "expiration" {
			for _, format := range formats {
				if date, err := time.Parse(format, event.Date); err == nil {
					return date, nil
				}
			}
			return time.Now(), fmt.Errorf("could not parse date: %s", event.Date)
		}
	}
	return time.Now(), fmt.Errorf("no expiration event for domain: %s ", domain)
}
