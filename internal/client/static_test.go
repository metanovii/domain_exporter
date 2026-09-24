package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/stretchr/testify/require"
)

type failingClient struct{}

func (failingClient) ExpireTime(context.Context, string, string) (time.Time, error) {
	return time.Time{}, errors.New("lookup")
}

func TestStaticClient(t *testing.T) {
	cli := NewStaticClient(failingClient{},
		safeconfig.Domain{Name: "example.eu", ExpiryDate: "2027-09-01"},
		safeconfig.Domain{Name: "example.com"},
	)

	got, err := cli.ExpireTime(context.Background(), "example.eu", "")
	require.NoError(t, err)
	require.Equal(t, time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC), got)

	_, err = cli.ExpireTime(context.Background(), "example.com", "")
	require.EqualError(t, err, "lookup")
}
