package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capture records what a request actually looked like, so each command test can
// assert the method, path, query, Bearer header, and decoded JSON body.
type capture struct {
	method string
	path   string
	query  string
	auth   string
	body   map[string]any
}

// newTestClient spins a fake API server, points a Client at it via GOLEM_API_URL,
// and returns the client plus a pointer the handler fills with the last request.
// handler writes the canned response.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.query = r.URL.RawQuery
		cap.auth = r.Header.Get("Authorization")
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &cap.body)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("GOLEM_API_KEY", "test-key")
	t.Setenv("GOLEM_API_URL", srv.URL)
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, cap
}

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestNew_MissingAPIKey(t *testing.T) {
	t.Setenv("GOLEM_API_KEY", "")
	if _, err := New(); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("New() err = %v, want ErrNoAPIKey", err)
	}
}

func TestNew_DefaultBaseURL(t *testing.T) {
	t.Setenv("GOLEM_API_KEY", "k")
	t.Setenv("GOLEM_API_URL", "")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want %q", c.BaseURL(), DefaultBaseURL)
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("GOLEM_API_KEY", "k")
	t.Setenv("GOLEM_API_URL", "https://example.test/")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != "https://example.test" {
		t.Fatalf("BaseURL = %q, want trailing slash trimmed", c.BaseURL())
	}
}

func TestDo_SendsBearerAndPath(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{}`))
	if err := c.Do(context.Background(), "GET", "whoami", nil, nil); err != nil {
		t.Fatal(err)
	}
	if cap.method != "GET" {
		t.Errorf("method = %q, want GET", cap.method)
	}
	if cap.path != "/api/v1/whoami" {
		t.Errorf("path = %q, want /api/v1/whoami", cap.path)
	}
	if cap.auth != "Bearer test-key" {
		t.Errorf("auth = %q, want Bearer test-key", cap.auth)
	}
}

func TestDo_ErrorEnvelope(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(409, `{"error":"cannot publish a torn-down app"}`))
	err := c.Do(context.Background(), "POST", "publish", map[string]bool{"force": false}, nil)
	if err == nil {
		t.Fatal("expected error on non-2xx")
	}
	if !strings.Contains(err.Error(), "cannot publish a torn-down app") {
		t.Errorf("err = %q, want the decoded {error} message", err.Error())
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("err = %q, want the status code included", err.Error())
	}
}

func TestDo_NonJSONErrorBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "bad gateway")
	})
	err := c.Do(context.Background(), "GET", "status", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want a 502 message", err)
	}
}

func TestDo_NetworkError(t *testing.T) {
	t.Setenv("GOLEM_API_KEY", "k")
	t.Setenv("GOLEM_API_URL", "http://127.0.0.1:1") // nothing listening
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), "GET", "whoami", nil, nil); err == nil {
		t.Fatal("expected a transport error")
	}
}

func TestWhoami(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200,
		`{"slug":"alltest","owner":"a@b.co","status":"active","key":{"name":"ci","scopes":["all"]}}`))
	w, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.path != "/api/v1/whoami" || cap.method != "GET" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if w.Slug != "alltest" || w.Owner != "a@b.co" || w.Status != "active" || w.Key.Name != "ci" {
		t.Errorf("decoded = %+v", w)
	}
}

func TestStatus(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200,
		`{"status":"active","configDirtyCount":2,"codeDirty":true,"publishing":false}`))
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigDirtyCount != 2 || !s.CodeDirty || s.Publishing {
		t.Errorf("decoded = %+v", s)
	}
}

func TestPublish_BodyAndForce(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"publishing":true}`))
	r, err := c.Publish(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != "POST" || cap.path != "/api/v1/publish" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.body["force"] != true {
		t.Errorf("body force = %v, want true", cap.body["force"])
	}
	if !r.Publishing {
		t.Error("want publishing true")
	}
}

func TestRuns(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"runs":[
		{"id":"r1","status":"failed","error":"build failed (exit 1)","buildError":"webhook.ts:168 ERROR","phases":[{"key":"building","status":"failed"}],"builtSha":"","startedAt":"2026-06-29T09:45:00Z","finishedAt":"2026-06-29T09:48:00Z","durationMs":180000}
	]}`))
	runs, err := c.Runs(context.Background(), 1)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if cap.method != "GET" || cap.path != "/api/v1/publish" || cap.query != "limit=1" {
		t.Fatalf("unexpected request: %s %s?%s", cap.method, cap.path, cap.query)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].BuildError != "webhook.ts:168 ERROR" {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	if len(runs[0].Phases) != 1 || runs[0].Phases[0].Key != "building" {
		t.Fatalf("unexpected phases: %+v", runs[0].Phases)
	}
}

func TestRestart(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"rolled":true}`))
	r, err := c.Restart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != "POST" || cap.path != "/api/v1/restart" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if !r.Rolled {
		t.Error("want rolled true")
	}
}

func TestConfigList(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200,
		`[{"key":"FOO","secret":false,"value":"bar","published":true,"dirty":false,"pendingRemoval":false},
		  {"key":"TOK","secret":true,"value":null,"published":true,"dirty":false,"pendingRemoval":false}]`))
	rows, err := c.ConfigList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
	if rows[0].Value == nil || *rows[0].Value != "bar" {
		t.Errorf("row0 value = %v", rows[0].Value)
	}
	if !rows[1].Secret || rows[1].Value != nil {
		t.Errorf("secret row should have nil value: %+v", rows[1])
	}
}

func TestConfigSet_Body(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"staged":true}`))
	if _, err := c.ConfigSet(context.Background(), "FOO", "bar", false); err != nil {
		t.Fatal(err)
	}
	if cap.method != "PUT" || cap.path != "/api/v1/config" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.body["key"] != "FOO" || cap.body["value"] != "bar" || cap.body["secret"] != false {
		t.Errorf("body = %+v", cap.body)
	}
}

func TestConfigSet_SecretFlag(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"staged":true}`))
	if _, err := c.ConfigSet(context.Background(), "TOK", "shh", true); err != nil {
		t.Fatal(err)
	}
	if cap.body["secret"] != true {
		t.Errorf("secret = %v, want true", cap.body["secret"])
	}
}

func TestConfigRemove_QueryParam(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"staged":true}`))
	if _, err := c.ConfigRemove(context.Background(), "FOO BAR"); err != nil {
		t.Fatal(err)
	}
	if cap.method != "DELETE" || cap.path != "/api/v1/config" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.query != "key=FOO+BAR" {
		t.Errorf("query = %q, want key=FOO+BAR (escaped)", cap.query)
	}
}

func TestSchedulesList(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200,
		`[{"id":"1","appId":"a","name":"nightly","cadence":"daily","target":"job.js","mechanism":"scheduler","enabled":true,"timeoutMs":2400000,"lastRunStatus":"succeeded","lastRunAt":null}]`))
	rows, err := c.SchedulesList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "nightly" || !rows[0].Enabled {
		t.Errorf("rows = %+v", rows)
	}
	if rows[0].TimeoutMs == nil || *rows[0].TimeoutMs != 2400000 {
		t.Errorf("TimeoutMs = %v, want 2400000", rows[0].TimeoutMs)
	}
}

func TestSchedulesSync(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200,
		`{"ok":true,"declared":3,"added":1,"updated":1,"removed":0}`))
	r, err := c.SchedulesSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != "POST" || cap.path != "/api/v1/schedules" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if r.Declared != 3 || r.Added != 1 || r.Updated != 1 {
		t.Errorf("result = %+v", r)
	}
}

func TestLogs_StreamQueryAndOK(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200,
		`{"status":"ok","rows":["line one","line two"]}`))
	r, err := c.Logs(context.Background(), "console")
	if err != nil {
		t.Fatal(err)
	}
	if cap.path != "/api/v1/logs" || cap.query != "stream=console" {
		t.Errorf("got path=%q query=%q", cap.path, cap.query)
	}
	if r.Status != "ok" || len(r.Rows) != 2 {
		t.Errorf("result = %+v", r)
	}
}

func TestLogs_Disabled(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(200,
		`{"status":"disabled","hint":"enable Sentry event:read"}`))
	r, err := c.Logs(context.Background(), "errors")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "disabled" || r.Hint == "" {
		t.Errorf("result = %+v", r)
	}
}

func TestWebhooksList(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200,
		`[{"id":"tok1","appId":"a","targetPath":"/webhooks/stripe","label":"Stripe","enabled":true,"createdAt":"2026-06-24T00:00:00Z","url":"https://hooks.deadnet.co/tok1"}]`))
	rows, err := c.WebhooksList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != "GET" || cap.path != "/api/v1/webhooks" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if len(rows) != 1 || rows[0].Label != "Stripe" || rows[0].URL != "https://hooks.deadnet.co/tok1" || !rows[0].Enabled {
		t.Errorf("rows = %+v", rows)
	}
}

func TestWebhooksAdd(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200,
		`{"ok":true,"id":"tok2","url":"https://hooks.deadnet.co/tok2"}`))
	r, err := c.WebhooksAdd(context.Background(), "Stripe", "/webhooks/stripe")
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != "POST" || cap.path != "/api/v1/webhooks" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.body["label"] != "Stripe" || cap.body["targetPath"] != "/webhooks/stripe" {
		t.Errorf("body = %+v", cap.body)
	}
	if !r.OK || r.ID != "tok2" || r.URL != "https://hooks.deadnet.co/tok2" {
		t.Errorf("result = %+v", r)
	}
}

func TestWebhooksRemove_QueryParam(t *testing.T) {
	c, cap := newTestClient(t, jsonHandler(200, `{"ok":true,"removed":true}`))
	if _, err := c.WebhooksRemove(context.Background(), "tok 3"); err != nil {
		t.Fatal(err)
	}
	if cap.method != "DELETE" || cap.path != "/api/v1/webhooks" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.query != "id=tok+3" {
		t.Errorf("query = %q, want id=tok+3 (escaped)", cap.query)
	}
}
