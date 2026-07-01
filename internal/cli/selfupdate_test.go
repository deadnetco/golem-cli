package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}:   "golem-linux-amd64",
		{"linux", "arm64"}:   "golem-linux-arm64",
		{"darwin", "arm64"}:  "golem-darwin-arm64",
		{"windows", "amd64"}: "golem-windows-amd64.exe",
	}
	for in, want := range cases {
		if got := assetName(in[0], in[1]); got != want {
			t.Errorf("assetName(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// fakeReleases serves the two endpoints latestTag/upgradeTo use: /latest (302 → /tag/<tag>) and
// /latest/download/<asset> (the new binary bytes). Returns the base URL.
func fakeReleases(t *testing.T, tag, assetBody string) string {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest" && r.Method == http.MethodHead:
			w.Header().Set("Location", base+"/tag/"+tag)
			w.WriteHeader(http.StatusFound)
		case r.URL.Path == "/latest/download/"+assetName(runtime.GOOS, runtime.GOARCH):
			_, _ = w.Write([]byte(assetBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	t.Setenv("GOLEM_RELEASES_URL", base)
	return base
}

func TestLatestTag(t *testing.T) {
	fakeReleases(t, "v9.9.10", "")
	tag, err := latestTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v9.9.10" {
		t.Errorf("tag = %q, want v9.9.10", tag)
	}
}

func TestUpdateNotice(t *testing.T) {
	// shows a message only when current differs from a known latest
	if msg := updateNotice("v0.1.2", versionCache{Latest: "v0.1.3"}); msg == "" {
		t.Error("expected a notice when current<latest")
	}
	// silent when up to date, when no latest known, on a dev build — and, crucially, when the
	// running binary is AHEAD of the cached latest (a local/ahead build or post-release cache lag):
	// it must NOT print a backwards "older is available" nudge.
	for _, c := range []struct {
		cur   string
		cache versionCache
	}{
		{"v0.1.3", versionCache{Latest: "v0.1.3"}},
		{"v0.1.3", versionCache{Latest: ""}},
		{"dev", versionCache{Latest: "v0.1.3"}},
		{"", versionCache{Latest: "v0.1.3"}},
		{"v0.1.7", versionCache{Latest: "v0.1.6"}}, // ahead of cached latest — the bug: no notice
		{"v0.2.0", versionCache{Latest: "v0.1.9"}}, // minor ahead
		{"v1.0.0", versionCache{Latest: "v0.9.9"}}, // major ahead
	} {
		if msg := updateNotice(c.cur, c.cache); msg != "" {
			t.Errorf("expected no notice for cur=%q cache=%+v, got %q", c.cur, c.cache, msg)
		}
	}
	// still nags across minor/major bumps and ignores a v-prefix / pre-release suffix
	for _, c := range []struct {
		cur   string
		cache versionCache
	}{
		{"v0.1.9", versionCache{Latest: "v0.2.0"}},
		{"v0.9.9", versionCache{Latest: "v1.0.0"}},
		{"0.1.2", versionCache{Latest: "0.1.3"}},
	} {
		if msg := updateNotice(c.cur, c.cache); msg == "" {
			t.Errorf("expected a notice for cur=%q cache=%+v", c.cur, c.cache)
		}
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.6", "v0.1.7", true},
		{"v0.1.7", "v0.1.6", false},
		{"v0.1.7", "v0.1.7", false},
		{"v0.2.0", "v0.10.0", true}, // numeric compare, not lexical
		{"v1.0.0", "v0.9.9", false},
		{"0.1.2", "v0.1.3", true}, // mixed prefix
		{"v0.1.3-rc1", "v0.1.3", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	// unparseable pair falls back to string inequality
	if !versionLess("weird", "other") {
		t.Error("versionLess fallback: distinct unparseable strings should be 'less' (a != b)")
	}
	if versionLess("weird", "weird") {
		t.Error("versionLess fallback: identical unparseable strings should not be 'less'")
	}
}

func TestVersionCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	want := versionCache{CheckedAt: 1782300000, Latest: "v1.2.3"}
	writeVersionCache(want)
	got := readVersionCache()
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestSelfReplace_WritableDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "golem")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "downloaded")
	if err := os.WriteFile(src, []byte("NEW-BINARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := selfReplace(src, target); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW-BINARY" {
		t.Errorf("target content = %q, want NEW-BINARY", got)
	}
	// staged temp is cleaned up by the rename
	if _, err := os.Stat(filepath.Join(dir, ".golem.upgrade.tmp")); !os.IsNotExist(err) {
		t.Errorf("staged temp should not remain: %v", err)
	}
	// new binary is executable
	if fi, _ := os.Stat(target); fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("target not executable: %v", fi.Mode())
	}
}

func TestUpgradeTo_DownloadsAndReplaces(t *testing.T) {
	fakeReleases(t, "v9.9.10", "FRESH-GOLEM-BINARY")
	dir := t.TempDir()
	target := filepath.Join(dir, "golem")
	if err := os.WriteFile(target, []byte("STALE"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := upgradeTo(context.Background(), "v0.0.1", target)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "golem upgraded to v9.9.10" {
		t.Errorf("msg = %q", msg)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "FRESH-GOLEM-BINARY" {
		t.Errorf("target content = %q, want FRESH-GOLEM-BINARY", got)
	}
}

func TestUpgradeTo_AlreadyLatest(t *testing.T) {
	fakeReleases(t, "v9.9.10", "SHOULD-NOT-DOWNLOAD")
	dir := t.TempDir()
	target := filepath.Join(dir, "golem")
	if err := os.WriteFile(target, []byte("CURRENT"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := upgradeTo(context.Background(), "v9.9.10", target) // current == latest
	if err != nil {
		t.Fatal(err)
	}
	if msg != "golem is already up to date (v9.9.10)" {
		t.Errorf("msg = %q", msg)
	}
	if got, _ := os.ReadFile(target); string(got) != "CURRENT" {
		t.Errorf("target should be untouched, got %q", got)
	}
}

func TestUpgradeCommand_RejectsArgs(t *testing.T) {
	// `golem upgrade foo` is a usage error before touching anything.
	if err := cmdUpgrade([]string{"foo"}, "v0.1.2"); err == nil {
		t.Fatal("expected an arg-count error")
	}
}

// guard against an accidental dependency on the live network in the notice path
func TestMaybePrintUpdateNotice_SkipsNonTTY(t *testing.T) {
	// stdout in `go test` is not a char device → the function must return without any network call.
	done := make(chan struct{})
	go func() { maybePrintUpdateNotice("v0.1.2", "status"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("maybePrintUpdateNotice blocked (should no-op on a non-TTY stdout)")
	}
}
