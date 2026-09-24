package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/metanovii/domain_exporter/v2/internal/client"
	"github.com/metanovii/domain_exporter/v2/internal/collector"
	"github.com/metanovii/domain_exporter/v2/internal/rdap"
	"github.com/metanovii/domain_exporter/v2/internal/refresher"
	"github.com/metanovii/domain_exporter/v2/internal/safeconfig"
	"github.com/metanovii/domain_exporter/v2/internal/state"
	"github.com/metanovii/domain_exporter/v2/internal/whois"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// nolint: gochecknoglobals
var (
	bind       = kingpin.Flag("bind", "addr to bind the server").Short('b').Default(":9222").String()
	debug      = kingpin.Flag("debug", "show debug logs").Default("false").Bool()
	format     = kingpin.Flag("logFormat", "log format to use").Default("console").Enum("json", "console")
	timeout    = kingpin.Flag("timeout", "timeout for each domain").Default("10s").Duration()
	configFile = kingpin.Flag("config", "configuration file").String()

	cacheTTL = kingpin.Flag("cache-ttl", "how long a successful result of a configured domain stays valid").
			Default("5h").Duration()
	cacheRefreshRatio = kingpin.Flag("cache-refresh-ratio", "after a successful check, check again when less than this share of cache-ttl is left").
				Default("0.3").Float64()
	cacheFailedRetry = kingpin.Flag("cache-failed-retry", "after a failed check, check again after this delay").
				Default("30m").Duration()
	cacheStateFile = kingpin.Flag("cache-state-file", "file to keep results across restarts; empty keeps them in memory only").
			String()
	cacheScanInterval = kingpin.Flag("cache-scan-interval", "how often to look for configured domains that are due for a check").
				Default("1m").Duration()
	cacheCheckPause = kingpin.Flag("cache-check-pause", "pause between two background checks").
			Default("1s").Duration()

	version = "dev"
)

func main() {
	kingpin.Version("domain_exporter version " + version)
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	if err := validateFlags(); err != nil {
		kingpin.Fatalf("%s", err)
	}

	urlPrefix, urlPrefixOK := os.LookupEnv("DOMAIN_EXPORTER_URL_PREFIX")
	if !urlPrefixOK {
		urlPrefix = ""
	}

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if *format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Debug().Msg("enabled debug mode")
	}

	log.Info().Msgf("starting domain_exporter %s", version)
	cfg, err := safeconfig.New(*configFile)
	if err != nil {
		log.Fatal().Err(err).Msg("error to create config")
	}

	wg := &sync.WaitGroup{}
	defer wg.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rdapClient, err := rdap.NewClient(cfg.RDAPServers)
	if err != nil {
		log.Fatal().Err(err).Msg("error to create rdap client")
	}
	static := client.NewStaticClient(client.NewMultiClient(rdapClient, whois.NewClient()), cfg.Domains...)
	var cli client.Client = static

	store, err := state.New(*cacheStateFile)
	if err != nil {
		log.Fatal().Err(err).Msg("error to load state")
	}
	if err := store.Prune(stateKeys(cfg.Domains)); err != nil {
		log.Fatal().Err(err).Msg("error to save state")
	}

	fresh := refresher.New(cli, store, refresher.Options{
		TTL:           *cacheTTL,
		RefreshRatio:  *cacheRefreshRatio,
		RetryInterval: *cacheFailedRetry,
		Timeout:       *timeout,
		ScanInterval:  *cacheScanInterval,
		Pause:         *cacheCheckPause,
	}, cfg.Domains...)

	stateCollector := collector.NewStateCollector(store, *cacheTTL, cfg.Domains...)
	prometheus.DefaultRegisterer.MustRegister(stateCollector)
	wg.Go(func() { fresh.Run(ctx) })

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	wg.Go(func() {
		for {
			select {
			case <-hup:
				reloadConfig(rdapClient, static, fresh, stateCollector, store)
			case <-ctx.Done():
				return
			}
		}
	})

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", probeHandler(cli, fresh))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(
			w, `
			<html>
			<head><title>Domain Exporter</title></head>
			<body>
				<h1>Domain Exporter</h1>
				<p><a href="%[1]s/metrics">Metrics</a></p>
				<p><a href="%[1]s/probe?target=google.com">probe google.com</a></p>
			</body>
			</html>
			`, urlPrefix,
		)
	})

	if err := runServerWithGracefullyShutdown(wg); err != nil {
		log.Fatal().Err(err).Msg("error starting server")
	}

	log.Info().Msg("domain exporter is finished")
}

func runServerWithGracefullyShutdown(wg *sync.WaitGroup) error {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	signal.Notify(signalChan, syscall.SIGINT)

	server := &http.Server{Addr: *bind}

	wg.Go(func() {
		sig := <-signalChan

		log.Warn().Msgf("got %s signal. Shutdown", sig)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("failed to shutdown http server")
		}
	})

	log.Info().Msgf("listening on %s", *bind)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

// probeHandler always looks the target up, bypassing the stored results.
// The result of a configured domain is also stored.
func probeHandler(cli client.Client, fresh *refresher.Refresher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		target := strings.TrimPrefix(params.Get("target"), "www.")
		host := params.Get("host")
		if target == "" {
			log.Error().Msg("target parameter missing")
			http.Error(w, "target parameter is missing", http.StatusBadRequest)
			return
		}

		domain := safeconfig.Domain{Name: target, Host: host}
		var onResult collector.Result
		if configured, ok := fresh.Configured(target, host); ok {
			domain = configured
			onResult = func(d safeconfig.Domain, expiry time.Time, err error, duration time.Duration) {
				fresh.Record(d, expiry, err, duration)
			}
		}

		registry := prometheus.NewRegistry()
		registry.MustRegister(collector.NewDomainCollector(cli, *timeout, onResult, domain))

		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	}
}

func validateFlags() error {
	switch {
	case *cacheTTL <= 0:
		return fmt.Errorf("--cache-ttl must be positive")
	case *cacheRefreshRatio <= 0 || *cacheRefreshRatio >= 1:
		return fmt.Errorf("--cache-refresh-ratio must be between 0 and 1")
	case *cacheFailedRetry <= 0:
		return fmt.Errorf("--cache-failed-retry must be positive")
	case *cacheScanInterval <= 0:
		return fmt.Errorf("--cache-scan-interval must be positive")
	case *cacheCheckPause < 0:
		return fmt.Errorf("--cache-check-pause must not be negative")
	}
	return nil
}

func stateKeys(domains []safeconfig.Domain) []string {
	keys := make([]string, 0, len(domains))
	for _, d := range domains {
		keys = append(keys, state.Key(d.Name, d.Host))
	}
	return keys
}

// reloadConfig reads the configuration file again (on SIGHUP) and applies the
// new domains and RDAP servers. On error the current configuration is kept.
func reloadConfig(
	rdapClient *rdap.Client,
	static *client.StaticClient,
	fresh *refresher.Refresher,
	stateCollector *collector.StateCollector,
	store *state.Store,
) {
	if *configFile == "" {
		log.Warn().Msg("got SIGHUP, but no config file is set")
		return
	}
	cfg, err := safeconfig.New(*configFile)
	if err != nil {
		log.Error().Err(err).Msg("failed to reload config, keeping the current one")
		return
	}
	if err := rdapClient.SetServers(cfg.RDAPServers); err != nil {
		log.Error().Err(err).Msg("failed to reload config, keeping the current one")
		return
	}
	static.SetDomains(cfg.Domains...)
	fresh.SetDomains(cfg.Domains...)
	stateCollector.SetDomains(cfg.Domains...)
	if err := store.Prune(stateKeys(cfg.Domains)); err != nil {
		log.Error().Err(err).Msg("failed to save state")
	}
	log.Info().Msgf("config reloaded: %d domains", len(cfg.Domains))
}
