package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// The response shapes below mirror golem-cp's src/app/api/v1/*/route.ts exactly.
// Fields the CLI does not render are still declared so a full JSON dump round-trips.

// Whoami is GET /api/v1/whoami.
type Whoami struct {
	Slug   string `json:"slug"`
	Owner  string `json:"owner"`
	Status string `json:"status"`
	Key    struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	} `json:"key"`
}

// Status is GET /api/v1/status — app lifecycle status + the publish rollup.
type Status struct {
	Status           string `json:"status"`
	ConfigDirtyCount int    `json:"configDirtyCount"`
	CodeDirty        bool   `json:"codeDirty"`
	Publishing       bool   `json:"publishing"`
}

// PublishResult is POST /api/v1/publish.
type PublishResult struct {
	OK         bool `json:"ok"`
	Publishing bool `json:"publishing"`
}

// PublishPhase is one entry of a publish run's phases array (PUBLISH_PHASES order).
type PublishPhase struct {
	Key        string `json:"key"`
	Status     string `json:"status"` // pending|running|done|skipped|failed
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// PublishRun is one element of GET /api/v1/publish.
type PublishRun struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"` // pending|running|succeeded|failed|blocked|interrupted
	Phases     []PublishPhase `json:"phases"`
	Error      string         `json:"error"`
	BuildError string         `json:"buildError"`
	BuiltSha   string         `json:"builtSha"`
	StartedAt  string         `json:"startedAt"`
	FinishedAt string         `json:"finishedAt"`
	DurationMs *int           `json:"durationMs"`
}

type publishRunsResponse struct {
	Runs []PublishRun `json:"runs"`
}

// RestartResult is POST /api/v1/restart.
type RestartResult struct {
	OK     bool `json:"ok"`
	Rolled bool `json:"rolled"`
}

// ConfigRow is one element of GET /api/v1/config. Secret rows carry a null value.
type ConfigRow struct {
	Key            string  `json:"key"`
	Secret         bool    `json:"secret"`
	Value          *string `json:"value"`
	Published      bool    `json:"published"`
	Dirty          bool    `json:"dirty"`
	PendingRemoval bool    `json:"pendingRemoval"`
}

// StageResult is the {ok, staged} envelope from PUT/DELETE /api/v1/config.
type StageResult struct {
	OK     bool `json:"ok"`
	Staged bool `json:"staged"`
}

// ScheduleRow is one element of GET /api/v1/schedules (the app_schedules row).
type ScheduleRow struct {
	ID        string `json:"id"`
	AppID     string `json:"appId"`
	Name      string `json:"name"`
	Cadence   string `json:"cadence"`
	Target    string `json:"target"`
	Mechanism string `json:"mechanism"`
	Enabled   bool   `json:"enabled"`
	// Per-schedule run timeout in ms; nil = the platform default (15m). golem.json `timeoutMinutes`.
	TimeoutMs     *int    `json:"timeoutMs"`
	LastRunStatus *string `json:"lastRunStatus"`
	LastRunAt     *string `json:"lastRunAt"`
}

// ScheduleSyncResult is POST /api/v1/schedules ({ ok, declared, added, updated, removed }).
type ScheduleSyncResult struct {
	OK       bool `json:"ok"`
	Declared int  `json:"declared"`
	Added    int  `json:"added"`
	Updated  int  `json:"updated"`
	Removed  int  `json:"removed"`
}

// WebhookRow is one element of GET /api/v1/webhooks (an app_webhook_endpoints row plus the
// computed public URL). `id` is the unguessable URL token; `url` is HOOKS_BASE_URL/<id>.
type WebhookRow struct {
	ID         string `json:"id"`
	AppID      string `json:"appId"`
	TargetPath string `json:"targetPath"`
	Label      string `json:"label"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"createdAt"`
	URL        string `json:"url"`
}

// WebhookCreateResult is POST /api/v1/webhooks ({ ok, id, url }).
type WebhookCreateResult struct {
	OK  bool   `json:"ok"`
	ID  string `json:"id"`
	URL string `json:"url"`
}

// WebhookRemoveResult is DELETE /api/v1/webhooks ({ ok, removed }).
type WebhookRemoveResult struct {
	OK      bool `json:"ok"`
	Removed bool `json:"removed"`
}

// EnvEntry is one element of GET /api/v1/env — an effective dev value (dev
// override if set, else the prod non-secret value; never a prod secret).
type EnvEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EnvResult is GET /api/v1/env ({ env: [{key,value}] }).
type EnvResult struct {
	Env []EnvEntry `json:"env"`
}

// IntegrationsResult is GET /api/v1/integrations — the app's CONNECTION (native integration)
// dev values: a flat map of {ENV_PREFIX}_{KEY} -> synthetic glm_ placeholder for every enabled
// connection, plus the egress-proxy URL + CA cert. `golem dev pull` writes the credentials into
// .env.golem and wires the proxy/CA so those placeholders resolve through golem's integration
// proxy in dev exactly as they do in prod. Never real provider keys.
type IntegrationsResult struct {
	Credentials map[string]string  `json:"credentials"`
	Proxy       *IntegrationsProxy `json:"proxy"`
}

// IntegrationsProxy is the egress-proxy cred + CA cert (null unless the platform configured
// PUBLIC_EGRESS_PROXY_URL + INTEGRATION_PROXY_CA_CERT). HTTPSProxyURL embeds a per-app dev cred.
type IntegrationsProxy struct {
	HTTPSProxyURL string `json:"httpsProxyUrl"`
	CACertPEM     string `json:"caCertPem"`
}

// LogStreamResult is GET /api/v1/logs — the ok/empty/disabled/error discriminated
// union (src/lib/log-streams.ts). Rows are left as raw JSON so any stream's row
// shape renders without the CLI needing per-stream structs.
type LogStreamResult struct {
	Status  string            `json:"status"`  // ok | empty | disabled | error
	Rows    []json.RawMessage `json:"rows"`    // present when status == "ok"
	Hint    string            `json:"hint"`    // present when status == "disabled"
	Message string            `json:"message"` // present when status == "error"
}

// --- per-command calls --------------------------------------------------------

func (c *Client) Whoami(ctx context.Context) (*Whoami, error) {
	var w Whoami
	return &w, c.Do(ctx, "GET", "whoami", nil, &w)
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.Do(ctx, "GET", "status", nil, &s)
}

func (c *Client) Publish(ctx context.Context, force bool) (*PublishResult, error) {
	var r PublishResult
	return &r, c.Do(ctx, "POST", "publish", map[string]bool{"force": force}, &r)
}

// Runs returns the app's newest publish runs (GET /api/v1/publish?limit=N), newest-first.
func (c *Client) Runs(ctx context.Context, limit int) ([]PublishRun, error) {
	var resp publishRunsResponse
	return resp.Runs, c.Do(ctx, "GET", fmt.Sprintf("publish?limit=%d", limit), nil, &resp)
}

func (c *Client) Restart(ctx context.Context) (*RestartResult, error) {
	var r RestartResult
	return &r, c.Do(ctx, "POST", "restart", nil, &r)
}

func (c *Client) ConfigList(ctx context.Context) ([]ConfigRow, error) {
	var rows []ConfigRow
	return rows, c.Do(ctx, "GET", "config", nil, &rows)
}

// ConfigSet stages an env (secret=false) or secret (secret=true) entry.
func (c *Client) ConfigSet(ctx context.Context, key, value string, secret bool) (*StageResult, error) {
	var r StageResult
	body := map[string]any{"key": key, "value": value, "secret": secret}
	return &r, c.Do(ctx, "PUT", "config", body, &r)
}

// ConfigRemove stages a removal of key.
func (c *Client) ConfigRemove(ctx context.Context, key string) (*StageResult, error) {
	var r StageResult
	return &r, c.Do(ctx, "DELETE", "config?key="+Query(key), nil, &r)
}

// Env fetches the app's effective dev values (GET /api/v1/env).
func (c *Client) Env(ctx context.Context) (*EnvResult, error) {
	var r EnvResult
	return &r, c.Do(ctx, "GET", "env", nil, &r)
}

// Integrations fetches the app's connection dev creds + egress proxy (GET /api/v1/integrations).
func (c *Client) Integrations(ctx context.Context) (*IntegrationsResult, error) {
	var r IntegrationsResult
	return &r, c.Do(ctx, "GET", "integrations", nil, &r)
}

func (c *Client) SchedulesList(ctx context.Context) ([]ScheduleRow, error) {
	var rows []ScheduleRow
	return rows, c.Do(ctx, "GET", "schedules", nil, &rows)
}

func (c *Client) SchedulesSync(ctx context.Context) (*ScheduleSyncResult, error) {
	var r ScheduleSyncResult
	return &r, c.Do(ctx, "POST", "schedules", nil, &r)
}

// Logs fetches a snapshot of the given stream (console | errors | ci).
func (c *Client) Logs(ctx context.Context, stream string) (*LogStreamResult, error) {
	var r LogStreamResult
	return &r, c.Do(ctx, "GET", "logs?stream="+Query(stream), nil, &r)
}

// WebhooksList lists the app's inbound webhook endpoints (newest-first), each with its URL.
func (c *Client) WebhooksList(ctx context.Context) ([]WebhookRow, error) {
	var rows []WebhookRow
	return rows, c.Do(ctx, "GET", "webhooks", nil, &rows)
}

// WebhooksAdd creates an endpoint; the response carries the minted id + public URL.
func (c *Client) WebhooksAdd(ctx context.Context, label, targetPath string) (*WebhookCreateResult, error) {
	var r WebhookCreateResult
	body := map[string]any{"label": label, "targetPath": targetPath}
	return &r, c.Do(ctx, "POST", "webhooks", body, &r)
}

// WebhooksRemove deletes the endpoint with the given id (scoped to this app server-side).
func (c *Client) WebhooksRemove(ctx context.Context, id string) (*WebhookRemoveResult, error) {
	var r WebhookRemoveResult
	return &r, c.Do(ctx, "DELETE", "webhooks?id="+Query(id), nil, &r)
}
