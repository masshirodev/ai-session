package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanEnvironmentRemovesSharedAuthLocations(t *testing.T) {
	got := cleanEnvironment([]string{
		"PATH=/bin",
		"CODEX_HOME=/old/codex",
		"CLAUDE_CONFIG_DIR=/old/claude",
		"XDG_CONFIG_HOME=/old/config",
		"KEEP=value",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "CODEX_HOME") || strings.Contains(joined, "CLAUDE_CONFIG_DIR") || strings.Contains(joined, "XDG_CONFIG_HOME") {
		t.Fatalf("shared auth environment leaked: %v", got)
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "KEEP=value") {
		t.Fatalf("ordinary environment was not preserved: %v", got)
	}
}

func TestSaveConfigUsesPrivateAtomicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "profiles.json")
	if err := saveConfig(path, Config{Profiles: []Profile{{Name: "personal", Provider: "codex", Command: "codex"}}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "personal" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestProfileEnvironmentIsolatedByName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/shared/config")
	first := profileEnv(Profile{Name: "codex-personal", Provider: "codex"})
	second := profileEnv(Profile{Name: "codex-work", Provider: "codex"})
	if first[0] == second[0] || !strings.Contains(first[0], "codex-personal") || !strings.Contains(second[0], "codex-work") {
		t.Fatalf("profiles are not isolated: %v / %v", first, second)
	}
}

func TestProfileLockPreventsConcurrentLaunches(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireProfileLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := acquireProfileLock(dir); err == nil {
		t.Fatal("second lock unexpectedly succeeded")
	}
}
