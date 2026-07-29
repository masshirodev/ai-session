package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"testing"
)

func testProfiles() []Profile {
	return []Profile{
		{Name: "claude-personal", Provider: "claude", Command: "claude"},
		{Name: "codex-work", Provider: "codex", Command: "codex"},
	}
}

func TestViewListsProfilesWithHelp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := tuiModel{profiles: testProfiles(), width: 80, cursor: 1}
	view := m.View()
	for _, want := range []string{"session profiles", "2 profiles", "claude-personal", "codex-work", "run", "login", "quit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view is missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "▌ codex-work") {
		t.Fatalf("selected profile is not marked:\n%s", view)
	}
}

func TestViewShowsAuthenticationColumn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	credentials := filepath.Join(root, appName, "profiles", "claude-personal", "claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(credentials), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentials, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(deepSeekKeyEnv, "sk-test")
	profiles := append(testProfiles(), Profile{Name: "deepseek", Provider: "deepseek", Command: "opencode"})
	view := tuiModel{profiles: profiles, width: 80}.View()
	if !strings.Contains(view, "AUTH") {
		t.Fatalf("missing AUTH column header:\n%s", view)
	}
	if strings.Count(view, "● yes") != 1 || strings.Count(view, "○ no") != 1 || strings.Count(view, "● key") != 1 {
		t.Fatalf("auth column does not distinguish the three auth sources:\n%s", view)
	}
	if strings.Contains(view, "sk-test") {
		t.Fatalf("the API key value leaked into the view:\n%s", view)
	}
}

func TestViewShowsSelectedModelWhenKnown(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	writeProfileFile(t, root, `{"model":"opus"}`, "claude-personal", "claude", "settings.json")
	view := tuiModel{profiles: testProfiles(), width: 100}.View()
	if !strings.Contains(view, "MODEL") || !strings.Contains(view, "opus") {
		t.Fatalf("model column missing:\n%s", view)
	}
	if !strings.Contains(view, "—") {
		t.Fatalf("a profile with no discoverable model should show a dash:\n%s", view)
	}
}

// The command column is the one that yields room, so a narrow terminal must
// keep the model visible rather than wrapping the row.
func TestViewDropsCommandColumnWhenNarrow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	writeProfileFile(t, root, `{"model":"opus"}`, "claude-personal", "claude", "settings.json")
	wide := tuiModel{profiles: testProfiles(), width: 100}.View()
	narrow := tuiModel{profiles: testProfiles(), width: 46}.View()
	if !strings.Contains(wide, "COMMAND") {
		t.Fatalf("wide view should show the command column:\n%s", wide)
	}
	if strings.Contains(narrow, "COMMAND") {
		t.Fatalf("narrow view should drop the command column:\n%s", narrow)
	}
	if !strings.Contains(narrow, "opus") {
		t.Fatalf("narrow view dropped the model instead:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if lipgloss.Width(line) > 46 {
			t.Fatalf("line overflows a 46-column terminal (%d):\n%q", lipgloss.Width(line), line)
		}
	}
}

func TestViewMarksRunningProfiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	profiles := testProfiles()
	dir := filepath.Join(root, appName, "profiles", profiles[1].Name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".active.lock"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := tuiModel{profiles: profiles, width: 80}
	view := m.View()
	if strings.Count(view, "running") != 1 {
		t.Fatalf("expected exactly one running marker:\n%s", view)
	}
	if !strings.Contains(view, "codex-work") {
		t.Fatalf("locked profile missing from view:\n%s", view)
	}
}

func TestViewEmptyStateInvitesFirstProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	view := tuiModel{width: 80}.View()
	if !strings.Contains(view, "No profiles yet") || !strings.Contains(view, "add profile") {
		t.Fatalf("unhelpful empty state:\n%s", view)
	}
	if strings.Contains(view, "delete") {
		t.Fatalf("empty state offers keys that do nothing:\n%s", view)
	}
}

func TestViewFormShowsFieldsAndError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := tuiModel{profiles: testProfiles(), width: 80, mode: tuiForm}
	m.form = profileForm{name: "codex-work", provider: "codex", command: "codex", field: 1, original: "codex-work"}
	m.setStatus(statusErr, "already exists")
	view := m.View()
	for _, want := range []string{"Edit codex-work", "Name", "Provider", "Command", "known providers", "✗ already exists"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form view is missing %q:\n%s", want, view)
		}
	}
}

func TestViewConfirmDeleteNamesProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := tuiModel{profiles: testProfiles(), width: 80, cursor: 1, mode: tuiConfirmDelete}
	view := m.View()
	if !strings.Contains(view, "Delete codex-work?") || !strings.Contains(view, "cannot be undone") {
		t.Fatalf("confirmation is unclear:\n%s", view)
	}
	if !strings.Contains(view, "keep") {
		t.Fatalf("confirmation lacks a cancel hint:\n%s", view)
	}
}

func TestViewSurvivesNarrowAndUnknownWidths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, width := range []int{0, 1, 20, 200} {
		m := tuiModel{profiles: testProfiles(), width: width}
		if view := m.View(); !strings.Contains(view, "codex-work") {
			t.Fatalf("width %d dropped content:\n%s", width, view)
		}
	}
}

func TestTruncateKeepsWidthBudget(t *testing.T) {
	if got := truncate("codex-personal", 6); got != "codex…" {
		t.Fatalf("truncate = %q, want %q", got, "codex…")
	}
	if got := truncate("codex", 8); got != "codex" {
		t.Fatalf("truncate shortened a fitting value: %q", got)
	}
	if got := pad("codex", 8); got != "codex   " {
		t.Fatalf("pad = %q", got)
	}
}
