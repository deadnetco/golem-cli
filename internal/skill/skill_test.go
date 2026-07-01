package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	t.Chdir(t.TempDir())

	// first install → Installed, file has the embedded content
	p, action, err := Install(false)
	if err != nil {
		t.Fatal(err)
	}
	if action != Installed {
		t.Fatalf("action = %q, want installed", action)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, Content()) {
		t.Fatal("installed content does not match the embedded skill")
	}
	if want := filepath.Join(".claude", "skills", "golem", "SKILL.md"); !bytes.HasSuffix([]byte(p), []byte(want)) {
		t.Errorf("path = %q, want it to end in %q", p, want)
	}

	// re-install unchanged → Unchanged, no rewrite
	if _, action, err = Install(false); err != nil || action != Unchanged {
		t.Fatalf("re-install action = %q err = %v, want unchanged/nil", action, err)
	}

	// drifted on disk → Updated, restored to embedded content
	if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, action, err = Install(false); err != nil || action != Updated {
		t.Fatalf("drifted-install action = %q err = %v, want updated/nil", action, err)
	}
	got, _ = os.ReadFile(p)
	if !bytes.Equal(got, Content()) {
		t.Fatal("Update did not restore the embedded content")
	}
}

func TestRefreshIfPresentUpdatesButNeverCreates(t *testing.T) {
	t.Chdir(t.TempDir())

	// no skill installed → RefreshIfPresent must NOT create one
	RefreshIfPresent()
	if _, err := os.Stat(filepath.Join(".claude", "skills", "golem", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("RefreshIfPresent created a skill that was never installed")
	}

	// install, then drift it, then RefreshIfPresent should heal it back
	p, _, err := Install(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	RefreshIfPresent()
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, Content()) {
		t.Fatal("RefreshIfPresent did not heal an installed-but-stale skill")
	}
}

// Drift guard: the embedded skill must document every user-facing golem command,
// so the skill can never silently fall behind the CLI's actual command surface.
func TestSkillDocumentsCoreCommands(t *testing.T) {
	c := Content()
	for _, cmd := range []string{
		"golem publish", "golem status", "golem config", "golem secret",
		"golem dev pull", "golem logs", "golem schedules", "golem webhooks",
		"golem restart", "golem whoami", "golem open", "golem upgrade",
	} {
		if !bytes.Contains(c, []byte(cmd)) {
			t.Errorf("SKILL.md does not mention %q — the skill has drifted from the CLI", cmd)
		}
	}
	// It must be a valid skill: YAML frontmatter with the golem name.
	if !bytes.HasPrefix(c, []byte("---")) || !bytes.Contains(c, []byte("name: golem")) {
		t.Error("SKILL.md is missing its `--- name: golem` frontmatter")
	}
}
