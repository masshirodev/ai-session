package main

import (
	"encoding/json"
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

func TestProfileAuthStateDetectsCredentialFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv(deepSeekKeyEnv, "")
	cases := []struct {
		profile Profile
		file    []string
		want    authState
	}{
		{Profile{Name: "codex-in", Provider: "codex"}, []string{"codex", "auth.json"}, authPresent},
		{Profile{Name: "codex-out", Provider: "codex"}, nil, authMissing},
		{Profile{Name: "claude-in", Provider: "claude"}, []string{"claude", ".credentials.json"}, authPresent},
		{Profile{Name: "claude-out", Provider: "claude"}, nil, authMissing},
		{Profile{Name: "opencode-in", Provider: "opencode"}, []string{"data", "opencode", "auth.json"}, authPresent},
		{Profile{Name: "opencode-out", Provider: "opencode"}, nil, authMissing},
		{Profile{Name: "deepseek", Provider: "deepseek"}, nil, authMissing},
		{Profile{Name: "custom", Provider: "something-else"}, nil, authUnknown},
	}
	for _, testCase := range cases {
		if testCase.file != nil {
			path := filepath.Join(append([]string{root, appName, "profiles", testCase.profile.Name}, testCase.file...)...)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if got := profileAuthState(testCase.profile); got != testCase.want {
			t.Errorf("profileAuthState(%s/%s) = %v, want %v", testCase.profile.Provider, testCase.profile.Name, got, testCase.want)
		}
	}
}

func TestProfileAuthStateReadsDeepSeekAPIKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	profile := Profile{Name: "deepseek", Provider: "deepseek", Command: "opencode"}
	t.Setenv(deepSeekKeyEnv, "sk-test")
	if got := profileAuthState(profile); got != authAPIKey {
		t.Fatalf("with %s set: got %v, want authAPIKey", deepSeekKeyEnv, got)
	}
	t.Setenv(deepSeekKeyEnv, "")
	if got := profileAuthState(profile); got != authMissing {
		t.Fatalf("without %s: got %v, want authMissing", deepSeekKeyEnv, got)
	}
	// The key is only read to test for emptiness; it must not reach the launch
	// environment as anything other than the value the user already exported.
	t.Setenv(deepSeekKeyEnv, "sk-test")
	for _, value := range profileEnv(profile) {
		if strings.Contains(value, "sk-test") {
			t.Fatalf("profile environment rewrote the API key: %q", value)
		}
	}
}

// An existing profile directory without a credential file must not read as
// authenticated: ensureProfileState creates those directories before login.
func TestProfileAuthStateIgnoresEmptyStateDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	profile := Profile{Name: "fresh", Provider: "codex", Command: "codex"}
	if _, err := ensureProfileState(profile); err != nil {
		t.Fatal(err)
	}
	if got := profileAuthState(profile); got != authMissing {
		t.Fatalf("fresh profile reads as %v, want authMissing", got)
	}
}

func writeProfileFile(t *testing.T, root, body string, parts ...string) {
	t.Helper()
	path := filepath.Join(append([]string{root, appName, "profiles"}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileModelReadsEachProviderSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	writeProfileFile(t, root, "model = \"gpt-5.6-luna\"\nmodel_reasoning_effort = \"medium\"\n", "cx", "codex", "config.toml")
	writeProfileFile(t, root, `{"theme":"dark","model":"opus"}`, "cl", "claude", "settings.json")
	writeProfileFile(t, root, `{"recent":[{"providerID":"deepseek","modelID":"deepseek-v4-pro"},{"providerID":"openai","modelID":"gpt-5.6-luna"}]}`,
		"oc", "state", "opencode", "model.json")

	cases := []struct {
		profile Profile
		want    string
	}{
		{Profile{Name: "cx", Provider: "codex"}, "gpt-5.6-luna"},
		{Profile{Name: "cl", Provider: "claude"}, "opus"},
		{Profile{Name: "oc", Provider: "opencode"}, "deepseek/deepseek-v4-pro"},
		{Profile{Name: "missing", Provider: "codex"}, ""},
		{Profile{Name: "ds", Provider: "deepseek"}, ""},
	}
	for _, testCase := range cases {
		if got := profileModel(testCase.profile); got != testCase.want {
			t.Errorf("profileModel(%s/%s) = %q, want %q", testCase.profile.Provider, testCase.profile.Name, got, testCase.want)
		}
	}
}

// The state file wins because OpenCode restores the model last chosen in the
// TUI; the config default only applies before anything has been picked.
func TestProfileModelPrefersOpenCodeStateOverConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	profile := Profile{Name: "oc", Provider: "opencode"}
	writeProfileFile(t, root, `{
		// a comment, and a trailing comma below
		"model": "openai/gpt-5.6-terra",
		"mcp": {"url": "https://example.com/mcp"},
	}`, "oc", "config", "opencode", "opencode.jsonc")
	if got := profileModel(profile); got != "openai/gpt-5.6-terra" {
		t.Fatalf("jsonc config model = %q, want openai/gpt-5.6-terra", got)
	}
	writeProfileFile(t, root, `{"recent":[{"providerID":"deepseek","modelID":"deepseek-v4-pro"}]}`, "oc", "state", "opencode", "model.json")
	if got := profileModel(profile); got != "deepseek/deepseek-v4-pro" {
		t.Fatalf("state model = %q, want deepseek/deepseek-v4-pro", got)
	}
}

func TestTomlTopLevelStringIgnoresSectionKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "# a comment\nmodel = \"gpt-5.6-luna\"\n\n[profiles.other]\nmodel = \"should-not-win\"\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if got := tomlTopLevelString(path, "model"); got != "gpt-5.6-luna" {
		t.Fatalf("tomlTopLevelString = %q, want gpt-5.6-luna", got)
	}

	sectionOnly := filepath.Join(dir, "section.toml")
	if err := os.WriteFile(sectionOnly, []byte("[projects.\"/tmp\"]\nmodel = \"nested\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := tomlTopLevelString(sectionOnly, "model"); got != "" {
		t.Fatalf("a key inside a section was reported as global: %q", got)
	}
}

func TestSanitizeJSONCKeepsStringContents(t *testing.T) {
	// The "//" inside a URL must survive: it is data, not a comment.
	input := []byte(`{
		"url": "https://example.com/a//b", // trailing note
		/* block */
		"model": "openai/gpt-5.6-terra",
	}`)
	var parsed struct {
		URL   string `json:"url"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(sanitizeJSONC(input), &parsed); err != nil {
		t.Fatalf("sanitized output is not valid JSON: %v\n%s", err, sanitizeJSONC(input))
	}
	if parsed.URL != "https://example.com/a//b" {
		t.Errorf("URL was mangled: %q", parsed.URL)
	}
	if parsed.Model != "openai/gpt-5.6-terra" {
		t.Errorf("model = %q", parsed.Model)
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
