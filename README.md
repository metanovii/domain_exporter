# domain_exporter

Exports the expiration time of your domains as prometheus metrics.

#### Environment variables

- `DOMAIN_EXPORTER_URL_PREFIX` — use when HTTP endpoint served with a prefix,
  e.g.: For this endpoint `http://example.org/exporters/domains` set to
  `/exporters/domains`. Not really required since useful only to prevent
  breaking human-oriented links. Defaults to empty string.

## Configuration

The exporter can be used in two ways, which can be combined.

### Configured domains (`/metrics`)

Put the domains into a configuration file (`host` is optional):

```yaml
domains:
- example.com
- name: example.es
  host: whois.nic.es # custom whois server for this domain
- name: example.ph
  host: whois.dot.ph
- name: example.eu
  expiry_date: 2027-09-01 # no lookup, see below
```

`expiry_date` (`YYYY-MM-DD`) is for domains whose registry publishes the date
neither in whois nor in RDAP (for example `.eu`). The domain is then reported
with this date and never looked up. A date in the past is logged as a warning
on start; an invalid date stops the exporter.

Send `SIGHUP` to reload the configuration file without a restart:

```bash
kill -HUP $(pidof domain_exporter)
```

Domains, `expiry_date` and `rdap_servers` are applied at once; results of
domains that stay in the file are kept. If the new file is invalid, the
current configuration is kept and the error is logged.

```bash
domain_exporter --config=domains.yaml
```

The exporter checks these domains in the background and keeps the results.
`/metrics` only reports the stored results and never does a lookup itself,
so a scrape is fast and cannot time out because of a slow whois server.

On the Prometheus side:

```yaml
- job_name: domain
  scrape_interval: 2m
  scrape_timeout: 60s
  static_configs:
    - targets:
      - localhost:9222 # domain_exporter address
```

### On-demand probes (`/probe`)

`/probe?target=<domain>` always does a fresh lookup and does not use the
stored results. If the domain is in the configuration file, the result is also
stored. The optional `host` parameter sets the whois server.

The exporter connects to whatever whois server `host` names, so anyone who can
reach `/probe` can make it open connections to arbitrary hosts on port 43.
Listen on a local or internal address only (for example `-b 127.0.0.1:9222`).

It works more or less like Prometheus's
[blackbox_exporter](https://github.com/prometheus/blackbox_exporter):

```yaml
- job_name: domain-probe
  metrics_path: /probe
  relabel_configs:
    - source_labels: [__address__]
      target_label: __param_target
    - target_label: __address__
      replacement: localhost:9222 # domain_exporter address
  static_configs:
    - targets:
      - example.com
      - example.org
```

To pass a custom whois server, add `params: {host: [whois.example.net]}` to
the job.

### How configured domains are refreshed

Each domain is checked on its own schedule:

- after a successful check, it is checked again when less than
  `--cache-refresh-ratio` of `--cache-ttl` is left, with a random spread of
  +/-10% so that domains do not all refresh at the same moment.
  With the defaults (`5h`, `0.3`) that is about 3.5h after the check;
- after a failed check, it is checked again after `--cache-failed-retry`;
- a successful result stays valid for `--cache-ttl` even if later checks
  fail, so a single whois error does not turn into an alert. When it is older
  than `--cache-ttl`, the domain is reported as failed;
- a domain that has never been checked yet is not reported at all.

Checks run one after another with `--cache-check-pause` between them, to stay
below the rate limits of whois servers.

With `--cache-state-file` the results are written to a JSON file and loaded on
start, so a restart does not check every domain again. Entries of domains
removed from the configuration are dropped on start. Without it the results
are kept in memory only.

### RDAP servers

The expiration date is looked up with RDAP first and with whois if RDAP fails.
RDAP servers are taken from the
[IANA bootstrap registry](https://data.iana.org/rdap/dns.json). Some
registries run an RDAP server that is not listed there (for example `.kz`);
it can be set in the configuration file, per TLD or domain suffix:

```yaml
rdap_servers:
  kz: https://rdap.nic.kz/
domains:
- example.kz
```

The longest matching suffix wins. Internationalized names can be written in
either form (`рф` or `xn--p1ai`).

### Flags

| Flag | Default | Description |
|---|---|---|
| `--config` | | configuration file |
| `-b`, `--bind` | `:9222` | address to listen on |
| `--timeout` | `10s` | timeout of one lookup |
| `--cache-ttl` | `5h` | how long a successful result of a configured domain stays valid |
| `--cache-refresh-ratio` | `0.3` | after a successful check, check again when less than this share of `--cache-ttl` is left |
| `--cache-failed-retry` | `30m` | after a failed check, check again after this delay |
| `--cache-state-file` | | file to keep results across restarts; empty keeps them in memory only |
| `--cache-scan-interval` | `1m` | how often to look for configured domains that are due for a check |
| `--cache-check-pause` | `1s` | pause between two background checks |
| `--debug` | `false` | show debug logs |
| `--logFormat` | `console` | `console` or `json` |

### Metrics

| Metric | Description |
|---|---|
| `domain_expiry_days` | days until the domain expires, `-1` if the check failed |
| `domain_expiry_time_seconds` | expiration time as a Unix timestamp; absent if the check failed |
| `domain_probe_success` | `1` if there is a valid result, `0` otherwise |
| `domain_probe_duration_seconds` | how long the last lookup took |
| `domain_last_success_timestamp_seconds` | when the expiration time was last obtained |

Since `domain_expiry_days` is `-1` for a failed check, filter it with
`domain_probe_success` in alerting rules:

```
domain_expiry_days < 30 and on(domain) domain_probe_success == 1
```

Alerting rules examples can be found in the [_examples](_examples) folder,
together with example configuration files.

## Install

**Container image** (linux/amd64, linux/arm64):

```bash
docker run --rm -p 9222:9222 ghcr.io/metanovii/domain_exporter:latest
```

**Binaries** for Linux, macOS and Windows are on the
[releases page](https://github.com/metanovii/domain_exporter/releases).
`checksums.txt` is signed with [cosign](https://github.com/sigstore/cosign)
(keyless, GitHub Actions identity):

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/metanovii/domain_exporter/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

**From source:**

```bash
go install github.com/metanovii/domain_exporter/v2@latest
# or
docker build -f Dockerfile.dev -t domain_exporter .
```

## Releasing

Push a `v*` tag; the `release` workflow runs GoReleaser
(`.goreleaser.yml`): binaries, archives, signed checksums, the GitHub release
and the image in `ghcr.io/metanovii/domain_exporter`. A local dry run:

```bash
goreleaser release --snapshot --clean --skip=sign
```
