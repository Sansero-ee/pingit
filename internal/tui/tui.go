package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sansero-ee/pingit/internal/config"
	"github.com/Sansero-ee/pingit/internal/logwriter"
	"github.com/Sansero-ee/pingit/internal/pinger"
)

type runState int

const (
	stateForm runState = iota
	stateRunning
	stateDone
)

const (
	fTargets = iota
	fDuration
	fInterval
	fLogPath
	fConfigPath
	fCheckbox
	fieldCount
)

type runHandle struct {
	run      *pinger.Run
	log      *logwriter.Writer
	cancel   context.CancelFunc
	logPath  string
	duration time.Duration
	interval time.Duration
}

type tickMsg time.Time
type runStartedMsg struct{ h *runHandle }
type runErrorMsg struct{ err error }
type runFinishedMsg struct{}

type Model struct {
	state      runState
	targets    textarea.Model
	duration   textinput.Model
	interval   textinput.Model
	logPath    textinput.Model
	configPath textinput.Model
	focus      int
	extended   bool
	errMsg     string
	h          *runHandle
	snapshots  []pinger.Snapshot
	started    time.Time
	stopping   bool
}

func newModel() Model {
	ta := textarea.New()
	ta.Placeholder = "8.8.8.8\n2001:db8::1\nexample.com"
	ta.SetHeight(6)
	ta.Focus()

	di := textinput.New()
	di.Placeholder = "60s"
	ii := textinput.New()
	ii.Placeholder = "1s"
	li := textinput.New()
	li.Placeholder = config.DefaultLogPath()
	ci := textinput.New()
	ci.Placeholder = "targets.txt"

	m := Model{
		state:      stateForm,
		targets:    ta,
		duration:   di,
		interval:   ii,
		logPath:    li,
		configPath: ci,
	}
	m.setFocus(fTargets)
	return m
}

func (m *Model) setFocus(f int) {
	m.focus = f
	m.targets.Blur()
	m.duration.Blur()
	m.interval.Blur()
	m.logPath.Blur()
	m.configPath.Blur()
	switch f {
	case fTargets:
		m.targets.Focus()
	case fDuration:
		m.duration.Focus()
	case fInterval:
		m.interval.Focus()
	case fLogPath:
		m.logPath.Focus()
	case fConfigPath:
		m.configPath.Focus()
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case stateForm:
			return m.updateForm(msg)
		case stateRunning:
			switch msg.String() {
			case "ctrl+c", "q":
				cmd := m.stopRun()
				return m, cmd
			}
		case stateDone:
			switch msg.String() {
			case "ctrl+c", "q", "esc", "enter":
				return m, tea.Quit
			}
		}
	case tickMsg:
		if m.state == stateRunning && m.h != nil {
			m.snapshots = m.h.run.Snapshots()
			select {
			case <-m.h.run.Done():
				cmd := m.stopRun()
				return m, cmd
			default:
			}
			return m, refreshTick()
		}
	case runStartedMsg:
		m.h = msg.h
		m.started = time.Now()
		m.state = stateRunning
		m.errMsg = ""
		return m, refreshTick()
	case runErrorMsg:
		m.state = stateForm
		m.stopping = false
		m.errMsg = "start failed: " + msg.err.Error()
	case runFinishedMsg:
		m.state = stateDone
		m.stopping = false
		if m.h != nil {
			m.snapshots = m.h.run.Snapshots()
		}
	}
	return m, nil
}

func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	case "ctrl+s":
		return m.startRun()
	case "ctrl+l":
		m.loadConfigFile()
		return m, nil
	case "tab":
		m.setFocus((m.focus + 1) % fieldCount)
		return m, nil
	case "shift+tab":
		m.setFocus((m.focus - 1 + fieldCount) % fieldCount)
		return m, nil
	case " ":
		if m.focus == fCheckbox {
			m.extended = !m.extended
			return m, nil
		}
	}
	var cmd tea.Cmd
	switch m.focus {
	case fTargets:
		m.targets, cmd = m.targets.Update(msg)
	case fDuration:
		m.duration, cmd = m.duration.Update(msg)
	case fInterval:
		m.interval, cmd = m.interval.Update(msg)
	case fLogPath:
		m.logPath, cmd = m.logPath.Update(msg)
	case fConfigPath:
		m.configPath, cmd = m.configPath.Update(msg)
	}
	return m, cmd
}

func (m Model) startRun() (tea.Model, tea.Cmd) {
	m.errMsg = ""
	targets, err := parseTargetLines(m.targets.Value())
	if err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	dur, err := time.ParseDuration(strings.TrimSpace(m.duration.Value()))
	if err != nil {
		m.errMsg = "invalid duration: " + err.Error()
		return m, nil
	}
	iv, err := time.ParseDuration(strings.TrimSpace(m.interval.Value()))
	if err != nil {
		m.errMsg = "invalid interval: " + err.Error()
		return m, nil
	}
	if dur <= 0 || iv <= 0 {
		m.errMsg = "duration and interval must be positive"
		return m, nil
	}
	if err := config.ValidateTargets(targets, m.extended); err != nil {
		m.errMsg = err.Error()
		return m, nil
	}
	logPath := strings.TrimSpace(m.logPath.Value())
	if logPath == "" {
		logPath = config.DefaultLogPath()
	}
	return m, func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		resolved := pinger.Resolve(targets)
		w, err := logwriter.Open(logPath)
		if err != nil {
			cancel()
			return runErrorMsg{err}
		}
		run := pinger.Start(ctx, resolved, iv, dur, w)
		return runStartedMsg{&runHandle{
			run:      run,
			log:      w,
			cancel:   cancel,
			logPath:  logPath,
			duration: dur,
			interval: iv,
		}}
	}
}

func (m *Model) loadConfigFile() {
	path := strings.TrimSpace(m.configPath.Value())
	if path == "" {
		m.errMsg = "config file path is empty"
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.errMsg = err.Error()
		return
	}
	targets, dur, iv, err := config.ParseConfig(data)
	if err != nil {
		m.errMsg = err.Error()
		return
	}
	m.targets.SetValue(strings.Join(targets, "\n"))
	m.duration.SetValue(dur.String())
	m.interval.SetValue(iv.String())
	m.errMsg = ""
}

func (m *Model) stopRun() tea.Cmd {
	if m.h == nil || m.stopping {
		return nil
	}
	m.stopping = true
	m.h.cancel()
	h := m.h
	return func() tea.Msg {
		h.run.Wait()
		h.log.Close()
		return runFinishedMsg{}
	}
}

func (m Model) View() string {
	switch m.state {
	case stateForm:
		return m.formView()
	case stateRunning:
		return m.runView()
	case stateDone:
		return m.doneView()
	}
	return ""
}

func (m Model) formView() string {
	var b strings.Builder
	b.WriteString("MULTIPING — parallel ICMP monitor\n\n")
	b.WriteString("Targets (one per line, IPv4/IPv6/hostname):\n")
	b.WriteString(m.targets.View())
	b.WriteString("\n\n")
	line := func(label string, in textinput.Model, f int) string {
		cursor := "  "
		if m.focus == f {
			cursor = "▸ "
		}
		return cursor + fmt.Sprintf("%-13s", label) + in.View() + "\n"
	}
	b.WriteString(line("Duration:", m.duration, fDuration))
	b.WriteString(line("Interval:", m.interval, fInterval))
	b.WriteString(line("Log file:", m.logPath, fLogPath))
	b.WriteString(line("Config file:", m.configPath, fConfigPath))
	b.WriteString("\n")
	check := "[ ]"
	if m.extended {
		check = "[x]"
	}
	cursor := "  "
	if m.focus == fCheckbox {
		cursor = "▸ "
	}
	b.WriteString(cursor + check + " allow up to 100 targets\n\n")
	if m.errMsg != "" {
		b.WriteString("Error: " + m.errMsg + "\n\n")
	}
	b.WriteString("tab next field · space toggle · ctrl+l load config file · ctrl+s start · ctrl+q quit")
	return b.String()
}

func (m Model) runView() string {
	var b strings.Builder
	if m.h != nil {
		elapsed := time.Since(m.started).Truncate(time.Second)
		fmt.Fprintf(&b, "Running — %s of %s, interval %s\n\n", elapsed, m.h.duration, m.h.interval)
	}
	b.WriteString(renderTable(m.snapshots))
	b.WriteString("\n\nctrl+c stop gracefully (log is flushed) · q stop and view summary")
	return b.String()
}

func (m Model) doneView() string {
	var b strings.Builder
	b.WriteString("Run finished.\n\n")
	if m.h != nil {
		fmt.Fprintf(&b, "Log file: %s\n\n", m.h.logPath)
	}
	b.WriteString(renderTable(m.snapshots))
	var sent, recv, lost int
	for _, s := range m.snapshots {
		sent += s.Sent
		recv += s.Received
		lost += s.Lost
	}
	fmt.Fprintf(&b, "\nTotal: sent %d, received %d, lost %d\n", sent, recv, lost)
	b.WriteString("\nq / esc / enter — exit")
	return b.String()
}

func renderTable(snaps []pinger.Snapshot) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tRESOLVED\tSENT\tRECV\tLOST %\tMIN ms\tAVG ms\tMAX ms")
	for _, s := range snaps {
		if s.Err != "" {
			fmt.Fprintf(tw, "%s\tresolve failed: %s\t-\t-\t-\t-\t-\t-\n", s.Target, s.Err)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.1f\t%.2f\t%.2f\t%.2f\n",
			s.Target, s.IP, s.Sent, s.Received, s.LostPct, s.MinMs, s.AvgMs, s.MaxMs)
	}
	tw.Flush()
	return b.String()
}

func parseTargetLines(s string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets specified")
	}
	return out, nil
}

func refreshTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func Run() error {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
