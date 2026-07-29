package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const appName = "ai"

type Profile struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Command  string `json:"command"`
}

type Config struct {
	Profiles []Profile `json:"profiles"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		info, err := os.Stdin.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			usage(stdout)
			return nil
		}
		return runTUI()
	}
	configPath, err := configPath()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	switch args[0] {
	case "profile":
		return profileCommand(args[1:], &cfg, configPath, stdout)
	case "run":
		if len(args) < 2 {
			return errors.New("usage: ai run <profile> [command arguments...]")
		}
		profile, err := findProfile(cfg, args[1])
		if err != nil {
			return err
		}
		return launch(profile, args[2:], stdout, stderr)
	case "login":
		if len(args) != 2 {
			return errors.New("usage: ai login <profile>")
		}
		profile, err := findProfile(cfg, args[1])
		if err != nil {
			return err
		}
		return launch(profile, loginArgs(profile.Provider), stdout, stderr)
	case "env":
		if len(args) != 2 {
			return errors.New("usage: ai env <profile>")
		}
		profile, err := findProfile(cfg, args[1])
		if err != nil {
			return err
		}
		for _, value := range profileEnv(profile) {
			fmt.Fprintln(stdout, value)
		}
		return nil
	case "integrate":
		if len(args) != 3 || args[1] != "openusage" {
			return errors.New("usage: ai integrate openusage <profile>")
		}
		profile, err := findProfile(cfg, args[2])
		if err != nil {
			return err
		}
		return launchExternal("openusage", []string{"integrations", "install", openUsageIntegration(profile.Provider)}, profile, stdout, stderr)
	case "path":
		path, err := profileRoot()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, path)
		return nil
	case "help", "--help", "-h":
		usage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q; try 'ai help'", args[0])
	}
}

func profileCommand(args []string, cfg *Config, path string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "list" {
		profiles := append([]Profile(nil), cfg.Profiles...)
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
		for _, profile := range profiles {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", profile.Name, profile.Provider, profile.Command)
		}
		return nil
	}
	if args[0] != "add" || len(args) < 3 || len(args) > 4 {
		return errors.New("usage: ai profile add <name> <provider> [command]")
	}
	name, provider := args[1], args[2]
	if !validName(name) {
		return fmt.Errorf("invalid profile name %q; use letters, numbers, dots, dashes, or underscores", name)
	}
	if _, err := findProfile(*cfg, name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	}
	command := defaultCommand(provider)
	if len(args) == 4 {
		command = args[3]
	}
	if command == "" {
		return fmt.Errorf("unsupported provider %q; provide an explicit command", provider)
	}
	root, err := profileRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
		return err
	}
	cfg.Profiles = append(cfg.Profiles, Profile{Name: name, Provider: provider, Command: command})
	if err := saveConfig(path, *cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %s (%s)\n", name, provider)
	return nil
}

func launch(profile Profile, args []string, stdout, stderr io.Writer) error {
	return launchExternal(profile.Command, args, profile, stdout, stderr)
}

func launchExternal(command string, args []string, profile Profile, stdout, stderr io.Writer) error {
	workdir, err := ensureProfileState(profile)
	if err != nil {
		return err
	}
	unlock, err := acquireProfileLock(workdir)
	if err != nil {
		return err
	}
	defer unlock()
	cmd := exec.Command(command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(cleanEnvironment(os.Environ()), profileEnv(profile)...)
	return cmd.Run()
}

func acquireProfileLock(workdir string) (func(), error) {
	lockPath := filepath.Join(workdir, ".active.lock")
	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("profile is already running (%s); refusing concurrent token refresh", workdir)
		}
		return nil, err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		file.Close()
		os.Remove(lockPath)
		return nil, err
	}
	if err := file.Close(); err != nil {
		os.Remove(lockPath)
		return nil, err
	}
	return func() { _ = os.Remove(lockPath) }, nil
}

func openUsageIntegration(provider string) string {
	switch provider {
	case "claude":
		return "claude_code"
	case "deepseek":
		return "opencode"
	default:
		return provider
	}
}

func loginArgs(provider string) []string {
	switch provider {
	case "codex":
		return []string{"login"}
	case "claude":
		return nil
	case "opencode":
		return []string{"auth", "login"}
	default:
		return nil
	}
}

func profileEnv(profile Profile) []string {
	root, err := profileRoot()
	if err != nil {
		return nil
	}
	dir := filepath.Join(root, profile.Name)
	switch profile.Provider {
	case "codex":
		return []string{"CODEX_HOME=" + filepath.Join(dir, "codex")}
	case "claude":
		return []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(dir, "claude")}
	case "opencode":
		return []string{
			"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
			"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
			"XDG_STATE_HOME=" + filepath.Join(dir, "state"),
		}
	default:
		return nil
	}
}

func ensureProfileState(profile Profile) (string, error) {
	root, err := profileRoot()
	if err != nil {
		return "", err
	}
	workdir := filepath.Join(root, profile.Name)
	if err := os.MkdirAll(workdir, 0700); err != nil {
		return "", err
	}
	switch profile.Provider {
	case "codex":
		err = os.MkdirAll(filepath.Join(workdir, "codex"), 0700)
	case "claude":
		err = os.MkdirAll(filepath.Join(workdir, "claude"), 0700)
	case "opencode":
		for _, directory := range []string{"config", "data", "state"} {
			if err = os.MkdirAll(filepath.Join(workdir, directory), 0700); err != nil {
				break
			}
		}
	}
	if err != nil {
		return "", err
	}
	return workdir, nil
}

func cleanEnvironment(values []string) []string {
	blocked := map[string]bool{
		"CODEX_HOME": true, "CLAUDE_CONFIG_DIR": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true,
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[key] {
			result = append(result, value)
		}
	}
	return result
}

func defaultCommand(provider string) string {
	switch provider {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	case "opencode":
		return "opencode"
	case "deepseek":
		return "opencode"
	default:
		return ""
	}
}

func findProfile(cfg Config, name string) (Profile, error) {
	for _, profile := range cfg.Profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	return Profile{}, fmt.Errorf("profile %q not found", name)
}

func configPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName, "profiles.json"), nil
}

func profileRoot() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName, "profiles"), nil
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profiles-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func validName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "ai - isolated local profiles for AI CLI accounts")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  ai                                      interactive profile TUI")
	fmt.Fprintln(w, "  ai profile add <name> <provider> [command]")
	fmt.Fprintln(w, "  ai profile list")
	fmt.Fprintln(w, "  ai login <profile>")
	fmt.Fprintln(w, "  ai run <profile> [arguments...]")
	fmt.Fprintln(w, "  ai env <profile>")
	fmt.Fprintln(w, "  ai integrate openusage <profile>")
	fmt.Fprintln(w, "  ai path")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Providers with isolated state: codex, claude, opencode, deepseek")
}
