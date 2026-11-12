# systemd (EL9 host)

Docker is **not** required — the exporter is a single static (`CGO_ENABLED=0`) binary. For a
non-container deployment on Enterprise Linux 9, use the unit shipped in `deploy/`.

## Install

```bash
# user + binary
sudo useradd --system --no-create-home --shell /usr/sbin/nologin nbu
sudo install -m 0755 bin/nbu_exporter /usr/local/bin/nbu_exporter

# config + secrets
sudo install -d -o root -g nbu -m 0750 /etc/nbu_exporter
sudo install -m 0640 -o root -g nbu config.yaml /etc/nbu_exporter/config.yaml
sudo install -m 0600 -o root -g nbu deploy/nbu_exporter.env.example /etc/nbu_exporter/nbu_exporter.env
# edit /etc/nbu_exporter/nbu_exporter.env: set NBU1_HOSTNAME=master.my.domain and NBU1_APIKEY=...
# (the shipped config.yaml references both; an unset ${NBU1_HOSTNAME} fails startup)

# service
sudo install -m 0644 deploy/nbu_exporter.service /etc/systemd/system/nbu_exporter.service
sudo systemctl daemon-reload
sudo systemctl enable --now nbu_exporter
```

Keep `logName: "stdout"` (the shipped default) so logs go to the journal. Set `server.host` to
`"0.0.0.0"` (shipped default) when Prometheus scrapes from another host, `"localhost"` only for a
local scrape.

TLS: keep verification on and trust the NetBackup CA — install it in the system trust store, or add
`Environment=SSL_CERT_DIR=/etc/ssl/certs:/etc/nbu_exporter/ca` to the unit (drop-in) with the PEM in
`/etc/nbu_exporter/ca/`. Setting `NBU1_SKIP_CERTIFICATE=true` is a last resort, for a trusted
network only.

## Operate

```bash
journalctl -u nbu_exporter -f         # follow logs
sudo systemctl reload nbu_exporter    # SIGHUP: re-read and validate config.yaml only (invalid edits are rejected, running config kept)
sudo systemctl restart nbu_exporter   # apply config changes (sites, collectors, intervals, allowlist)
sudo systemctl status nbu_exporter
```

## Hardening

The unit runs as the unprivileged `nbu` user inside a sandbox:

- `NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`
- `PrivateTmp`, `PrivateDevices`, `ProtectKernel*`, `ProtectControlGroups`
- `RestrictAddressFamilies=AF_INET AF_INET6`, `RestrictNamespaces`, `LockPersonality`
- `Restart=on-failure`

Secrets are supplied through the `EnvironmentFile` and referenced as `${NBU1_HOSTNAME}` /
`${NBU1_APIKEY}` in `config.yaml` (`NBU2_*` for a second `nbuservers:` entry). Keep that file mode
`0600`.

## macOS (launchd / Homebrew)

On macOS run it under **launchd** (the systemd equivalent). `brew services` is not wired up:
the Homebrew cask only installs the binary on your PATH — it defines no service block — so
register a `launchd` job yourself, e.g. `~/Library/LaunchAgents/com.fjacquet.nbu_exporter.plist`
with `ProgramArguments` `[/opt/homebrew/bin/nbu_exporter, --config, <path>/config.yaml]` and
`RunAtLoad`/`KeepAlive` set, then `launchctl load` it.
