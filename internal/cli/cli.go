// Package cli implements golem's subcommand dispatch and rendering. It is kept
// separate from main so the command handlers are directly unit-testable against
// an httptest server (point GOLEM_API_URL at the fake, assert method/path/body).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/deadnetco/golem-cli/internal/client"
)

// followInterval is how often --follow re-fetches a log snapshot.
const followInterval = 3 * time.Second

// Run dispatches a single golem invocation. args is os.Args[1:]; version is the
// build-stamped version string. A returned error is printed to stderr by main
// and causes a non-zero exit.
func Run(args []string, version string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a subcommand is required (run `golem help`)")
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		usage()
		return nil
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "whoami":
		return cmdWhoami(rest)
	case "status":
		return cmdStatus(rest)
	case "publish":
		return cmdPublish(rest)
	case "restart":
		return cmdRestart(rest)
	case "config":
		return cmdConfig(rest)
	case "env":
		return cmdEnv(rest)
	case "secret":
		return cmdSecret(rest)
	case "logs":
		return cmdLogs(rest)
	case "schedules":
		return cmdSchedules(rest)
	case "webhooks":
		return cmdWebhooks(rest)
	case "open":
		return cmdOpen(rest)
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func ctx() context.Context { return context.Background() }

// --- commands -----------------------------------------------------------------

func cmdWhoami(args []string) error {
	if err := noFlags("whoami", args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	w, err := c.Whoami(ctx())
	if err != nil {
		return err
	}
	fmt.Printf("slug:    %s\n", w.Slug)
	fmt.Printf("owner:   %s\n", w.Owner)
	fmt.Printf("status:  %s\n", w.Status)
	fmt.Printf("key:     %s\n", w.Key.Name)
	if len(w.Key.Scopes) > 0 {
		fmt.Printf("scopes:  %s\n", strings.Join(w.Key.Scopes, ", "))
	}
	return nil
}

func cmdStatus(args []string) error {
	if err := noFlags("status", args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	s, err := c.Status(ctx())
	if err != nil {
		return err
	}
	fmt.Printf("app status:       %s\n", s.Status)
	fmt.Printf("config dirty:     %d staged change(s)\n", s.ConfigDirtyCount)
	fmt.Printf("code dirty:       %s\n", yesNo(s.CodeDirty))
	fmt.Printf("publishing:       %s\n", yesNo(s.Publishing))
	if !s.Publishing && (s.ConfigDirtyCount > 0 || s.CodeDirty) {
		fmt.Println("\nThere are unpublished changes — run `golem publish` to apply them.")
	}
	return nil
}

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	force := fs.Bool("force", false, "rebuild even if the repo HEAD matches the last built image")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	r, err := c.Publish(ctx(), *force)
	if err != nil {
		return err
	}
	if r.Publishing {
		fmt.Println("publishing… (this can take several minutes — run `golem status` to check progress)")
	} else {
		fmt.Println("publish requested.")
	}
	return nil
}

func cmdRestart(args []string) error {
	if err := noFlags("restart", args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	r, err := c.Restart(ctx())
	if err != nil {
		return err
	}
	if r.Rolled {
		fmt.Println("restarted — the machine was rolled onto its current image.")
	} else {
		fmt.Println("no live machine to roll (nothing to restart).")
	}
	return nil
}

func cmdConfig(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: golem config <list|get|set|rm> [...]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return configList(rest)
	case "get":
		return configGet(rest)
	case "set":
		return configSet(rest, false)
	case "rm":
		return configRemove(rest)
	default:
		return fmt.Errorf("unknown config subcommand %q (want list|get|set|rm)", sub)
	}
}

func cmdEnv(args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: golem env set KEY=VALUE")
	}
	return configSet(args[1:], false)
}

func cmdSecret(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: golem secret <set|rm> [...]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "set":
		return secretSet(rest)
	case "rm":
		return configRemove(rest)
	default:
		return fmt.Errorf("unknown secret subcommand %q (want set|rm)", sub)
	}
}

func configList(args []string) error {
	if err := noFlags("config list", args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	rows, err := c.ConfigList(ctx())
	if err != nil {
		return err
	}
	printConfigRows(rows)
	return nil
}

func configGet(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: golem config get KEY")
	}
	key := args[0]
	c, err := client.New()
	if err != nil {
		return err
	}
	rows, err := c.ConfigList(ctx())
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Key == key {
			if r.Secret {
				fmt.Printf("%s=(secret — value not returned)\n", r.Key)
			} else if r.Value != nil {
				fmt.Printf("%s=%s\n", r.Key, *r.Value)
			} else {
				fmt.Printf("%s=\n", r.Key)
			}
			return nil
		}
	}
	return fmt.Errorf("no config entry %q", key)
}

func configSet(args []string, secret bool) error {
	if len(args) != 1 {
		return errors.New("usage: golem config set KEY=VALUE")
	}
	key, value, ok := strings.Cut(args[0], "=")
	if !ok || key == "" {
		return errors.New("expected KEY=VALUE")
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	if _, err := c.ConfigSet(ctx(), key, value, secret); err != nil {
		return err
	}
	fmt.Printf("%s staged — run `golem publish` to apply.\n", key)
	return nil
}

// secretSet handles `golem secret set KEY[=VALUE]`: when VALUE is omitted the
// secret is read from stdin so it never appears on argv (or in shell history).
func secretSet(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: golem secret set KEY[=VALUE]  (value read from stdin if omitted)")
	}
	key, value, hasValue := strings.Cut(args[0], "=")
	if key == "" {
		return errors.New("expected KEY or KEY=VALUE")
	}
	// Construct the client FIRST so the missing-key guard fires immediately — an
	// interactive `golem secret set TOK` with no GOLEM_API_KEY must fail fast,
	// not block on stdin. Only then read the value from stdin if it wasn't on argv.
	c, err := client.New()
	if err != nil {
		return err
	}
	if !hasValue {
		read, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read secret from stdin: %w", err)
		}
		// Trim a single trailing newline (the common `echo | golem secret set` case)
		// but preserve any other whitespace the secret may legitimately contain.
		value = strings.TrimRight(string(read), "\n")
	}
	if _, err := c.ConfigSet(ctx(), key, value, true); err != nil {
		return err
	}
	fmt.Printf("%s staged (secret) — run `golem publish` to apply.\n", key)
	return nil
}

func configRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: golem config rm KEY")
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	if _, err := c.ConfigRemove(ctx(), args[0]); err != nil {
		return err
	}
	fmt.Printf("%s removal staged — run `golem publish` to apply.\n", args[0])
	return nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	stream := fs.String("stream", "console", "log stream: console|errors|ci")
	follow := fs.Bool("follow", false, "poll the snapshot every few seconds (these are snapshot fetchers, not live streams)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *stream {
	case "console", "errors", "ci":
	default:
		return fmt.Errorf("unknown stream %q (want console|errors|ci)", *stream)
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	if !*follow {
		res, err := c.Logs(ctx(), *stream)
		if err != nil {
			return err
		}
		printLogStream(res)
		return nil
	}
	// --follow: these are snapshot endpoints, not real streams — we re-fetch on
	// an interval and re-render the whole snapshot each time.
	for {
		res, err := c.Logs(ctx(), *stream)
		if err != nil {
			return err
		}
		printLogStream(res)
		fmt.Printf("--- (re-fetching every %s; Ctrl-C to stop) ---\n", followInterval)
		time.Sleep(followInterval)
	}
}

func cmdSchedules(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: golem schedules <list|sync>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		if err := noFlags("schedules list", rest); err != nil {
			return err
		}
		c, err := client.New()
		if err != nil {
			return err
		}
		rows, err := c.SchedulesList(ctx())
		if err != nil {
			return err
		}
		printSchedules(rows)
		return nil
	case "sync":
		if err := noFlags("schedules sync", rest); err != nil {
			return err
		}
		c, err := client.New()
		if err != nil {
			return err
		}
		r, err := c.SchedulesSync(ctx())
		if err != nil {
			return err
		}
		fmt.Printf("synced from golem.json: %d declared, %d added, %d updated, %d removed.\n",
			r.Declared, r.Added, r.Updated, r.Removed)
		return nil
	default:
		return fmt.Errorf("unknown schedules subcommand %q (want list|sync)", sub)
	}
}

func cmdWebhooks(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: golem webhooks <list|add|rm>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		if err := noFlags("webhooks list", rest); err != nil {
			return err
		}
		c, err := client.New()
		if err != nil {
			return err
		}
		rows, err := c.WebhooksList(ctx())
		if err != nil {
			return err
		}
		printWebhooks(rows)
		return nil
	case "add":
		// `golem webhooks add LABEL TARGET_PATH` — quote a multi-word label.
		if len(rest) != 2 {
			return errors.New(`usage: golem webhooks add LABEL TARGET_PATH  (quote a multi-word LABEL; e.g. golem webhooks add "Payment events" /webhooks/stripe)`)
		}
		c, err := client.New()
		if err != nil {
			return err
		}
		r, err := c.WebhooksAdd(ctx(), rest[0], rest[1])
		if err != nil {
			return err
		}
		fmt.Println("webhook endpoint created. Point your provider (Stripe, GitHub, …) at:")
		fmt.Printf("  %s\n", r.URL)
		fmt.Println("(this URL is the credential — keep it secret. golem forwards POSTs here to your app,")
		fmt.Println(" HMAC-signed with GOLEM_WEBHOOK_SECRET so your app can verify them.)")
		return nil
	case "rm":
		if len(rest) != 1 {
			return errors.New("usage: golem webhooks rm ID")
		}
		c, err := client.New()
		if err != nil {
			return err
		}
		if _, err := c.WebhooksRemove(ctx(), rest[0]); err != nil {
			return err
		}
		fmt.Println("webhook endpoint removed — its URL will no longer reach your app.")
		return nil
	default:
		return fmt.Errorf("unknown webhooks subcommand %q (want list|add|rm)", sub)
	}
}

// cmdOpen prints the app's public URL and best-effort opens it in a browser.
// It uses whoami to resolve the slug (the only API call it makes).
func cmdOpen(args []string) error {
	if err := noFlags("open", args); err != nil {
		return err
	}
	c, err := client.New()
	if err != nil {
		return err
	}
	w, err := c.Whoami(ctx())
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://%s.tools.deadnet.co", w.Slug)
	fmt.Println(url)
	openInBrowser(url)
	return nil
}

// openInBrowser best-effort launches the platform's URL opener. Failure is
// silent — the URL is already printed for the user to click. Setting
// GOLEM_NO_BROWSER (used by tests) suppresses the launch.
func openInBrowser(url string) {
	if os.Getenv("GOLEM_NO_BROWSER") != "" {
		return
	}
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	default:
		bin = "xdg-open"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return
	}
	_ = exec.Command(bin, url).Start()
}

// --- rendering helpers --------------------------------------------------------

func printConfigRows(rows []client.ConfigRow) {
	if len(rows) == 0 {
		fmt.Println("(no config entries)")
		return
	}
	for _, r := range rows {
		val := ""
		if r.Secret {
			val = "(secret)"
		} else if r.Value != nil {
			val = *r.Value
		}
		var tags []string
		if r.Secret {
			tags = append(tags, "secret")
		}
		if r.Dirty {
			tags = append(tags, "staged")
		}
		if r.PendingRemoval {
			tags = append(tags, "pending-removal")
		}
		if !r.Published {
			tags = append(tags, "unpublished")
		}
		suffix := ""
		if len(tags) > 0 {
			suffix = "  [" + strings.Join(tags, ", ") + "]"
		}
		fmt.Printf("%s=%s%s\n", r.Key, val, suffix)
	}
}

func printSchedules(rows []client.ScheduleRow) {
	if len(rows) == 0 {
		fmt.Println("(no schedules declared in golem.json)")
		return
	}
	for _, r := range rows {
		state := "enabled"
		if !r.Enabled {
			state = "disabled"
		}
		last := ""
		if r.LastRunStatus != nil {
			last = fmt.Sprintf("  last run: %s", *r.LastRunStatus)
		}
		fmt.Printf("%s  (%s)  %s  [%s]%s\n", r.Name, r.Cadence, r.Target, state, last)
	}
}

func printWebhooks(rows []client.WebhookRow) {
	if len(rows) == 0 {
		fmt.Println("(no webhook endpoints — `golem webhooks add LABEL /path` to create one)")
		return
	}
	for _, r := range rows {
		state := "enabled"
		if !r.Enabled {
			state = "disabled"
		}
		fmt.Printf("%s  →  %s  [%s]\n", r.Label, r.TargetPath, state)
		fmt.Printf("  url: %s\n", r.URL)
		fmt.Printf("  id:  %s   (golem webhooks rm %s)\n", r.ID, r.ID)
	}
}

func printLogStream(res *client.LogStreamResult) {
	switch res.Status {
	case "ok":
		for _, row := range res.Rows {
			fmt.Println(renderLogRow(row))
		}
	case "empty":
		fmt.Println("(no log entries)")
	case "disabled":
		fmt.Printf("stream disabled: %s\n", res.Hint)
	case "error":
		fmt.Printf("stream error: %s\n", res.Message)
	default:
		fmt.Printf("(unexpected stream status %q)\n", res.Status)
	}
}

// renderLogRow prints a log row compactly. Rows are heterogeneous across streams
// (console lines, Sentry issues, CI runs), so we pretty-print the raw JSON rather
// than guess a single struct.
func renderLogRow(raw json.RawMessage) string {
	// A bare JSON string (e.g. a console line) renders without quotes.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return string(raw)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// noFlags rejects extra arguments for commands that take none.
func noFlags(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s takes no arguments (got %q)", name, strings.Join(args, " "))
	}
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `golem — thin client for the golem control-plane v1 API

Usage:
  golem whoami                      who am I + which app this key authorizes
  golem status                      publish state (config-dirty, code-dirty, publishing)
  golem publish [--force]           request a publish (rebuild + reconcile config)
  golem restart                     best-effort roll the app's machine
  golem config list                 list config entries (secret values never shown)
  golem config get KEY              print one entry
  golem config set KEY=VALUE        stage an env var
  golem config rm KEY               stage a removal
  golem env set KEY=VALUE           alias of 'config set'
  golem secret set KEY[=VALUE]      stage a secret (value read from stdin if omitted)
  golem secret rm KEY               stage a secret removal
  golem logs [--stream S] [--follow]  snapshot logs; S = console|errors|ci (default console)
  golem schedules list              list golem.json-declared schedules
  golem schedules sync              reconcile golem.json @ HEAD
  golem webhooks list               list inbound webhook endpoints (with their URLs)
  golem webhooks add LABEL PATH     create an endpoint; prints the public URL to give a provider
  golem webhooks rm ID              remove an endpoint
  golem open                        print + open https://<slug>.tools.deadnet.co
  golem version                     print the CLI version
  golem help                        this help

Environment:
  GOLEM_API_KEY   (required) Bearer token for every API call
  GOLEM_API_URL   (optional) API base, default https://platform.tools.deadnet.co
`)
}
