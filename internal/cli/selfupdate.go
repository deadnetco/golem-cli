package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Self-update: `golem upgrade` replaces the running binary with the latest release, and a cached,
// best-effort version check nudges the user when a newer release is out. Both resolve "latest" via
// the GitHub releases CDN redirect (no API → no 60/hr unauthenticated rate limit), and download the
// platform asset from releases/latest/download/. Stdlib-only.

// releasesURL is the golem-cli releases base. Overridable via GOLEM_RELEASES_URL (used by tests; also
// a seam if the repo moves). Default is the public repo.
func releasesURL() string {
	if v := os.Getenv("GOLEM_RELEASES_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://github.com/deadnetco/golem-cli/releases"
}

// assetName is the release asset for a platform, e.g. "golem-linux-amd64" / "golem-windows-amd64.exe".
// Matches the release workflow's published asset names exactly.
func assetName(goos, goarch string) string {
	n := "golem-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// latestTag resolves the newest release tag (e.g. "v0.1.3") via the /releases/latest 302 redirect,
// reading the Location header rather than calling the rate-limited GitHub API.
func latestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesURL()+"/latest", nil)
	if err != nil {
		return "", err
	}
	// Don't follow the redirect — we want to read its Location.
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	loc := res.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("could not parse latest release tag from %q", loc)
	}
	return strings.Trim(loc[i+len("/tag/"):], "/"), nil
}

// --- `golem upgrade` ----------------------------------------------------------

func cmdUpgrade(args []string, current string) error {
	if err := noFlags("upgrade", args); err != nil {
		return err
	}
	target, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the golem binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg, err := upgradeTo(ctx, current, target)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// upgradeTo downloads the latest asset and replaces `target`. Returns a human message. Split from
// cmdUpgrade (which supplies os.Executable()) so tests can drive it against a temp target file.
func upgradeTo(ctx context.Context, current, target string) (string, error) {
	latest, tagErr := latestTag(ctx)
	if tagErr == nil && latest != "" && latest == current {
		return fmt.Sprintf("golem is already up to date (%s)", current), nil
	}
	url := releasesURL() + "/latest/download/" + assetName(runtime.GOOS, runtime.GOARCH)
	tmp, err := downloadToTemp(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download latest golem: %w", err)
	}
	defer os.Remove(tmp)
	if err := selfReplace(tmp, target); err != nil {
		return "", fmt.Errorf("replace %s: %w", target, err)
	}
	if latest != "" {
		return fmt.Sprintf("golem upgraded to %s", latest), nil
	}
	return "golem upgraded to the latest release", nil
}

// downloadToTemp GETs url (following redirects) into a fresh temp file and returns its path.
func downloadToTemp(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d fetching %s", res.StatusCode, url)
	}
	f, err := os.CreateTemp("", "golem-upgrade-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// selfReplace atomically swaps `target` for the binary at `src`. It stages a sibling temp file in the
// target's directory and renames it over the target — rename is immune to ETXTBSY (the running binary
// keeps its inode) and is atomic on the same filesystem. If the directory isn't writable, it escalates
// via sudo (mirrors how setup.sh installs into root-owned /usr/local/bin).
func selfReplace(src, target string) error {
	dir := filepath.Dir(target)
	staged := filepath.Join(dir, "."+filepath.Base(target)+".upgrade.tmp")
	if err := copyExecutable(src, staged); err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return sudoReplace(src, target)
		}
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		os.Remove(staged)
		if errors.Is(err, fs.ErrPermission) {
			return sudoReplace(src, target)
		}
		return err
	}
	return nil
}

// copyExecutable copies src → dst (a NEW path, so no ETXTBSY) with 0755 perms.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Chmod(dst, 0o755)
}

// sudoReplace installs src into target via sudo when the target dir is root-owned (the devcontainer
// case: /usr/local/bin). It writes a temp sibling then renames over the target so a running binary
// isn't truncated (ETXTBSY-safe), matching selfReplace's strategy.
func sudoReplace(src, target string) error {
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("cannot write %s (permission denied) and sudo is unavailable — re-run as a user who can write it", target)
	}
	staged := target + ".upgrade.tmp"
	script := fmt.Sprintf("install -m 0755 %q %q && mv -f %q %q", src, staged, staged, target)
	cmd := exec.Command("sudo", "sh", "-c", script)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// --- "new version available" nudge -------------------------------------------

type versionCache struct {
	CheckedAt int64  `json:"checkedAt"` // unix seconds of the last latest-tag fetch
	Latest    string `json:"latest"`    // last-seen latest tag
}

const updateCheckTTL = 24 * time.Hour

func versionCachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(base, "golem", "version.json")
}

func readVersionCache() versionCache {
	var c versionCache
	if b, err := os.ReadFile(versionCachePath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func writeVersionCache(c versionCache) {
	p := versionCachePath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(p, b, 0o644)
	}
}

// updateNotice is the pure decision: the message to show given the running version and the cache,
// or "" for nothing. Separated so the logic is unit-tested without network or a TTY.
func updateNotice(current string, c versionCache) string {
	if current == "" || current == "dev" || c.Latest == "" || c.Latest == current {
		return ""
	}
	return fmt.Sprintf("golem %s is available (you have %s) — run `golem upgrade` to update.", c.Latest, current)
}

// maybePrintUpdateNotice prints the nudge to stderr when a newer release is known. It is best-effort
// and side-effect-light: it reads the cache for the message and refreshes it (synchronously, tight
// timeout) only when stale — at most ~once/day. Skipped for meta commands, dev builds, when
// GOLEM_NO_UPDATE_CHECK is set, and when stdout is not a TTY (pipes/CI stay clean and check-free).
func maybePrintUpdateNotice(current, cmd string) {
	switch cmd {
	case "version", "--version", "-v", "help", "-h", "--help", "upgrade":
		return
	}
	if current == "dev" || os.Getenv("GOLEM_NO_UPDATE_CHECK") != "" {
		return
	}
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return // not an interactive terminal — don't check or nag
	}
	c := readVersionCache()
	if time.Since(time.Unix(c.CheckedAt, 0)) > updateCheckTTL {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if tag, err := latestTag(ctx); err == nil && tag != "" {
			c = versionCache{CheckedAt: time.Now().Unix(), Latest: tag}
			writeVersionCache(c)
		}
	}
	if msg := updateNotice(current, c); msg != "" {
		fmt.Fprintln(os.Stderr, "\n"+msg)
	}
}
