# Installation

## Prerequisites

- Go 1.27.4 or later (only to build from source)
- Veritas NetBackup 10.0 or later (API version auto-detected per site: 14.0 on 11.1/11.2, 13.0 on 11.0, 12.0 on 10.5, 10.0 on 10.0–10.4)
- Access to NetBackup REST API (port 1556 on the primary server)
- NetBackup API key (generated from NBU UI)

## Homebrew (macOS)

On macOS, install via the tap:

```bash
brew install fjacquet/tap/nbu_exporter
```

Or tap the repository first, then install:

```bash
brew tap fjacquet/tap
brew install nbu_exporter
```

Upgrade to the latest release:

```bash
brew upgrade nbu_exporter
```

> **Linux users:** Homebrew is not published for Linux. Use a [GitHub Release](#github-releases) binary, [Docker](#docker), or build [from source](#from-source).

## From Source

```bash
git clone https://github.com/fjacquet/nbu_exporter.git
cd nbu_exporter
make cli
```

The binary will be output to `bin/nbu_exporter`.

## Docker

```bash
# Exporter + Prometheus + Grafana, built from local source
docker compose up -d

# Same stack with the published image
# ghcr.io/${NBU_IMAGE_OWNER:-fjacquet}/nbu_exporter:${NBU_TAG:-latest}
docker compose -f docker-compose.ghcr.yml up -d

# Exporter image only
make docker
```

Set `NBU1_HOSTNAME` / `NBU1_APIKEY` in a gitignored `.env` first (`cp .env.example .env`).

See the [Docker deployment guide](../docker.md) for advanced usage.

## GitHub Releases

Download pre-built binaries from the [Releases page](https://github.com/fjacquet/nbu_exporter/releases).
