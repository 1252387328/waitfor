# waitfor

Wait until your dependencies are actually ready — then start your app.

```console
$ waitfor db:5432 http://localhost:8080/healthz -- npm run e2e
waitfor: waiting for tcp://db:5432 (dial tcp 172.18.0.2:5432: connect: connection refused)
waitfor: waiting for http://localhost:8080/healthz (status 503)
waitfor: tcp://db:5432 is up
waitfor: http://localhost:8080/healthz is up
waitfor: ready after 4.183s
> e2e tests running…
```

A single static binary (no shell, no `nc`, no `curl` needed) — handy in slim Docker images, docker-compose entrypoints
and CI jobs.

## Install

```bash
go install github.com/1252387328/waitfor@latest
```

## Targets

| Target | Ready when |
| --- | --- |
| `host:port`, `tcp://host:port`, `:8080` | a TCP connection succeeds |
| `http://…`, `https://…` | the URL answers with a status below 400 |
| `file:///path` | the file or directory exists |

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-t` | `60s` | overall timeout (`0` = wait forever) |
| `-i` | `1s` | delay between attempts |
| `-a` | `5s` | timeout for a single attempt |
| `-any` | off | continue as soon as **one** target is ready |
| `-k` | off | skip TLS verification for `https://` targets |
| `-q` | off | no progress output |

Everything after `--` is executed once the targets are ready. Signals (Ctrl-C, `SIGTERM`) are forwarded to it and
`waitfor` exits with the command's exit code. Without a command it exits `0` when ready, `1` on timeout and `2` on
bad arguments.

## docker-compose

```yaml
services:
  api:
    build: .
    command: ["waitfor", "-t", "90s", "db:5432", "cache:6379", "--", "./api"]
```

## Development

```bash
go test ./...
```

## License

MIT
