package refresher

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/metanovii/domain_exporter/v2/internal/state"
	"github.com/stretchr/testify/require"
)

var expiry = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

type fakeClient struct {
	err   error
	calls atomic.Int32
}

func (f *fakeClient) ExpireTime(ctx context.Context, domain string, host string) (time.Time, error) {
	f.calls.Add(1)
	if f.err != nil {
		return time.Time{}, f.err
	}
	return expiry, nil
}

var opts = Options{
	TTL:           5 * time.Hour,
	RefreshRatio:  0.3,
	RetryInterval: 30 * time.Minute,
	Timeout:       time.Second,
	ScanInterval:  time.Minute,
	Pause:         0,
}

func newRefresher(t *testing.T, cli *fakeClient, now *time.Time, domains ...safeconfig.Domain) (*Refresher, *state.Store) {
	t.Helper()
	store, err := state.New("")
	require.NoError(t, err)
	r := New(cli, store, opts, domains...)
	r.now = func() time.Time { return *now }
	return r, store
}

func TestRefreshSuccessSchedulesByRatio(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cli := &fakeClient{}
	domain := safeconfig.Domain{Name: "foo.com"}
	r, store := newRefresher(t, cli, &now, domain)

	r.Refresh(context.Background())
	require.EqualValues(t, 1, cli.calls.Load())

	e, ok := store.Get("foo.com")
	require.True(t, ok)
	require.Equal(t, expiry, e.Expiry)
	require.Equal(t, now, e.LastSuccess)
	require.Empty(t, e.LastError)
	// 5h * (1 - 0.3) = 3.5h, +/-10%.
	delay := e.NextCheck.Sub(now)
	require.GreaterOrEqual(t, delay, 189*time.Minute)
	require.LessOrEqual(t, delay, 231*time.Minute)

	// Not due yet: no new lookup.
	now = now.Add(3 * time.Hour)
	r.Refresh(context.Background())
	require.EqualValues(t, 1, cli.calls.Load())

	// Due: looked up again.
	now = now.Add(time.Hour)
	r.Refresh(context.Background())
	require.EqualValues(t, 2, cli.calls.Load())
}

func TestRefreshFailureKeepsLastExpiryAndRetries(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cli := &fakeClient{}
	domain := safeconfig.Domain{Name: "foo.com"}
	r, store := newRefresher(t, cli, &now, domain)
	r.Refresh(context.Background())
	first, _ := store.Get("foo.com")

	now = first.NextCheck
	cli.err = errors.New("whois timeout")
	r.Refresh(context.Background())

	e, _ := store.Get("foo.com")
	require.Equal(t, expiry, e.Expiry)
	require.Equal(t, first.LastSuccess, e.LastSuccess)
	require.Equal(t, "whois timeout", e.LastError)
	require.Equal(t, now.Add(30*time.Minute), e.NextCheck)
	require.True(t, e.Valid(now, opts.TTL))
}

func TestRefreshDelayNeverExceedsTTL(t *testing.T) {
	r := New(&fakeClient{}, nil, Options{TTL: time.Hour, RefreshRatio: 0.01})
	for range 1000 {
		require.LessOrEqual(t, r.refreshDelay(), time.Hour)
	}
}

func TestRefreshStopsOnCancel(t *testing.T) {
	now := time.Now()
	cli := &fakeClient{}
	r, _ := newRefresher(t, cli, &now, safeconfig.Domain{Name: "a.com"}, safeconfig.Domain{Name: "b.com"})
	r.opts.Pause = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Refresh(ctx)
		close(done)
	}()
	require.Eventually(t, func() bool { return cli.calls.Load() == 1 }, time.Second, 10*time.Millisecond)
	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, cli.calls.Load())
}

func TestConfigured(t *testing.T) {
	r := New(&fakeClient{}, nil, opts,
		safeconfig.Domain{Name: "a.com"},
		safeconfig.Domain{Name: "b.es", Host: "whois.nic.es"},
	)
	d, ok := r.Configured("b.es", "")
	require.True(t, ok)
	require.Equal(t, "whois.nic.es", d.Host)
	_, ok = r.Configured("b.es", "other.host")
	require.False(t, ok)
	_, ok = r.Configured("c.com", "")
	require.False(t, ok)
}
