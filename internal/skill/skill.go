// Package skill embeds the golem Claude Code SKILL.md (an agent-facing golem CLI
// reference) and installs/refreshes it into a project or the user's home.
//
// Distribution & updateability: the SKILL.md is bundled into the CLI binary at
// build time (go:embed), so it rides the CLI's existing self-update channel —
// update the source, cut a release, and every builder's Codespace picks up the
// new skill the next time `golem` updates. `Install` is explicit + idempotent;
// `RefreshIfPresent` self-heals an already-installed skill after a self-update
// without ever creating one unprompted.
package skill

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var content []byte

// Content is the embedded SKILL.md bytes (the build-time source of truth).
func Content() []byte { return content }

// dirName is the skill's directory under .claude/skills/.
const dirName = "golem"

// Path returns where the skill file lives: the current project
// (<cwd>/.claude/skills/golem/SKILL.md) or, when global, the user's home
// (~/.claude/skills/golem/SKILL.md). Claude Code discovers both.
func Path(global bool) (string, error) {
	base, err := os.Getwd()
	if global {
		base, err = os.UserHomeDir()
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".claude", "skills", dirName, "SKILL.md"), nil
}

// Action is the outcome of an Install.
type Action string

const (
	Installed Action = "installed"
	Updated   Action = "updated"
	Unchanged Action = "unchanged"
)

// Install writes the embedded skill to Path(global), creating parent dirs. It is
// idempotent — it writes ONLY when the on-disk content differs from the embedded
// content — and reports which happened. The write is atomic (tmp + rename) so a
// partial write never leaves a corrupt SKILL.md.
func Install(global bool) (string, Action, error) {
	p, err := Path(global)
	if err != nil {
		return "", "", err
	}
	existing, readErr := os.ReadFile(p)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", "", readErr
	}
	if readErr == nil && bytes.Equal(existing, content) {
		return p, Unchanged, nil
	}
	if err := writeAtomic(p, content); err != nil {
		return "", "", err
	}
	if readErr == nil {
		return p, Updated, nil
	}
	return p, Installed, nil
}

// RefreshIfPresent best-effort UPDATES an already-installed golem skill (project
// and/or home) to the current embedded content, but NEVER creates one that isn't
// there. Call it after a successful command so an installed skill self-heals to
// the latest after a CLI self-update, without nagging or writing unprompted.
func RefreshIfPresent() {
	for _, global := range []bool{false, true} {
		p, err := Path(global)
		if err != nil {
			continue
		}
		existing, err := os.ReadFile(p)
		if err != nil || bytes.Equal(existing, content) {
			continue // absent (don't create) or already current
		}
		_ = writeAtomic(p, content)
	}
}

func writeAtomic(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
