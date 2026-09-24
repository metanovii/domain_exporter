package safeconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type Domain struct {
	Name string `yaml:"name"`
	Host string `yaml:"host,omitempty"`
	// ExpiryDate (YYYY-MM-DD) is used instead of a lookup, for domains whose
	// registry publishes the date neither in whois nor in RDAP.
	ExpiryDate string `yaml:"expiry_date,omitempty"`
}

// expiryDateLayout is the format of Domain.ExpiryDate.
const expiryDateLayout = time.DateOnly

// Expiry returns the parsed ExpiryDate and whether it is set.
func (d Domain) Expiry() (time.Time, bool) {
	if d.ExpiryDate == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(expiryDateLayout, d.ExpiryDate)
	return t, err == nil
}

type domainAlias Domain

func (a *Domain) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var d domainAlias
	if err := unmarshal(&d); err == nil {
		*a = Domain(d)
		return nil
	}

	var ds string
	if err := unmarshal(&ds); err != nil {
		return err
	}
	*a = Domain{Name: ds}
	return nil
}

type SafeConfig struct {
	Domains     []Domain          `yaml:"domains"`
	RDAPServers map[string]string `yaml:"rdap_servers"`
}

func New(pathToFile string) (SafeConfig, error) {
	cfg := SafeConfig{}
	if pathToFile == "" {
		log.Debug().Msg("config file path is empty, skip loading")
		return cfg, nil
	}

	if err := cfg.Reload(pathToFile); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (cfg *SafeConfig) Reload(pathToFile string) error {
	log.Info().Msgf("trying to load config from file %s", pathToFile)

	filename, err := filepath.Abs(pathToFile)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of file %s: %w", pathToFile, err)
	}
	log.Debug().Msgf("absolute path of config file is %s", filename)

	yamlFile, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	err = yaml.Unmarshal(yamlFile, cfg)
	if err != nil {
		return fmt.Errorf("failed to unmarshal file: %w", err)
	}

	for _, d := range cfg.Domains {
		if d.ExpiryDate == "" {
			continue
		}
		t, err := time.Parse(expiryDateLayout, d.ExpiryDate)
		if err != nil {
			return fmt.Errorf("invalid expiry_date %q of %s, expected YYYY-MM-DD: %w", d.ExpiryDate, d.Name, err)
		}
		if t.Before(time.Now()) {
			log.Warn().Msgf("expiry_date %s of %s is in the past", d.ExpiryDate, d.Name)
		}
	}

	log.Debug().Msgf("config file is loaded:\n %s", *cfg)
	return nil
}
