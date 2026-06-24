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
	// silent when up to date, when no latest known, or on a dev build
	for _, c := range []struct {
		cur   string
		cache versionCache
	}{
		{"v0.1.3", versionCache{Latest: "v0.1.3"}},
		{"v0.1.3", versionCache{Latest: ""}},
		{"dev", versionCache{Latest: "v0.1.3"}},
		{"", versionCache{Latest: "v0.1.3"}},
	} {
		if msg := updateNotice(c.cur, c.cache); msg != "" {
			t.Errorf("expected no notice for cur=%q cache=%+v, got %q", c.cur, c.cache, msg)
		}
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
