package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ChowDPa02K/jellyfin-remora/internal/kickstart"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type kickstartStage int

const (
	stageInstallation kickstartStage = iota
	stageArchive
	stageArchiveValidation
	stageHome
	stageMedia
	stageServerName
	stageDisplayLanguage
	stageMetadataLanguage
	stageMetadataRegion
	stageAdminPassword
	stageSubmit
)

type selectionItem string

func (i selectionItem) FilterValue() string { return string(i) }

type kickstartModel struct {
	detected           kickstart.Installation
	found              bool
	stage              kickstartStage
	choice             int
	input              textinput.Model
	list               list.Model
	spinner            spinner.Model
	catalog            kickstart.LocalizationCatalog
	answers            kickstart.Answers
	pendingArchive     kickstart.ArchiveInfo
	pendingArchivePath string
	validationProgress <-chan tea.Msg
	validationCancel   context.CancelFunc
	validationStatus   kickstart.PackageValidationPhase
	err                error
	done               bool
}

var (
	kickstartTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	kickstartHint  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	kickstartError = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

var validateKickstartPackage = kickstart.ValidatePackage

type kickstartPackageProgressMsg struct {
	phase kickstart.PackageValidationPhase
}
type kickstartPackageDoneMsg struct {
	validation kickstart.PackageValidation
	err        error
}

func runKickstartTUI(detected kickstart.Installation, found bool) (kickstart.Answers, error) {
	catalog, err := kickstart.Localizations()
	if err != nil {
		return kickstart.Answers{}, err
	}
	model := newKickstartModel(detected, found, catalog)
	result, err := tea.NewProgram(model).Run()
	if err != nil {
		return kickstart.Answers{}, err
	}
	completed := result.(kickstartModel)
	if !completed.done {
		return kickstart.Answers{}, fmt.Errorf("kickstart cancelled")
	}
	return completed.answers, nil
}

func newKickstartModel(detected kickstart.Installation, found bool, catalog kickstart.LocalizationCatalog) kickstartModel {
	activity := spinner.New()
	activity.Spinner = spinner.Dot
	model := kickstartModel{detected: detected, found: found, catalog: catalog, stage: stageInstallation, spinner: activity}
	if !found {
		model.stage = stageArchive
		model.setInput("Archive path", false)
	}
	return model
}

func (m kickstartModel) Init() tea.Cmd {
	if m.stage == stageArchive {
		return textinput.Blink
	}
	return nil
}

func (m kickstartModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case kickstartPackageProgressMsg:
		m.validationStatus = typed.phase
		return m, waitForKickstartPackageProgress(m.validationProgress)
	case kickstartPackageDoneMsg:
		if m.validationCancel != nil {
			m.validationCancel()
			m.validationCancel = nil
		}
		if typed.err != nil {
			m.stage = stageArchive
			m.setInput("Archive path", false)
			m.input.SetValue(m.pendingArchivePath)
			m.input.CursorEnd()
			m.err = typed.err
			return m, textinput.Blink
		}
		m.pendingArchive.VerifiedSHA256 = typed.validation.LocalSHA256
		m.pendingArchive.VerifiedSize = typed.validation.LocalSize
		m.answers.Installation = kickstart.Installation{Archive: &m.pendingArchive}
		m.err = nil
		m.stage = stageHome
		m.setInput("Jellyfin home", false)
		return m, textinput.Blink
	case spinner.TickMsg:
		if m.stage == stageArchiveValidation {
			var command tea.Cmd
			m.spinner, command = m.spinner.Update(typed)
			return m, command
		}
	}
	key, isKey := message.(tea.KeyMsg)
	if isKey && key.String() == "ctrl+c" {
		if m.validationCancel != nil {
			m.validationCancel()
		}
		return m, tea.Quit
	}
	if m.isSelectionStage() {
		if isKey && key.String() == "enter" && m.list.SelectedItem() != nil {
			m.acceptSelection(string(m.list.SelectedItem().(selectionItem)))
			return m, textinput.Blink
		}
		var command tea.Cmd
		m.list, command = m.list.Update(message)
		return m, command
	}
	if m.stage == stageArchiveValidation {
		return m, nil
	}
	if m.stage == stageInstallation || m.stage == stageSubmit {
		if isKey {
			switch key.String() {
			case "up", "left", "k", "shift+tab":
				m.choice = 0
			case "down", "right", "j", "tab":
				m.choice = 1
			case "enter":
				if m.stage == stageInstallation {
					if m.choice == 0 {
						m.answers.Installation = m.detected
						m.stage = stageHome
						m.setInput("Jellyfin home", false)
					} else {
						m.stage = stageArchive
						m.setInput("Archive path", false)
					}
				} else if m.choice == 0 {
					m.done = true
					return m, tea.Quit
				} else {
					return m, tea.Quit
				}
			}
		}
		return m, nil
	}
	if m.stage == stageMedia && isKey && key.String() == "ctrl+d" {
		m.stage = stageServerName
		m.setInput("Server name", false)
		return m, textinput.Blink
	}
	if isKey && key.String() == "enter" {
		if m.stage == stageArchive {
			command, err := m.beginPackageValidation(strings.TrimSpace(m.input.Value()))
			if err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			return m, tea.Batch(command, waitForKickstartPackageProgress(m.validationProgress), m.spinner.Tick)
		}
		if err := m.acceptText(); err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		return m, textinput.Blink
	}
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	return m, command
}

func (m *kickstartModel) acceptText() error {
	value := strings.TrimSpace(m.input.Value())
	switch m.stage {
	case stageHome:
		if value == "" {
			return fmt.Errorf("Jellyfin home is required")
		}
		home, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		m.answers.Home = home
		if archive := m.answers.Installation.Archive; archive != nil {
			m.answers.Installation.Executable = filepath.Join(home, "server", archive.ExecutableEntry)
			if archive.WebDirEntry != "" {
				m.answers.Installation.WebDir = filepath.Join(home, "server", archive.WebDirEntry)
			}
		}
		m.stage = stageMedia
		m.setInput("Media path", false)
	case stageMedia:
		if value == "" {
			return fmt.Errorf("enter a media path to add, or press Ctrl+D when done")
		}
		path, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		m.answers.MediaPaths = append(m.answers.MediaPaths, path)
		m.setInput("Media path", false)
	case stageServerName:
		if value == "" {
			return fmt.Errorf("server name is required")
		}
		m.answers.ServerName = value
		m.stage = stageDisplayLanguage
		m.setList("Display language", m.catalog.DisplayLanguages)
	case stageAdminPassword:
		if value == "" {
			return fmt.Errorf("administrator password is required")
		}
		m.answers.AdminPassword = value
		m.stage = stageSubmit
		m.choice = 0
	}
	return nil
}

func (m *kickstartModel) beginPackageValidation(path string) (tea.Cmd, error) {
	archive, err := kickstart.InspectArchive(path)
	if err != nil {
		return nil, err
	}
	progress := make(chan tea.Msg, 3)
	validationContext, cancel := context.WithCancel(context.Background())
	m.pendingArchive = archive
	m.pendingArchivePath = path
	m.validationProgress = progress
	m.validationCancel = cancel
	m.validationStatus = kickstart.PackageValidationConnecting
	m.stage = stageArchiveValidation
	return func() tea.Msg {
		validation, validationErr := validateKickstartPackage(validationContext, path, func(phase kickstart.PackageValidationPhase) {
			progress <- kickstartPackageProgressMsg{phase: phase}
		})
		progress <- kickstartPackageDoneMsg{validation: validation, err: validationErr}
		close(progress)
		return nil
	}, nil
}

func waitForKickstartPackageProgress(progress <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		message, ok := <-progress
		if !ok {
			return nil
		}
		return message
	}
}

func (m *kickstartModel) acceptSelection(value string) {
	switch m.stage {
	case stageDisplayLanguage:
		m.answers.DisplayLanguage = value
		m.stage = stageMetadataLanguage
		m.setList("Metadata language", m.catalog.MetadataLanguages)
	case stageMetadataLanguage:
		m.answers.MetadataLanguage = value
		m.stage = stageMetadataRegion
		m.setList("Metadata region", m.catalog.MetadataRegions)
	case stageMetadataRegion:
		m.answers.MetadataRegion = value
		m.stage = stageAdminPassword
		m.setInput("Administrator password", true)
	}
}

func (m *kickstartModel) setInput(prompt string, password bool) {
	input := textinput.New()
	input.Prompt = prompt + ": "
	input.Width = 72
	input.CharLimit = 4096
	if password {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}
	input.Focus()
	m.input = input
}

func (m *kickstartModel) setList(title string, values []string) {
	items := make([]list.Item, len(values))
	for index, value := range values {
		items[index] = selectionItem(value)
	}
	selector := list.New(items, list.NewDefaultDelegate(), 76, 20)
	selector.Title = title
	selector.SetShowHelp(true)
	selector.SetFilteringEnabled(true)
	m.list = selector
}

func (m kickstartModel) isSelectionStage() bool {
	return m.stage == stageDisplayLanguage || m.stage == stageMetadataLanguage || m.stage == stageMetadataRegion
}

func (m kickstartModel) View() string {
	var body string
	switch m.stage {
	case stageInstallation:
		body = fmt.Sprintf("Compatible Jellyfin detected:\n%s\n\nUse this installation?\n%s", m.detected.Executable, choices(m.choice, "Yes", "No"))
	case stageArchive:
		body = "Select a Generic Jellyfin .tar.gz, .tar.xz, or .zip package.\nThe executable OS and architecture will be checked before deployment.\n\n" + m.input.View()
	case stageArchiveValidation:
		body = fmt.Sprintf("Validating selected package against the official Jellyfin repository.\n\n%s %s…\n%s", m.spinner.View(), m.validationStatus, m.pendingArchivePath)
	case stageHome:
		body = "Choose Jellyfin home. Kickstart will create data, config, cache, logs, and transcode below it.\n\n" + m.input.View()
	case stageMedia:
		body = "Add zero or more media paths. Each containing physical, SMB, or NFS mount is monitored.\n\n"
		for _, path := range m.answers.MediaPaths {
			body += "  • " + path + "\n"
		}
		body += "\n" + m.input.View() + "\n" + kickstartHint.Render("Enter: Add    Ctrl+D: Done")
	case stageServerName, stageAdminPassword:
		body = m.input.View()
	case stageDisplayLanguage, stageMetadataLanguage, stageMetadataRegion:
		body = m.list.View()
	case stageSubmit:
		body = fmt.Sprintf("Ready to generate remora-config.yaml and deploy the platform service.\n\nJellyfin: %s\nHome: %s\nMedia paths: %d\nServer: %s\n\nSubmit?\n%s", m.answers.Installation.Executable, m.answers.Home, len(m.answers.MediaPaths), m.answers.ServerName, choices(m.choice, "Submit", "Cancel"))
	}
	if m.err != nil {
		body += "\n\n" + kickstartError.Render(m.err.Error())
	}
	return kickstartTitle.Render("Jellyfin Remora Kickstart") + "\n\n" + body + "\n"
}

func choices(selected int, values ...string) string {
	var rendered []string
	for index, value := range values {
		marker := "  "
		if index == selected {
			marker = "> "
		}
		rendered = append(rendered, marker+value)
	}
	return strings.Join(rendered, "\n")
}
