---
name: golem
description: Use when operating a golem app — deploy/ship/publish via the golem CLI (`golem publish`), stage config or secrets, add or edit schedules or webhooks, pull the dev env, or tail logs. Not for generic coding unrelated to golem.
---

# golem

This app runs on the **golem** platform. You operate it with the **golem CLI** (preinstalled in the devcontainer, authed via `GOLEM_API_KEY`; base URL `GOLEM_API_URL` defaults to prod). The API holds all privilege — the CLI shapes the request and renders the result. Missing `GOLEM_API_KEY` fails fast (non-zero exit). Run `golem help` for the full command list; the notes below are the non-obvious overrides.

## Do it the golem way (read this first)

- **Deploy = `golem publish`, NOT git-push-to-prod.** `git push` only SAVES code; golem never watches the repo and nothing ships until you Publish. No auto-deploy-on-push.
- **Publish is a fixed, FAIL-CLOSED pipeline** that stops at the first failure (the previous version keeps serving; retry-safe):
  - rebuild image only if repo HEAD moved past the last-built commit (`--force` always rebuilds),
  - run pending `db/migrations/*.sql`,
  - apply staged config/secrets,
  - ONE rolling restart.
- **Config & secrets are STAGED, not live.** `config`/`env`/`secret set` only stage a change; it takes effect only on the next `golem publish` (which restarts the app). `env` = plaintext; `secret` = encrypted, write-only, never shown again (keep your own copy).
- **Outbound is default-drop egress-locked in the kernel (nftables) — unbypassable**, even from your own code/shell/deps:
  - Private 6PN hosts (`DATABASE_URL`, `REDIS_URL`, LiteLLM gateway) work with zero config.
  - Every public host needs BOTH: route through `HTTPS_PROXY`/`HTTP_PROXY` AND an `external` egress grant for the exact bare host (Network tab; no scheme/port/path/wildcard). Grants are live immediately, no rebuild.
- **DB schema changes are ENFORCED forward-only SQL files** in `db/migrations/NNNN_name.sql`. Applied files are immutable (checksummed) — never edit one; fix forward. golem runs them against prod before new code serves.
- **Don't set platform-managed vars** (`DATABASE_URL`, `REDIS_URL`, `AWS_*`, `LITELLM_*`, `RESEND_*`, `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY`, `SENTRY_DSN`, `GOLEM_*`, `APP_ENV`, `KRAKEND_*`) — a same-named entry can silently override golem's value.

## Ship a code change

1. Edit + `git add -A && git commit -m "…" && git push` (SAVES only, does not deploy).
2. `golem publish` (add `--force` to rebuild with no new commit). It **follows the run to completion** and exits **non-zero** on failed/blocked/interrupted, printing the error + build-log tail. `--no-wait` returns immediately; Ctrl-C stops following but does NOT cancel the server-side publish.
3. On failure, read the printed error/tail or watch Logs → Audit. Live at `https://<slug>.tools.deadnet.co`.

Check state anytime with `golem status`; tail with `golem logs [--stream console|errors|ci] [--follow]`.

## Commands

Every command operates on the one app this `GOLEM_API_KEY` authorizes; `golem help` has the full flags.

**Ship & inspect**
- `golem publish [--force] [--no-wait]` — build-if-HEAD-moved → migrate → apply config → one restart
- `golem status` — anything to ship? (config-dirty / code-dirty / publishing)
- `golem restart` — best-effort roll the app's machine
- `golem whoami` — who am I + which app this key authorizes
- `golem open` — print + open the app's public URL

**Config & secrets** (staged; applied on the next publish)
- `golem config list | get KEY | set KEY=VALUE | rm KEY` — env vars (secret values never shown)
- `golem env set KEY=VALUE` — alias of `config set`
- `golem secret set KEY[=VALUE]` — stage a secret (value read from stdin if omitted); `golem secret rm KEY`

**Schedules & webhooks** (declared in `golem.json`)
- `golem schedules list | sync` — reconcile `golem.json` schedules @ HEAD
- `golem webhooks list | add LABEL PATH | rm ID` — inbound endpoints (`add` prints the public URL to give a provider)

**Dev & observability**
- `golem dev pull` — hydrate `.env.golem` with this app's dev values
- `golem logs [--stream console|errors|ci] [--follow]` — snapshot the app's logs

**CLI**
- `golem upgrade` — update the CLI to the latest release
- `golem skill install [--global]` — (re)install this skill; it refreshes automatically after an upgrade

## More detail — per topic

Full recipes/schemas live in `docs/builders/` (rendered at **/docs** in the golem panel). Don't inline their constants here — they drift.

- **Deploying, publish flags, `status`, `restart`** → `docs/builders/deploying.md`
- **`config` / `env` / `secret` (staging, stdin secrets), platform-managed vars** → `docs/builders/config-and-secrets.md`
- **`golem.json` schema, `schedules list`/`sync`, cadence/target/`timeoutMinutes`/`memoryMb`/`size`** → `docs/builders/schedules.md`
- **`webhooks add`/`list`/`rm`, signing (`X-Golem-*`, `GOLEM_WEBHOOK_SECRET`), body cap, reply window** → `docs/builders/webhooks.md`
- **`dev pull` / `.env.golem`, dev parity** → `docs/builders/developing.md`
- **Egress grants, proxies** → `docs/builders/egress-and-networking.md`
- **Migrations** → `docs/builders/database-and-migrations.md`
- **Logs & observability** → `docs/builders/logs-and-observability.md`
- **LLM budgets, email, integrations** → `docs/builders/{llm,email,connections}.md`
