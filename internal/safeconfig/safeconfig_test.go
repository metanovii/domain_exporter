package safeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	type args struct {
		pathToFile string
	}
	tests := []struct {
		name    string
		args    args
		want    SafeConfig
		wantErr bool
	}{
		{
			name: "Empty file name. Default",
			args: args{
				"",
			},
			want:    SafeConfig{},
			wantErr: false,
		},
		{
			name: "Empty file name",
			args: args{
				"file-which-does-not-exist.yaml",
			},
			want:    SafeConfig{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.args.pathToFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("New() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSafeConfig_Reload(t *testing.T) {
	tests := []struct {
		name        string
		cfg         SafeConfig
		fileContent string
		wantErr     bool
	}{
		{
			name:        "Load empty file",
			cfg:         SafeConfig{},
			fileContent: "",
			wantErr:     false,
		},
		{
			name:        "yaml is not valid",
			cfg:         SafeConfig{},
			fileContent: "yaml is not correct",
			wantErr:     true,
		},
		{
			name: "Vaidd yaml",
			cfg: SafeConfig{
				Domains: []Domain{{Name: "google.com", Host: ""}},
			},
			fileContent: `
domains:
- google.com`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.CreateTemp(os.TempDir(), "temp.*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(file.Name())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			})
			log.Info().Msg(tt.fileContent)

			_, err = f.WriteString(tt.fileContent)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(file.Name()); err != nil {
					t.Fatal(err)
				}
			})

			cfg, err := New(file.Name())
			if (err != nil) != tt.wantErr {
				t.Errorf("SafeConfig.New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(cfg, tt.cfg) {
				t.Errorf("cfg is not equal:\n got %s\n expected: %s", cfg, tt.cfg)
			}
		})
	}
}

func TestExpiryDate(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	require.NoError(t, os.WriteFile(good, []byte("domains:\n- name: example.eu\n  expiry_date: 2027-09-01\n- example.com\n"), 0o600))
	cfg, err := New(good)
	require.NoError(t, err)
	d, ok := cfg.Domains[0].Expiry()
	require.True(t, ok)
	require.Equal(t, time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC), d)
	_, ok = cfg.Domains[1].Expiry()
	require.False(t, ok)

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("domains:\n- name: example.eu\n  expiry_date: 01.09.2027\n"), 0o600))
	_, err = New(bad)
	require.ErrorContains(t, err, "invalid expiry_date")
}
