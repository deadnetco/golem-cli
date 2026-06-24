# golem CLI

A small, static binary that builders run inside their golem devcontainer /
Codespace to drive their app through the **golem control-plane v1 API**. It is a
**thin HTTP client** — all privilege lives in the API; the CLI just shapes
requests, attaches the Bearer key, and renders the result.

> Distributed exactly like [`golem-migrate`](https://github.com/deadnetco/golem-migrate):
> a pinned GitHub release the starter dev containers `curl` + `chmod +x` on
> `postStart`. It reads its config from two Codespaces secrets (`GOLEM_API_KEY`,
> `GOLEM_API_URL`) golem mints at provision time.

## Install / build

```sh
go build -o golem .
```

Requires Go 1.26+. No third-party dependencies — standard library only.

A release binary is stamped with its version:

```sh
go build -ldflags "-X main.version=v0.1.0" -o golem .
```

## Configuration

Both come from the environment (set as Codespaces secrets in a builder's repo):

| Variable        | Required | Default                              |
|-----------------|----------|--------------------------------------|
| `GOLEM_API_KEY` | yes      | —                                    |
| `GOLEM_API_URL` | no       | `https://platform.tools.deadnet.co`  |

Every call is `GET/POST/PUT/DELETE {GOLEM_API_URL}/api/v1/<route>` with the
header `Authorization: Bearer $GOLEM_API_KEY`. If `GOLEM_API_KEY` is unset the
CLI prints a friendly message and exits non-zero **without** making a call:

```
golem: no GOLEM_API_KEY in this environment — your Codespace may predate the
key; ask an admin to reissue, or restart the Codespace
```

## Commands

| Command | HTTP call | Notes |
|---|---|---|
| `golem whoami` | `GET /api/v1/whoami` | slug, owner, status, key name |
| `golem status` | `GET /api/v1/status` | publish state: config-dirty count, code-dirty, publishing |
| `golem publish [--force]` | `POST /api/v1/publish` | rebuild (if HEAD moved) + reconcile staged config + roll |
| `golem config list` | `GET /api/v1/config` | env + secret keys (secret values never returned) |
| `golem config get KEY` | `GET /api/v1/config` (filtered client-side) | |
| `golem config set KEY=VALUE` | `PUT /api/v1/config` `{secret:false}` | staged — run `golem publish` |
| `golem env set KEY=VALUE` | alias of `config set` | |
| `golem secret set KEY[=VALUE]` | `PUT /api/v1/config` `{secret:true}` | value read from **stdin** when omitted (never required on argv) |
| `golem secret rm KEY` / `golem config rm KEY` | `DELETE /api/v1/config?key=KEY` | stages a removal |
| `golem logs [--stream console\|errors\|ci] [--follow]` | `GET /api/v1/logs?stream=…` | default `console`; **`--follow` polls the snapshot** (see below) |
| `golem schedules list` | `GET /api/v1/schedules` | golem.json-declared schedules |
| `golem schedules sync` | `POST /api/v1/schedules` | reconcile `golem.json` @ HEAD (build-free) |
| `golem webhooks list` | `GET /api/v1/webhooks` | inbound webhook endpoints, each with its public URL |
| `golem webhooks add LABEL PATH` | `POST /api/v1/webhooks` | create an endpoint; returns the `hooks.deadnet.co/<id>` URL |
| `golem webhooks rm ID` | `DELETE /api/v1/webhooks?id=ID` | remove an endpoint (scoped to this app) |
| `golem restart` | `POST /api/v1/restart` | best-effort roll of the app's machine |
| `golem open` | `GET /api/v1/whoami` (for the slug) | prints + `open`/`xdg-open`s `https://<slug>.tools.deadnet.co` |
| `golem upgrade` | (none — GitHub releases) | replace the running binary with the latest release (sudo-escalates if its dir is root-owned) |
| `golem version` | (none) | prints the stamped version |
| `golem help` | (none) | usage |

### Update notices

On an interactive run, `golem` checks (at most ~once/day, cached in `~/.cache/golem/version.json`)
whether a newer release exists and prints a one-line notice to **stderr** when you're behind:

```
golem v0.1.3 is available (you have v0.1.2) — run `golem upgrade` to update.
```

The check uses the `releases/latest` redirect (no GitHub API → no rate limit), never blocks output
(stderr only, skipped when stdout isn't a TTY), and is disabled by `GOLEM_NO_UPDATE_CHECK`. In the
starter dev containers the CLI also auto-refreshes to latest on container start, so the notice is
mainly a heads-up for a new release mid-session.

### A note on `logs --follow`

The log endpoints are **snapshot** fetchers, not live streams. `--follow`
re-fetches the snapshot every few seconds and re-renders it; it is not a tail.
Press Ctrl-C to stop.

### Error handling

A non-2xx response carries the control-plane's uniform `{"error": "..."}`
envelope; the CLI decodes it, prints it to stderr, and exits non-zero. A
transport failure prints a clear network error. Successful responses are
rendered as a friendly summary on stdout.

## Examples

```sh
export GOLEM_API_KEY=...            # normally a Codespaces secret

golem whoami
golem status
golem config set FEATURE_FLAG=on    # staged
golem secret set OPENAI_KEY         # prompts via stdin
golem publish                       # apply staged config + rebuild if code moved
golem logs --stream errors
golem schedules sync
golem webhooks add Stripe /webhooks/stripe   # prints the URL to paste into Stripe
```

## Project layout

```
.
├── main.go                       # CLI entrypoint: stamps `version`, dispatches to internal/cli
├── internal/
│   ├── cli/                      # subcommand dispatch + rendering (unit-tested via httptest)
│   │   ├── cli.go
│   │   └── cli_test.go
│   └── client/                   # the thin v1 HTTP client + typed response shapes
│       ├── client.go             # New(), Do(), Bearer auth, {error} envelope decoding
│       ├── api.go                # per-command calls + response structs (mirror the route handlers)
│       └── client_test.go
├── go.mod
├── .gitignore
└── README.md
```

## Tests

```sh
go test ./...
```

Tests spin a fake API with `net/http/httptest`, point `GOLEM_API_URL` at it, and
assert each command issues the right method/path/body, sends the Bearer header,
surfaces the `{"error"}` envelope, and guards the missing-`GOLEM_API_KEY` case —
all with **no live control plane required**.
