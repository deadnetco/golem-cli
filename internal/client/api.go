package client

import (
	"context"
	"encoding/json"
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
	ID            string  `json:"id"`
	AppID         string  `json:"appId"`
	Name          string  `json:"name"`
	Cadence       string  `json:"cadence"`
	Target        string  `json:"target"`
	Mechanism     string  `json:"mechanism"`
	Enabled       bool    `json:"enabled"`
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
