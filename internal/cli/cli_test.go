package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	rec, out, err := runCmd(t, jsonResp(200, `{"ok":true,"publishing":true}`),
		"publish", "--force")
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
	rec, _, err := runCmd(t, jsonResp(200, `{"ok":true,"publishing":true}`), "publish")
	if err != nil {
		t.Fatal(err)
	}
	if rec.body["force"] != false {
		t.Errorf("force = %v, want false", rec.body["force"])
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
