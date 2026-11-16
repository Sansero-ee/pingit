# pingit

Parallel ICMP ping monitor for Linux with JSON Lines logging and a
terminal UI. Pings multiple targets concurrently (one goroutine per
target) via raw ICMP sockets (`golang.org/x/net/icmp`), measures RTT and
packet loss, and writes one JSON record per probe.

## Features

- Targets: IPv4/IPv6 addresses and hostnames (hostnames are resolved
  before the run starts; an unresolvable target is marked as failed
  without stopping the others).
- Raw ICMP via `golang.org/x/net/icmp` + `ipv4`/`ipv6` — the system
  `ping` binary is never invoked. Falls back to unprivileged datagram
  ICMP sockets when raw sockets are unavailable.
- Parallel probing: every target runs in its own goroutine.
- User-defined duration and interval (e.g. 60 s / 1 s).
- JSON Lines log, one record per ping:
  `timestamp`, `target`, `resolved_ip`, `seq`, `rtt_ms`, `success`,
  `error`. Writes are serialized through a single writer goroutine;
  on write errors it retries with backoff — a disk failure never
  interrupts probing. SIGINT/SIGTERM (or `ctrl+c` in the TUI) stop the
  run gracefully: the buffer is flushed and the file is closed.
- Target limit: 10 by default; the TUI checkbox "allow up to 100
  targets" raises it to 100.

## Build

Requires Go 1.21+.

```sh
CGO_ENABLED=0 go build -o pingit ./cmd
```

## ICMP privileges

Raw ICMP sockets require `cap_net_raw`. For unprivileged use:

```sh
sudo setcap cap_net_raw=eip ./pingit
```

To revoke:

```sh
sudo setcap -r ./pingit
```

Without the capability the tool falls back to unprivileged datagram
ICMP sockets (`udp4`/`udp6`), which work only if the kernel allows your
group to use them (see `net.ipv4.ping_group_range`).

## Usage

The CLI accepts only `--help` and `--version`; any other argument
prints usage and exits with status 2.

```sh
./pingit --help
./pingit --version
./pingit            # opens the TUI
```

### TUI

Fields:

- **Targets** — one target per line (IPv4, IPv6 or hostname).
- **Duration** — total probing time, e.g. `60s`, `5m`.
- **Interval** — delay between probes, e.g. `1s`.
- **Log file** — JSONL output path (default:
  `~/pinglog-YYYYMMDD-HHMMSS.jsonl`).
- **Config file** — optional input file, loaded with `ctrl+l`.

While running, the TUI shows per target: sent / received / lost % and
min / avg / max RTT.

Keys:

| Key              | Action                                          |
|------------------|-------------------------------------------------|
| `tab`/`shift+tab`| next / previous field                           |
| `space`          | toggle "allow up to 100 targets"                |
| `ctrl+l`         | load targets/duration/interval from config file |
| `ctrl+s`         | start the run                                   |
| `ctrl+c`         | stop the run gracefully (log is flushed) / quit |

## Configuration file

Instead of typing targets, load them from a text file: one target per
line; the last line sets `duration` and `interval`. Blank lines and
`#` comments are ignored.

`examples/targets.txt`:

```
# one target per line: IPv4, IPv6 or hostname
8.8.8.8
1.1.1.1
2001:4860:4860::8888
example.com
duration=60s, interval=1s
```

In the TUI, type the path into the **Config file** field and press
`ctrl+l`.

## Log format

JSON Lines, one record per ping:

```json
{"timestamp":"2025-01-01T12:00:00.123456789+01:00","target":"8.8.8.8","resolved_ip":"8.8.8.8","seq":1,"rtt_ms":10.412,"success":true}
{"timestamp":"2025-01-01T12:00:01.124001000+01:00","target":"example.com","resolved_ip":"93.184.216.34","seq":1,"rtt_ms":0,"success":false,"error":"timeout"}
```

## Tests

```sh
go test -race ./...
```

## Layout

```
cmd/main.go               entry point, --help/--version handling
internal/config           config file parsing, target limit validation
internal/pinger           ICMP probing, resolution, per-target stats
internal/logwriter        thread-safe JSONL writer with retry
internal/tui              Bubble Tea interface
```
