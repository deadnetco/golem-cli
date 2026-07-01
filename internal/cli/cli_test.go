package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/deadnetco/golem-cli/internal/client"
)

// recorded is the request a command actually issued through the full env-driven
// client path.
type recorded struct {
	method string
	path   string
	query  string
	auth   string
	body   map[string]any
}

// runCmd points the CLI at a fake API (via GOLEM_API_URL), runs Run(args), and
// returns the captured request, the command's stdout, and its error.
func runCmd(t *testing.T, resp func(w http.ResponseWriter, r *http.Request), args ...string) (*recorded, string, error) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.auth = r.Header.Get("Authorization")
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		resp(w, r)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("GOLEM_API_KEY", "test-key")
	t.Setenv("GOLEM_API_URL", srv.URL)

	out := captureStdout(t, func() error { return Run(args, "v9.9.9") })
	return rec, out.text, out.err
}

type outResult struct {
	text string
	err  error
}

// captureStdout redirects os.Stdout for the duration of fn and returns what it
// printed plus fn's error.
func captureStdout(t *testing.T, fn func() error) outResult {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	b, _ := io.ReadAll(r)
	return outResult{text: string(b), err: runErr}
}

func jsonResp(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// devPullResp routes the two requests `golem dev pull` makes: GET /api/v1/env and
// GET /api/v1/integrations. Any other path is a test failure.
func devPullResp(t *testing.T, envBody, integrationsBody string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/env":
			_, _ = io.WriteString(w, envBody)
		case "/api/v1/integrations":
			_, _ = io.WriteString(w, integrationsBody)
		default:
			t.Errorf("dev pull hit unexpected path %s", r.URL.Path)
		}
	}
}

func TestRun_NoArgs(t *testing.T) {
	if err := Run(nil, "v1"); err == nil {
		t.Fatal("expected an error for no subcommand")
	}
}

func TestRun_Version(t *testing.T) {
	out := captureStdout(t, func() error { return Run([]string{"version"}, "v1.2.3") })
	if out.err != nil {
		t.Fatal(out.err)
	}
	if strings.TrimSpace(out.text) != "v1.2.3" {
		t.Errorf("version output = %q, want v1.2.3", out.text)
	}
}

func TestRun_Help(t *testing.T) {
	if err := Run([]string{"help"}, "v1"); err != nil {
		t.Fatalf("help should not error: %v", err)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	if err := Run([]string{"frobnicate"}, "v1"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestMissingAPIKeyGuard(t *testing.T) {
	// No GOLEM_API_URL is set + no key → the command must fail BEFORE any call.
	t.Setenv("GOLEM_API_KEY", "")
	err := Run([]string{"whoami"}, "v1")
	if err == nil {
		t.Fatal("expected an error when GOLEM_API_KEY is unset")
	}
	if !errors.Is(err, client.ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if !strings.Contains(err.Error(), "GOLEM_API_KEY") {
		t.Errorf("err = %q, want a friendly GOLEM_API_KEY message", err.Error())
	}
}

func TestWhoamiCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `{"slug":"alltest","owner":"a@b.co","status":"active","key":{"name":"ci","scopes":["all"]}}`),
		"whoami")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "GET" || rec.path != "/api/v1/whoami" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if rec.auth != "Bearer test-key" {
		t.Errorf("auth = %q", rec.auth)
	}
	for _, want := range []string{"alltest", "a@b.co", "active", "ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestStatusCommand(t *testing.T) {
	_, out, err := runCmd(t,
		jsonResp(200, `{"status":"active","configDirtyCount":2,"codeDirty":true,"publishing":false}`),
		"status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "active") || !strings.Contains(out, "publish") {
		t.Errorf("status output unexpected: %q", out)
	}
}

func TestPublishCommand_Force(t *testing.T) {
	// --no-wait keeps the assertion on the POST request shape without entering the
	// follow loop (which a single static handler can't terminate).
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"publishing":true}`),
		"publish", "--force", "--no-wait")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "POST" || rec.path != "/api/v1/publish" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if rec.body["force"] != true {
		t.Errorf("force = %v, want true", rec.body["force"])
	}
	if !strings.Contains(out, "publishing") {
		t.Errorf("output = %q", out)
	}
}

func TestPublishCommand_DefaultNotForced(t *testing.T) {
	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"publishing":true}`), "publish", "--no-wait")
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["force"] != false {
		t.Errorf("force = %v, want false", rec.body["force"])
	}
}

// TestPublishFollow_FailedPrintsBuildError scripts POST publish → {publishing:true},
// then GET publish?limit=1 → a terminal failed run carrying a buildError tail. The
// default (follow) path must poll the run, print the error + build-output tail, and
// return a non-nil error so the process exits non-zero.
func TestPublishFollow_FailedPrintsBuildError(t *testing.T) {
	t.Setenv("GOLEM_PUBLISH_POLL_MS", "1") // fast poll for the test
	_, out, err := runCmd(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"ok":true,"publishing":true}`)
			return
		}
		// GET /api/v1/publish?limit=1 → terminal failed run
		_, _ = io.WriteString(w, `{"runs":[{"id":"r1","status":"failed","error":"build failed (exit 1)","buildError":"webhook.ts:168:6\nERROR: Cannot use \"||\" with \"??\"","phases":[{"key":"building","status":"failed"}]}]}`)
	}, "publish")
	if err == nil {
		t.Fatal("expected a non-nil error on a failed publish")
	}
	if !strings.Contains(out, "build failed (exit 1)") || !strings.Contains(out, "Cannot use") {
		t.Fatalf("expected build error in output, got:\n%s", out)
	}
}

// TestPublishNoWait proves --no-wait short-circuits before the follow loop: it
// prints the "publishing" message and exits 0 without polling the run.
func TestPublishNoWait(t *testing.T) {
	_, out, err := runCmd(t, jsonResp(200, `{"ok":true,"publishing":true}`), "publish", "--no-wait")
	if err != nil {
		t.Fatalf("no-wait should not error: %v", err)
	}
	if !strings.Contains(out, "publishing") {
		t.Fatalf("expected publishing message, got: %s", out)
	}
}

func TestRestartCommand(t *testing.T) {
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"rolled":true}`), "restart")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "POST" || rec.path != "/api/v1/restart" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(out, "restarted") {
		t.Errorf("output = %q", out)
	}
}

func TestConfigListCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `[{"key":"FOO","secret":false,"value":"bar","published":true,"dirty":false,"pendingRemoval":false}]`),
		"config", "list")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "GET" || rec.path != "/api/v1/config" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("output = %q", out)
	}
}

func TestConfigGetCommand_FiltersClientSide(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `[{"key":"FOO","secret":false,"value":"bar","published":true,"dirty":false,"pendingRemoval":false},
		                {"key":"BAZ","secret":false,"value":"qux","published":true,"dirty":false,"pendingRemoval":false}]`),
		"config", "get", "BAZ")
	if err != nil {
		t.Fatal(err)
	}
	// Filter is client-side: still just a GET on /config.
	if rec.path != "/api/v1/config" {
		t.Errorf("path = %q", rec.path)
	}
	if !strings.Contains(out, "BAZ=qux") || strings.Contains(out, "FOO") {
		t.Errorf("output = %q, want only BAZ", out)
	}
}

func TestConfigGetCommand_NotFound(t *testing.T) {
	_, _, err := runCmd(t,
		jsonResp(200, `[{"key":"FOO","secret":false,"value":"bar","published":true,"dirty":false,"pendingRemoval":false}]`),
		"config", "get", "NOPE")
	if err == nil {
		t.Fatal("expected an error for a missing key")
	}
}

func TestConfigSetCommand(t *testing.T) {
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"config", "set", "FOO=bar")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "PUT" || rec.path != "/api/v1/config" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if rec.body["key"] != "FOO" || rec.body["value"] != "bar" || rec.body["secret"] != false {
		t.Errorf("body = %+v", rec.body)
	}
	if !strings.Contains(out, "staged") || !strings.Contains(out, "publish") {
		t.Errorf("output = %q", out)
	}
}

func TestEnvSetCommand_AliasOfConfigSet(t *testing.T) {
	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"env", "set", "API_BASE=https://x.test")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "PUT" || rec.body["secret"] != false {
		t.Errorf("env set should PUT secret=false: %+v", rec)
	}
	if rec.body["value"] != "https://x.test" {
		t.Errorf("value = %v", rec.body["value"])
	}
}

func TestSecretSetCommand_InlineValue(t *testing.T) {
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"secret", "set", "TOK=shh")
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["secret"] != true {
		t.Errorf("secret = %v, want true", rec.body["secret"])
	}
	if rec.body["value"] != "shh" {
		t.Errorf("value = %v", rec.body["value"])
	}
	if !strings.Contains(out, "secret") {
		t.Errorf("output = %q", out)
	}
}

func TestSecretSetCommand_FromStdin(t *testing.T) {
	// Value omitted on argv → read from stdin (never require it on the command line).
	orig := os.Stdin
	r, w, _ := os.Pipe()
	_, _ = io.WriteString(w, "super-secret\n")
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"secret", "set", "TOK")
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["secret"] != true {
		t.Errorf("secret = %v, want true", rec.body["secret"])
	}
	if rec.body["value"] != "super-secret" {
		t.Errorf("stdin value = %v, want 'super-secret' (trailing newline trimmed)", rec.body["value"])
	}
}

// TestSecretSet_MissingKeyGuardFiresBeforeStdin proves the missing-key guard runs
// BEFORE stdin is consumed: with no GOLEM_API_KEY, `golem secret set TOK` (value
// omitted) must return ErrNoAPIKey immediately and NOT block reading from stdin.
// os.Stdin is pointed at a pipe whose write end is held open and never written —
// a regression that reads stdin first would block on io.ReadAll, which we catch as
// a timeout instead of hanging the suite.
func TestSecretSet_MissingKeyGuardFiresBeforeStdin(t *testing.T) {
	t.Setenv("GOLEM_API_KEY", "")

	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = w.Close() // release any blocked reader on the (failure) path
		_ = r.Close()
	})

	done := make(chan error, 1)
	go func() { done <- Run([]string{"secret", "set", "TOK"}, "v1") }()

	select {
	case runErr := <-done:
		if !errors.Is(runErr, client.ErrNoAPIKey) {
			t.Fatalf("err = %v, want ErrNoAPIKey (fired before stdin)", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("secret set blocked on stdin instead of failing fast on the missing key")
	}
}

func TestSecretRmCommand(t *testing.T) {
	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"secret", "rm", "TOK")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "DELETE" || rec.path != "/api/v1/config" || rec.query != "key=TOK" {
		t.Errorf("got %s %s?%s", rec.method, rec.path, rec.query)
	}
}

func TestConfigRmCommand(t *testing.T) {
	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"staged":true}`),
		"config", "rm", "FOO")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "DELETE" || rec.query != "key=FOO" {
		t.Errorf("got %s %s?%s", rec.method, rec.path, rec.query)
	}
}

func TestLogsCommand_DefaultStream(t *testing.T) {
	rec, out, err := runCmd(t, jsonResp(200, `{"status":"ok","rows":["hello from console"]}`), "logs")
	if err != nil {
		t.Fatal(err)
	}
	if rec.path != "/api/v1/logs" || rec.query != "stream=console" {
		t.Errorf("got path=%q query=%q", rec.path, rec.query)
	}
	if !strings.Contains(out, "hello from console") {
		t.Errorf("output = %q", out)
	}
}

func TestLogsCommand_ErrorsStream(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `{"status":"disabled","hint":"enable Sentry event:read"}`),
		"logs", "--stream", "errors")
	if err != nil {
		t.Fatal(err)
	}
	if rec.query != "stream=errors" {
		t.Errorf("query = %q", rec.query)
	}
	if !strings.Contains(out, "disabled") || !strings.Contains(out, "Sentry") {
		t.Errorf("output = %q", out)
	}
}

func TestLogsCommand_UnknownStream(t *testing.T) {
	// An unknown stream is rejected client-side before any call.
	called := false
	_, _, err := runCmd(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}, "logs", "--stream", "bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown stream")
	}
	if called {
		t.Error("should not have made a call for an unknown stream")
	}
}

func TestSchedulesListCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `[{"id":"1","appId":"a","name":"nightly","cadence":"daily","target":"job.js","mechanism":"scheduler","enabled":true,"lastRunStatus":"succeeded","lastRunAt":null}]`),
		"schedules", "list")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "GET" || rec.path != "/api/v1/schedules" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(out, "nightly") {
		t.Errorf("output = %q", out)
	}
}

func TestSchedulesListCommand_Timeout(t *testing.T) {
	// A per-schedule override (timeoutMs 2_400_000 = 40m) renders as "timeout: 40m"; a row with a
	// nil override shows no timeout segment (= the 15m platform default).
	_, out, err := runCmd(t,
		jsonResp(200, `[
			{"id":"1","appId":"a","name":"long","cadence":"weekly","target":"export.js","mechanism":"scheduler","enabled":true,"timeoutMs":2400000,"lastRunStatus":null,"lastRunAt":null},
			{"id":"2","appId":"a","name":"quick","cadence":"daily","target":"job.js","mechanism":"scheduler","enabled":true,"timeoutMs":null,"lastRunStatus":null,"lastRunAt":null}
		]`),
		"schedules", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "timeout: 40m") {
		t.Errorf("expected the override timeout in output = %q", out)
	}
	if strings.Count(out, "timeout:") != 1 {
		t.Errorf("only the overridden row should show a timeout = %q", out)
	}
}

func TestSchedulesSyncCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `{"ok":true,"declared":3,"added":1,"updated":1,"removed":0}`),
		"schedules", "sync")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "POST" || rec.path != "/api/v1/schedules" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if !strings.Contains(out, "3 declared") {
		t.Errorf("output = %q", out)
	}
}

func TestWebhooksListCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `[{"id":"tok1","appId":"a","targetPath":"/webhooks/stripe","label":"Stripe","enabled":true,"createdAt":"2026-06-24T00:00:00Z","url":"https://hooks.deadnet.co/tok1"}]`),
		"webhooks", "list")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "GET" || rec.path != "/api/v1/webhooks" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	// surfaces the label, the public URL, AND the bare id (needed for `webhooks rm`)
	if !strings.Contains(out, "Stripe") || !strings.Contains(out, "https://hooks.deadnet.co/tok1") || !strings.Contains(out, "id:  tok1") {
		t.Errorf("output = %q", out)
	}
}

func TestWebhooksListCommand_Empty(t *testing.T) {
	_, out, err := runCmd(t, jsonResp(200, `[]`), "webhooks", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no webhook endpoints") {
		t.Errorf("output = %q, want the empty-state line", out)
	}
}

func TestWebhooksAddCommand(t *testing.T) {
	rec, out, err := runCmd(t,
		jsonResp(200, `{"ok":true,"id":"tok2","url":"https://hooks.deadnet.co/tok2"}`),
		"webhooks", "add", "Stripe", "/webhooks/stripe")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "POST" || rec.path != "/api/v1/webhooks" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if rec.body["label"] != "Stripe" || rec.body["targetPath"] != "/webhooks/stripe" {
		t.Errorf("body = %+v", rec.body)
	}
	if !strings.Contains(out, "https://hooks.deadnet.co/tok2") {
		t.Errorf("output = %q, want the created URL", out)
	}
}

func TestWebhooksAddCommand_ArgCount(t *testing.T) {
	// add requires exactly LABEL + PATH; a missing path errors before any call.
	called := false
	_, _, err := runCmd(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}, "webhooks", "add", "OnlyLabel")
	if err == nil {
		t.Fatal("expected an error for missing TARGET_PATH")
	}
	if called {
		t.Error("should not have made a call with bad arg count")
	}
}

func TestWebhooksRemoveCommand(t *testing.T) {
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"removed":true}`),
		"webhooks", "rm", "tok9")
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != "DELETE" || rec.path != "/api/v1/webhooks" || rec.query != "id=tok9" {
		t.Errorf("got %s %s?%s", rec.method, rec.path, rec.query)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output = %q", out)
	}
}

func TestWebhooksRemoveCommand_ArgCount(t *testing.T) {
	// rm requires exactly one id; with none it errors before any call.
	called := false
	_, _, err := runCmd(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}, "webhooks", "rm")
	if err == nil {
		t.Fatal("expected an error for missing ID")
	}
	if called {
		t.Error("should not have made a call with no id")
	}
}

func TestWebhooksCommand_NoSub(t *testing.T) {
	_, _, err := runCmd(t, jsonResp(200, `{}`), "webhooks")
	if err == nil {
		t.Fatal("expected a usage error when no webhooks subcommand is given")
	}
}

func TestOpenCommand_PrintsURL(t *testing.T) {
	t.Setenv("GOLEM_NO_BROWSER", "1") // don't actually spawn a browser in CI/dev
	rec, out, err := runCmd(t,
		jsonResp(200, `{"slug":"alltest","owner":"a@b.co","status":"active","key":{"name":"ci","scopes":[]}}`),
		"open")
	if err != nil {
		t.Fatal(err)
	}
	// open resolves the slug via whoami.
	if rec.path != "/api/v1/whoami" {
		t.Errorf("path = %q, want whoami", rec.path)
	}
	if !strings.Contains(out, "https://alltest.tools.deadnet.co") {
		t.Errorf("output = %q, want the public URL", out)
	}
}

func TestDevPullCommand(t *testing.T) {
	// .env.golem must land in a throwaway dir, not the repo tree.
	t.Chdir(t.TempDir())

	rec, _, err := runCmd(t,
		devPullResp(t,
			`{"env":[{"key":"FOO","value":"bar"},{"key":"SLACK_WEBHOOK_URL","value":"dev-hook"}]}`,
			`{"credentials":{},"proxy":null}`),
		"dev", "pull")
	if err != nil {
		t.Fatal(err)
	}
	// Two requests are made (env + integrations); rec captures the last. Auth binds both.
	if rec.auth != "Bearer test-key" {
		t.Errorf("auth = %q, want Bearer test-key", rec.auth)
	}
	data, err := os.ReadFile(".env.golem")
	if err != nil {
		t.Fatalf("read .env.golem: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "FOO='bar'\n") {
		t.Errorf(".env.golem = %q, missing FOO='bar'", got)
	}
	if !strings.Contains(got, "SLACK_WEBHOOK_URL='dev-hook'\n") {
		t.Errorf(".env.golem = %q, missing SLACK_WEBHOOK_URL='dev-hook'", got)
	}
	// No connections → no CA file written and no proxy lines.
	if _, statErr := os.Stat(".golem-proxy-ca.pem"); !os.IsNotExist(statErr) {
		t.Error("CA file should not be written when proxy is null")
	}
	if strings.Contains(got, "HTTPS_PROXY=") {
		t.Errorf(".env.golem = %q, unexpected proxy line with null proxy", got)
	}
	// 0600: dev secrets on disk must not be world-readable.
	info, err := os.Stat(".env.golem")
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".env.golem perm = %o, want 600", perm)
	}
}

// TestDevPull_ConnectionEnvsAndProxy is the regression guard for the bug this fixes: `golem dev
// pull` must fold the CONNECTION (native-integration) dev creds from /api/v1/integrations into
// .env.golem, and wire the egress proxy + CA so the glm_ placeholders resolve in dev.
func TestDevPull_ConnectionEnvsAndProxy(t *testing.T) {
	t.Chdir(t.TempDir())

	const caPEM = "-----BEGIN CERTIFICATE-----\nMIIFAKEpem\n-----END CERTIFICATE-----\n"
	integBody, err := json.Marshal(map[string]any{
		"credentials": map[string]string{"SLACK_BOT_TOKEN": "glm_abc", "SLACK_TEAM_ID": "glm_def"},
		"proxy": map[string]string{
			"httpsProxyUrl": "https://myapp:sekret@egress.golem.dev",
			"caCertPem":     caPEM,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = runCmd(t,
		devPullResp(t, `{"env":[{"key":"FOO","value":"bar"}]}`, string(integBody)),
		"dev", "pull")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(".env.golem")
	if err != nil {
		t.Fatalf("read .env.golem: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"FOO='bar'\n",                 // plain env still there
		"SLACK_BOT_TOKEN='glm_abc'\n", // connection cred
		"SLACK_TEAM_ID='glm_def'\n",   // connection cred
		"HTTPS_PROXY='https://myapp:sekret@egress.golem.dev'\n", // egress proxy (uppercase)
		"https_proxy='https://myapp:sekret@egress.golem.dev'\n", // + lowercase for broad coverage
		"NODE_EXTRA_CA_CERTS='",                                 // Node CA (append-safe), abs path
	} {
		if !strings.Contains(got, want) {
			t.Errorf(".env.golem missing %q\n--- file ---\n%s", want, got)
		}
	}

	// The CA cert is written verbatim to the file NODE_EXTRA_CA_CERTS points at.
	ca, err := os.ReadFile(".golem-proxy-ca.pem")
	if err != nil {
		t.Fatalf("read CA file: %v", err)
	}
	if string(ca) != caPEM {
		t.Errorf("CA file = %q, want %q", string(ca), caPEM)
	}
}

func TestDevPull_MissingKeyFailsFast(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GOLEM_API_KEY", "")

	err := Run([]string{"dev", "pull"}, "v1")
	if err == nil {
		t.Fatal("expected an error when GOLEM_API_KEY is unset")
	}
	if !errors.Is(err, client.ErrNoAPIKey) {
		t.Fatalf("err = %v, want ErrNoAPIKey", err)
	}
	if _, statErr := os.Stat(".env.golem"); !os.IsNotExist(statErr) {
		t.Error(".env.golem should not be written when the key is missing")
	}
}

// TestDevPull_EscapingRoundTrip arms the API to return a value containing an
// apostrophe, a `$`, and a newline, then asserts the written .env.golem
// round-trips: sourcing it under `set -a; . .env.golem; set +a` yields exactly
// the original value (single-quoted, with embedded single-quotes escaped as
// '\”). This proves the file can't corrupt or shell-inject when sourced.
func TestDevPull_EscapingRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX sh available to verify the round-trip")
	}
	t.Chdir(t.TempDir())

	const raw = "it's $HOME\nline2" // apostrophe + dollar + newline
	body, err := json.Marshal(map[string]any{
		"env": []map[string]string{{"key": "TRICKY", "value": raw}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runCmd(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}, "dev", "pull")
	if err != nil {
		t.Fatal(err)
	}

	// Source the file the way start-app.sh does, then echo the value back out so
	// we compare the SOURCED result to the original input — not just the file text.
	out, err := exec.Command("sh", "-c",
		`set -a; . ./.env.golem; set +a; printf '%s' "$TRICKY"`).Output()
	if err != nil {
		t.Fatalf("sourcing .env.golem failed: %v", err)
	}
	if string(out) != raw {
		t.Errorf("sourced TRICKY = %q, want %q (escaping round-trip failed)", string(out), raw)
	}
}

func TestErrorEnvelopeSurfacesToCommand(t *testing.T) {
	_, _, err := runCmd(t,
		jsonResp(409, `{"error":"cannot publish a torn-down app"}`),
		"publish")
	if err == nil {
		t.Fatal("expected the {error} envelope to surface as a command error")
	}
	if !strings.Contains(err.Error(), "torn-down") {
		t.Errorf("err = %q", err.Error())
	}
}
