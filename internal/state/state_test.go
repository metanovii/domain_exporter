package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	entry := Entry{
		Expiry:      now.AddDate(1, 0, 0),
		LastSuccess: now,
		LastAttempt: now,
		Duration:    time.Second,
		NextCheck:   now.Add(time.Hour),
	}

	s, err := New(path)
	require.NoError(t, err)
	require.NoError(t, s.Set(Key("a.com", ""), entry))
	require.NoError(t, s.Set(Key("b.es", "whois.nic.es"), Entry{LastError: "boom"}))

	loaded, err := New(path)
	require.NoError(t, err)
	got, ok := loaded.Get("a.com")
	require.True(t, ok)
	require.True(t, entry.Expiry.Equal(got.Expiry))
	require.True(t, entry.NextCheck.Equal(got.NextCheck))
	got, ok = loaded.Get("b.es@whois.nic.es")
	require.True(t, ok)
	require.Equal(t, "boom", got.LastError)

	require.NoError(t, loaded.Prune([]string{"a.com"}))
	again, err := New(path)
	require.NoError(t, err)
	_, ok = again.Get("b.es@whois.nic.es")
	require.False(t, ok)
	_, ok = again.Get("a.com")
	require.True(t, ok)

	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, files, 1, "temporary files must be removed")
}

func TestStoreMissingFile(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	_, ok := s.Get("a.com")
	require.False(t, ok)
}

func TestStoreBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	_, err := New(path)
	require.Error(t, err)
}

func TestEntryValid(t *testing.T) {
	now := time.Now()
	require.False(t, Entry{}.Valid(now, time.Hour))
	require.True(t, Entry{LastSuccess: now.Add(-59 * time.Minute)}.Valid(now, time.Hour))
	require.False(t, Entry{LastSuccess: now.Add(-61 * time.Minute)}.Valid(now, time.Hour))
}
