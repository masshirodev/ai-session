package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type tuiMode int

const (
	tuiList tuiMode = iota
	tuiForm
	tuiConfirmDelete
)

type profileForm struct {
	name     string
	provider string
	command  string
	field    int
	original string
	isNew    bool
}

type processFinishedMsg struct {
	err error
}

type tuiModel struct {
	configPath string
	profiles   []Profile
	cursor     int
	mode       tuiMode
	form       profileForm
	status     string
	running    bool
	unlock     func()
}

func runTUI() error {
	configPath, err := configPath()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	m := tuiModel{configPath: configPath, profiles: sortedProfiles(cfg.Profiles)}
	_, err = tea.NewProgram(m).Run()
	return err
}

func (m tuiModel) Init() tea.Cmd {
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.running {
			return m, nil
		}
		switch m.mode {
		case tuiList:
			return m.updateList(msg)
		case tuiForm:
			return m.updateForm(msg)
		case tuiConfirmDelete:
			return m.updateDelete(msg)
		}
	case processFinishedMsg:
		m.running = false
		if m.unlock != nil {
			m.unlock()
			m.unlock = nil
		}
		if msg.err != nil {
			m.status = "process finished: " + msg.err.Error()
		} else {
			m.status = "process finished"
		}
	}
	return m, nil
}

func (m tuiModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
	case "a":
		m.mode = tuiForm
		m.form = profileForm{name: "", provider: "codex", command: "codex", isNew: true}
	case "e":
		if len(m.profiles) > 0 {
			profile := m.profiles[m.cursor]
			m.mode = tuiForm
			m.form = profileForm{name: profile.Name, provider: profile.Provider, command: profile.Command, original: profile.Name}
		}
	case "x":
		if len(m.profiles) > 0 {
			m.mode = tuiConfirmDelete
			m.status = ""
		}
	case "l":
		if len(m.profiles) > 0 {
			return m, m.execProfile(m.profiles[m.cursor], loginArgs(m.profiles[m.cursor].Provider))
		}
	case "enter":
		if len(m.profiles) > 0 {
			return m, m.execProfile(m.profiles[m.cursor], nil)
		}
	}
	return m, nil
}

func (m tuiModel) updateDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		profile := m.profiles[m.cursor]
		root, err := profileRoot()
		if err == nil {
			lockPath := filepath.Join(root, profile.Name, ".active.lock")
			if _, lockErr := os.Stat(lockPath); lockErr == nil {
				m.status = "cannot delete a running profile"
				m.mode = tuiList
				return m, nil
			}
			err = os.RemoveAll(filepath.Join(root, profile.Name))
		}
		if err != nil {
			m.status = "delete failed: " + err.Error()
		} else {
			cfg, loadErr := loadConfig(m.configPath)
			if loadErr == nil {
				filtered := cfg.Profiles[:0]
				for _, candidate := range cfg.Profiles {
					if candidate.Name != profile.Name {
						filtered = append(filtered, candidate)
					}
				}
				cfg.Profiles = filtered
				err = saveConfig(m.configPath, cfg)
			}
			if err != nil {
				m.status = "delete failed: " + err.Error()
			} else {
				m.profiles = sortedProfiles(cfg.Profiles)
				if m.cursor >= len(m.profiles) && m.cursor > 0 {
					m.cursor--
				}
				m.status = "deleted " + profile.Name
			}
		}
		m.mode = tuiList
	case "n", "N", "esc", "q":
		m.mode = tuiList
		m.status = ""
	}
	return m, nil
}

func (m tuiModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = tuiList
		m.status = ""
		return m, nil
	case "tab", "down", "enter":
		if m.form.field < 2 {
			m.form.field++
			return m, nil
		}
		if err := m.saveForm(); err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.mode = tuiList
		return m, nil
	case "shift+tab", "up":
		if m.form.field > 0 {
			m.form.field--
		}
		return m, nil
	case "backspace", "ctrl+h":
		value := m.formValue()
		if len(value) > 0 {
			value = value[:len(value)-1]
			m.setFormValue(value)
		}
		return m, nil
	case "ctrl+u":
		m.setFormValue("")
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.setFormValue(m.formValue() + string(msg.Runes))
	}
	return m, nil
}

func (m *tuiModel) formValue() string {
	switch m.form.field {
	case 0:
		return m.form.name
	case 1:
		return m.form.provider
	default:
		return m.form.command
	}
}

func (m *tuiModel) setFormValue(value string) {
	switch m.form.field {
	case 0:
		m.form.name = value
	case 1:
		m.form.provider = value
	default:
		m.form.command = value
	}
}

func (m *tuiModel) saveForm() error {
	if !validName(m.form.name) {
		return errors.New("name must use letters, numbers, dots, dashes, or underscores")
	}
	if m.form.provider == "" || m.form.command == "" {
		return errors.New("provider and command are required")
	}
	cfg, err := loadConfig(m.configPath)
	if err != nil {
		return err
	}
	if m.form.isNew {
		if _, err := findProfile(cfg, m.form.name); err == nil {
			return fmt.Errorf("profile %q already exists", m.form.name)
		}
		root, err := profileRoot()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, m.form.name), 0700); err != nil {
			return err
		}
		cfg.Profiles = append(cfg.Profiles, Profile{Name: m.form.name, Provider: m.form.provider, Command: m.form.command})
	} else {
		if m.form.name != m.form.original {
			if _, err := findProfile(cfg, m.form.name); err == nil {
				return fmt.Errorf("profile %q already exists", m.form.name)
			}
			root, err := profileRoot()
			if err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(root, m.form.original), filepath.Join(root, m.form.name)); err != nil {
				return err
			}
		}
		for index := range cfg.Profiles {
			if cfg.Profiles[index].Name == m.form.original {
				cfg.Profiles[index] = Profile{Name: m.form.name, Provider: m.form.provider, Command: m.form.command}
				break
			}
		}
	}
	if err := saveConfig(m.configPath, cfg); err != nil {
		return err
	}
	m.profiles = sortedProfiles(cfg.Profiles)
	m.cursor = 0
	m.status = "saved " + m.form.name
	return nil
}

func (m *tuiModel) execProfile(profile Profile, args []string) tea.Cmd {
	workdir, err := ensureProfileState(profile)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	unlock, err := acquireProfileLock(workdir)
	if err != nil {
		m.status = err.Error()
		return nil
	}
	cmd := exec.Command(profile.Command, args...)
	cmd.Env = append(cleanEnvironment(os.Environ()), profileEnv(profile)...)
	m.running = true
	m.unlock = unlock
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return processFinishedMsg{err: err}
	})
}

func (m tuiModel) View() string {
	var b strings.Builder
	b.WriteString("ai profiles\n\n")
	if m.mode == tuiForm {
		b.WriteString("Profile editor\n\n")
		fields := []struct{ name, value string }{
			{"Name", m.form.name},
			{"Provider", m.form.provider},
			{"Command", m.form.command},
		}
		for index, field := range fields {
			marker := "  "
			if index == m.form.field {
				marker = "> "
			}
			b.WriteString(fmt.Sprintf("%s%-9s %s\n", marker, field.name+":", field.value))
		}
		b.WriteString("\nenter/tab: next or save  esc: cancel  ctrl-u: clear\n")
		return b.String()
	}
	if m.mode == tuiConfirmDelete {
		profile := m.profiles[m.cursor]
		b.WriteString(fmt.Sprintf("Delete %q and its isolated state? [y/N]\n", profile.Name))
		return b.String()
	}
	if len(m.profiles) == 0 {
		b.WriteString("No profiles yet. Press a to add one.\n")
	} else {
		for index, profile := range m.profiles {
			marker := "  "
			if index == m.cursor {
				marker = "> "
			}
			b.WriteString(fmt.Sprintf("%s%-24s %-10s %s\n", marker, profile.Name, profile.Provider, profile.Command))
		}
	}
	b.WriteString("\nenter: run  l: login  a: add  e: edit  x: delete  q: quit\n")
	if m.status != "" {
		b.WriteString("\n" + m.status + "\n")
	}
	return b.String()
}

func sortedProfiles(profiles []Profile) []Profile {
	result := append([]Profile(nil), profiles...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
