package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tuiMode int

const (
	tuiList tuiMode = iota
	tuiForm
	tuiConfirmDelete
	tuiConfirmKill
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

type statusKind int

const (
	statusNone statusKind = iota
	statusOK
	statusErr
)

type tuiModel struct {
	configPath string
	profiles   []Profile
	cursor     int
	mode       tuiMode
	form       profileForm
	status     string
	statusKind statusKind
	width      int
	running    bool
	unlock     func()
}

func (m *tuiModel) setStatus(kind statusKind, message string) {
	m.statusKind = kind
	m.status = message
}

func (m *tuiModel) clearStatus() {
	m.statusKind = statusNone
	m.status = ""
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
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m tuiModel) Init() tea.Cmd {
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
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
		case tuiConfirmKill:
			return m.updateKill(msg)
		}
	case processFinishedMsg:
		m.running = false
		if m.unlock != nil {
			m.unlock()
			m.unlock = nil
		}
		if msg.err != nil {
			m.setStatus(statusErr, "process exited: "+msg.err.Error())
		} else {
			m.setStatus(statusOK, "process finished")
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
		m.clearStatus()
	case "e":
		if len(m.profiles) > 0 {
			profile := m.profiles[m.cursor]
			m.mode = tuiForm
			m.form = profileForm{name: profile.Name, provider: profile.Provider, command: profile.Command, original: profile.Name}
			m.clearStatus()
		}
	case "x":
		if len(m.profiles) > 0 {
			m.mode = tuiConfirmDelete
			m.clearStatus()
		}
	case "K":
		if len(m.profiles) > 0 {
			profile := m.profiles[m.cursor]
			if !profileIsRunning(profile) {
				return m, nil
			}
			m.mode = tuiConfirmKill
			m.clearStatus()
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

func (m tuiModel) updateKill(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		profile := m.profiles[m.cursor]
		if err := terminateProfile(profile); err != nil {
			var staleErr staleProfileLockError
			if errors.As(err, &staleErr) {
				m.setStatus(statusOK, staleErr.Error())
			} else {
				m.setStatus(statusErr, "stop failed: "+err.Error())
			}
		} else {
			m.setStatus(statusOK, "stopped "+profile.Name)
		}
		m.mode = tuiList
	case "n", "N", "esc", "q":
		m.mode = tuiList
		m.clearStatus()
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
				m.setStatus(statusErr, "cannot delete a running profile")
				m.mode = tuiList
				return m, nil
			}
			err = os.RemoveAll(filepath.Join(root, profile.Name))
		}
		if err != nil {
			m.setStatus(statusErr, "delete failed: "+err.Error())
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
				m.setStatus(statusErr, "delete failed: "+err.Error())
			} else {
				m.profiles = sortedProfiles(cfg.Profiles)
				if m.cursor >= len(m.profiles) && m.cursor > 0 {
					m.cursor--
				}
				m.setStatus(statusOK, "deleted "+profile.Name)
			}
		}
		m.mode = tuiList
	case "n", "N", "esc", "q":
		m.mode = tuiList
		m.clearStatus()
	}
	return m, nil
}

func (m tuiModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = tuiList
		m.clearStatus()
		return m, nil
	case "tab", "down", "enter":
		if m.form.field < 2 {
			m.form.field++
			return m, nil
		}
		if err := m.saveForm(); err != nil {
			m.setStatus(statusErr, err.Error())
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
	for index, profile := range m.profiles {
		if profile.Name == m.form.name {
			m.cursor = index
			break
		}
	}
	m.setStatus(statusOK, "saved "+m.form.name)
	return nil
}

func (m *tuiModel) execProfile(profile Profile, args []string) tea.Cmd {
	workdir, err := ensureProfileState(profile)
	if err != nil {
		m.setStatus(statusErr, err.Error())
		return nil
	}
	unlock, err := acquireProfileLock(workdir)
	if err != nil {
		m.setStatus(statusErr, err.Error())
		return nil
	}
	cmd := exec.Command(profile.Command, args...)
	cmd.Env = append(cleanEnvironment(os.Environ()), profileEnv(profile)...)
	m.running = true
	m.unlock = unlock
	return tea.Exec(trackedExecCommand{cmd: cmd, workdir: workdir}, func(err error) tea.Msg {
		return processFinishedMsg{err: err}
	})
}

type trackedExecCommand struct {
	cmd     *exec.Cmd
	workdir string
}

func (c trackedExecCommand) SetStdin(reader io.Reader)  { c.cmd.Stdin = reader }
func (c trackedExecCommand) SetStdout(writer io.Writer) { c.cmd.Stdout = writer }
func (c trackedExecCommand) SetStderr(writer io.Writer) { c.cmd.Stderr = writer }
func (c trackedExecCommand) Run() error {
	if err := c.cmd.Start(); err != nil {
		return err
	}
	if err := setProfileChildPID(c.workdir, c.cmd.Process.Pid); err != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
		return err
	}
	return c.cmd.Wait()
}

func (m tuiModel) View() string {
	width := m.contentWidth()
	sections := []string{m.headerView(width)}
	switch m.mode {
	case tuiForm:
		sections = append(sections, m.formView(width))
	case tuiConfirmDelete:
		sections = append(sections, m.listView(width), m.confirmView(width))
	default:
		sections = append(sections, m.listView(width))
	}
	sections = append(sections, rule(width), m.helpView(width))
	if status := m.statusView(width); status != "" {
		sections = append(sections, status)
	}
	return "\n" + lipgloss.NewStyle().PaddingLeft(2).Render(
		lipgloss.JoinVertical(lipgloss.Left, sections...),
	) + "\n"
}

// contentWidth keeps the layout comfortable on wide terminals and still usable
// on narrow ones; a zero width means the terminal size is not known yet.
func (m tuiModel) contentWidth() int {
	width := m.width - 4
	if m.width == 0 {
		width = 72
	}
	return min(max(width, 34), 86)
}

func (m tuiModel) headerView(width int) string {
	title := lipgloss.JoinHorizontal(lipgloss.Center, appBadgeStyle.Render("ai"), " ", headerTitleStyle.Render("session profiles"))
	count := fmt.Sprintf("%d profiles", len(m.profiles))
	switch len(m.profiles) {
	case 0:
		count = "no profiles"
	case 1:
		count = "1 profile"
	}
	return spread(title, headerCountStyle.Render(count), width) + "\n" + rule(width) + "\n"
}

func (m tuiModel) listView(width int) string {
	if len(m.profiles) == 0 {
		return emptyStateStyle.Render("No profiles yet — press a to create one.") + "\n"
	}
	nameWidth, providerWidth, modelWidth := 12, 8, len("MODEL")
	models := make([]string, len(m.profiles))
	for index, profile := range m.profiles {
		models[index] = profileModel(profile)
		nameWidth = max(nameWidth, lipgloss.Width(profile.Name))
		providerWidth = max(providerWidth, lipgloss.Width(profile.Provider))
		modelWidth = max(modelWidth, lipgloss.Width(models[index]))
	}
	nameWidth, providerWidth, modelWidth = min(nameWidth, 26), min(providerWidth, 12), min(modelWidth, 22)

	// The command is the least useful column — it usually repeats the provider —
	// so it is the one that gives up room, and disappears entirely when a narrow
	// terminal leaves nothing worth showing.
	fixed := 2 + nameWidth + 2 + providerWidth + 2 + authWidth + 2 + modelWidth + 2
	commandWidth := min(width-fixed-runningBadgeWidth, 24)
	showCommand := commandWidth >= 6

	header := pad("NAME", nameWidth) + "  " + pad("PROVIDER", providerWidth) + "  " + pad("AUTH", authWidth) + "  " + pad("MODEL", modelWidth)
	if showCommand {
		header += "  COMMAND"
	}
	rows := []string{"  " + columnHeaderStyle.Render(header)}
	for index, profile := range m.profiles {
		bar, name := "  ", nameStyle.Render(pad(truncate(profile.Name, nameWidth), nameWidth))
		if index == m.cursor {
			bar = cursorBarStyle.Render("▌ ")
			name = nameActiveStyle.Render(pad(truncate(profile.Name, nameWidth), nameWidth))
		}
		provider := providerStyle(profile.Provider).Render(pad(truncate(profile.Provider, providerWidth), providerWidth))
		left := bar + name + "  " + provider + "  " + authCell(profile) + "  " + modelCell(models[index], modelWidth)
		if showCommand {
			left += "  " + commandStyle.Render(truncate(profile.Command, commandWidth))
		}
		badge := ""
		if profileIsRunning(profile) {
			badge = liveStyle.Render("▶ running")
		}
		rows = append(rows, spread(left, badge, width))
	}
	return strings.Join(rows, "\n") + "\n"
}

// runningBadgeWidth reserves room for "▶ running" plus a separating space.
const runningBadgeWidth = 10

func modelCell(model string, width int) string {
	if model == "" {
		return unknownStyle.Render(pad("—", width))
	}
	return modelStyle.Render(pad(truncate(model, width), width))
}

// authWidth fits the widest cell rendered by authCell, plus the AUTH heading.
const authWidth = 5

func authCell(profile Profile) string {
	switch profileAuthState(profile) {
	case authPresent:
		return authPresentStyle.Render(pad("● yes", authWidth))
	case authAPIKey:
		return authKeyStyle.Render(pad("● key", authWidth))
	case authMissing:
		return authMissingStyle.Render(pad("○ no", authWidth))
	default:
		return authUnknownStyle.Render(pad("· ?", authWidth))
	}
}

func (m tuiModel) formView(width int) string {
	heading := "New profile"
	if !m.form.isNew {
		heading = "Edit " + m.form.original
	}
	fields := []struct{ label, value string }{
		{"Name", m.form.name},
		{"Provider", m.form.provider},
		{"Command", m.form.command},
	}
	lines := make([]string, 0, len(fields)+1)
	for index, field := range fields {
		label, value := fieldLabelStyle.Render(pad(field.label, 9)), fieldValueStyle.Render(field.value)
		if index == m.form.field {
			label = fieldLabelActive.Render(pad(field.label, 9))
			value += cursorStyle.Render(" ")
		}
		lines = append(lines, label+" "+value)
	}
	if m.form.field == 1 {
		lines = append(lines, "", hintStyle.Render("known providers: codex · claude · opencode · deepseek"))
	}
	body := panelStyle.Width(width - 2).Render(strings.Join(lines, "\n"))
	return sectionTitle.Render(heading) + "\n" + body + "\n"
}

func (m tuiModel) confirmView(width int) string {
	if m.cursor >= len(m.profiles) {
		return ""
	}
	profile := m.profiles[m.cursor]
	var body string
	if m.mode == tuiConfirmKill {
		body = dangerPanelStyle.Width(width - 2).Render(lipgloss.JoinVertical(lipgloss.Left,
			dangerTextStyle.Render("Stop "+profile.Name+"?"),
			confirmBodyStyle.Render("The running CLI process will be terminated."),
		))
		return "\n" + body + "\n"
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		dangerTextStyle.Render("Delete "+profile.Name+"?"),
		confirmBodyStyle.Render("Its isolated state directory and stored credentials"),
		confirmBodyStyle.Render("are removed. This cannot be undone."),
	)
	body = dangerPanelStyle.Width(width - 2).Render(content)
	return "\n" + body + "\n"
}

// helpView wraps at the content width so the key list reflows instead of
// running past the edge of a narrow terminal.
func (m tuiModel) helpView(width int) string {
	return lipgloss.NewStyle().Width(width).Render(m.helpEntries())
}

func (m tuiModel) helpEntries() string {
	switch m.mode {
	case tuiForm:
		return renderHelp([]helpEntry{
			{"tab/↵", "next field"},
			{"↵", "save on last field"},
			{"ctrl-u", "clear"},
			{"esc", "cancel"},
		})
	case tuiConfirmDelete:
		return renderHelp([]helpEntry{{"y", "delete"}, {"n/esc", "keep"}})
	case tuiConfirmKill:
		return renderHelp([]helpEntry{{"y/enter", "stop"}, {"n/esc", "keep running"}})
	default:
		if len(m.profiles) == 0 {
			return renderHelp([]helpEntry{{"a", "add profile"}, {"q", "quit"}})
		}
		return renderHelp([]helpEntry{
			{"↵", "run"},
			{"l", "login"},
			{"a", "add"},
			{"e", "edit"},
			{"x", "delete"},
			{"K", "stop"},
			{"q", "quit"},
		})
	}
}

func (m tuiModel) statusView(width int) string {
	if m.status == "" {
		return ""
	}
	icon, style := "•", statusInfoStyle
	switch m.statusKind {
	case statusOK:
		icon, style = "✓", statusOKStyle
	case statusErr:
		icon, style = "✗", statusErrStyle
	}
	return "\n" + style.Width(width).Render(icon+" "+m.status)
}

// profileIsRunning reports whether another process already holds the profile
// lock, so the list can show which accounts are busy.
func profileIsRunning(profile Profile) bool {
	root, err := profileRoot()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(root, profile.Name, ".active.lock"))
	return err == nil
}

func terminateProfile(profile Profile) error {
	root, err := profileRoot()
	if err != nil {
		return err
	}
	workdir := filepath.Join(root, profile.Name)
	lockPath := filepath.Join(workdir, ".active.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	lines := strings.Fields(string(data))
	if len(lines) == 0 {
		return errors.New("profile lock is empty")
	}
	pidText := lines[0]
	if len(lines) >= 2 {
		pidText = lines[1]
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return errors.New("profile lock has an invalid process PID")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.Signal(0)); errors.Is(err, syscall.ESRCH) {
		if removeErr := os.Remove(lockPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
		return staleProfileLockError{pid: pid}
	}
	if err := process.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			if removeErr := os.Remove(lockPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return removeErr
			}
			return nil
		}
		return err
	}
	return nil
}

type staleProfileLockError struct {
	pid int
}

func (e staleProfileLockError) Error() string {
	return fmt.Sprintf("cleared stale profile lock (process %d is not running)", e.pid)
}

func sortedProfiles(profiles []Profile) []Profile {
	result := append([]Profile(nil), profiles...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
