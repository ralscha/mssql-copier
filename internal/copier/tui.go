package copier

import (
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/brianvoe/gofakeit/v7"
)

type tuiScreen int

type tuiRunMode int

const (
	tuiScreenForm tuiScreen = iota
	tuiScreenLoadingFakeData
	tuiScreenFakeData
	tuiScreenFakerPicker
	tuiScreenFakerParams
	tuiScreenAutoSelecting
)

const (
	tuiRunModeCopy tuiRunMode = iota
	tuiRunModePlan
	tuiRunModeExportDDL
	tuiRunModeExportDDLData
)

const (
	formFieldSourceServer = iota
	formFieldSourcePort
	formFieldSourceDatabase
	formFieldSourceUser
	formFieldSourcePassword
	formFieldSourceEncrypt
	formFieldSourceTrustCert
	formFieldSourceOptions
	formFieldRunMode
	formFieldTargetType
	formFieldTargetServer
	formFieldTargetPort
	formFieldTargetDatabase
	formFieldTargetUser
	formFieldTargetPassword
	formFieldTargetEncrypt
	formFieldTargetTrustCert
	formFieldTargetOptions
	formFieldDockerDir
	formFieldDockerPort
	formFieldDockerPersistent
	formFieldDockerPassword
	formFieldWorkers
	formFieldBatchSize
	formFieldVerbose
	formFieldDropExisting
	formFieldIncludeSchemas
	formFieldExcludeSchemas
	formFieldIncludeTables
	formFieldExcludeTables
	formFieldExportDDLPath
	formFieldExportDataPath
	formFieldExportDataRows
	formFieldReportPath
	formFieldExportPath
	formFieldEditFakeData
	formFieldExportConfig
	formFieldStartCopy
	formFieldCount
)

type tuiFormState struct {
	Source         sqlServerDSNForm
	Target         sqlServerDSNForm
	RunMode        tuiRunMode
	DockerDir      string
	DockerPort     string
	Workers        string
	BatchSize      string
	IncludeSchemas string
	ExcludeSchemas string
	IncludeTables  string
	ExcludeTables  string
	ExportDDLPath  string
	ExportDataPath string
	ExportDataRows string
	ReportPath     string
	ExportPath     string
}

type tuiFakeDataEntry struct {
	Selector        string
	Display         string
	TypeName        string
	FunctionName    string
	FunctionDisplay string
	FunctionParams  []string
}

type fakeFunctionOption struct {
	LookupName  string
	Display     string
	Category    string
	Description string
	Example     string
	SearchText  string
	Params      []gofakeit.Param
}

type fakeDataLoadedMsg struct {
	entries []tuiFakeDataEntry
	err     error
}

type autoSelectDoneMsg struct {
	selections map[string]string
	err        error
}

type configExportedMsg struct {
	path string
	err  error
}

type executionFinishedMsg struct {
	logPath string
	err     error
}

var tuiExecutionMu sync.Mutex

var runTUIExecution = executeTUIExecution

type tuiModel struct {
	cfg               config
	form              tuiFormState
	screen            tuiScreen
	formFocus         int
	status            string
	width             int
	height            int
	fakeDataEntries   []tuiFakeDataEntry
	fakeDataCursor    int
	fakeDataOffset    int
	pickerTarget      int
	pickerCursor      int
	pickerOffset      int
	pickerQuery       string
	paramTarget       int
	paramInput        string
	paramOption       fakeFunctionOption
	fakeFunctions     []fakeFunctionOption
	preservedFakeData map[string]string
	runInProgress     bool
	currentLogPath    string
	recentLogPaths    []string
	quitting          bool
	logTailLines      []string
	logTailScroll     int
	logPanelFocused   bool
}

func runTUI(cfg config) error {
	program := tea.NewProgram(newTUIModel(cfg))
	_, err := program.Run()
	return err
}

func newTUIModel(cfg config) tuiModel {
	exportPath := strings.TrimSpace(cfg.ConfigPath)
	if exportPath == "" {
		exportPath = defaultConfigPath
	}
	dockerPortStr := ""
	if cfg.Docker.Port > 0 {
		dockerPortStr = strconv.Itoa(cfg.Docker.Port)
	}
	return tuiModel{
		cfg: cfg,
		form: tuiFormState{
			Source:         parseSQLServerDSNForm(cfg.SourceDSN),
			Target:         parseSQLServerDSNForm(cfg.TargetDSN),
			RunMode:        initialTUIRunMode(cfg),
			DockerDir:      cfg.Docker.ComposeDir,
			DockerPort:     dockerPortStr,
			Workers:        strconv.Itoa(max(1, cfg.Workers)),
			BatchSize:      strconv.Itoa(max(1, cfg.BatchSize)),
			IncludeSchemas: strings.Join(cfg.IncludeSchemas, ","),
			ExcludeSchemas: strings.Join(cfg.ExcludeSchemas, ","),
			IncludeTables:  strings.Join(cfg.IncludeTables, ","),
			ExcludeTables:  strings.Join(cfg.ExcludeTables, ","),
			ExportDDLPath:  strings.TrimSpace(cfg.ExportDDLFile),
			ExportDataPath: strings.TrimSpace(cfg.ExportDataFile),
			ExportDataRows: strconv.Itoa(max(0, cfg.ExportDataRows)),
			ReportPath:     strings.TrimSpace(cfg.ReportMDFile),
			ExportPath:     exportPath,
		},
		screen:            tuiScreenForm,
		formFocus:         formFieldSourceServer,
		width:             100,
		height:            30,
		fakeFunctions:     availableFakeFunctionOptions(),
		preservedFakeData: preserveNonFullFakeData(cfg.FakeData),
		recentLogPaths:    listRecentTUILogPaths(exportPath, 5),
	}
}

func (m tuiModel) Init() tea.Cmd {
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case fakeDataLoadedMsg:
		if msg.err != nil {
			m.screen = tuiScreenForm
			m.status = msg.err.Error()
			return m, nil
		}
		m.screen = tuiScreenFakeData
		m.fakeDataEntries = msg.entries
		m.fakeDataCursor = 0
		m.fakeDataOffset = 0
		if len(msg.entries) == 0 {
			m.status = "No copyable source columns were found."
		} else {
			m.status = fmt.Sprintf("Loaded %d source columns for fake-data editing.", len(msg.entries))
		}
		return m, nil
	case autoSelectDoneMsg:
		m.screen = tuiScreenFakeData
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		applied := 0
		byName := make(map[string]fakeFunctionOption, len(m.fakeFunctions))
		for _, option := range m.fakeFunctions {
			byName[option.LookupName] = option
		}
		for index, entry := range m.fakeDataEntries {
			name, ok := msg.selections[entry.Selector]
			if !ok {
				continue
			}
			option, ok := byName[name]
			if !ok {
				continue
			}
			m.fakeDataEntries[index].FunctionName = option.LookupName
			m.fakeDataEntries[index].FunctionDisplay = option.Display
			m.fakeDataEntries[index].FunctionParams = nil
			applied++
		}
		m.status = fmt.Sprintf("Applied %d LLM faker suggestions.", applied)
		return m, nil
	case configExportedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("Exported config to %s.", msg.path)
		return m, nil
	case executionFinishedMsg:
		m.runInProgress = false
		m.recentLogPaths = listRecentTUILogPaths(m.form.ExportPath, 5)
		m.loadLogTail()
		if msg.err != nil {
			m.status = fmt.Sprintf("Action failed. See log: %s", msg.logPath)
			if strings.TrimSpace(msg.logPath) == "" {
				m.status = msg.err.Error()
			}
			return m, nil
		}
		if strings.TrimSpace(msg.logPath) != "" {
			m.status = fmt.Sprintf("Action finished. Log file: %s", msg.logPath)
		} else {
			m.status = "Action finished."
		}
		return m, nil
	case tea.PasteMsg:
		if m.quitting {
			return m, nil
		}
		switch m.screen {
		case tuiScreenForm:
			if m.isFormFieldTextInput(m.formFocus) {
				m.appendFormText(msg.Content)
			}
		case tuiScreenLoadingFakeData, tuiScreenFakeData, tuiScreenAutoSelecting:
		case tuiScreenFakerPicker:
			m.pickerQuery += msg.Content
			m.pickerCursor = 0
			m.pickerOffset = 0
		case tuiScreenFakerParams:
			m.paramInput += msg.Content
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.quitting {
			return m, nil
		}
		switch m.screen {
		case tuiScreenForm:
			return m.updateForm(msg)
		case tuiScreenLoadingFakeData, tuiScreenAutoSelecting:
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		case tuiScreenFakeData:
			return m.updateFakeData(msg)
		case tuiScreenFakerPicker:
			return m.updateFakerPicker(msg)
		case tuiScreenFakerParams:
			return m.updateFakerParams(msg)
		}
	}
	return m, nil
}

func (m tuiModel) View() tea.View {
	var builder strings.Builder
	builder.WriteString("mssql-copier TUI\n\n")
	builder.WriteString(m.statusLine())
	builder.WriteString("\n\n")

	switch m.screen {
	case tuiScreenForm:
		builder.WriteString(m.formView())
	case tuiScreenLoadingFakeData:
		builder.WriteString("Loading source schema metadata for fake-data editing...\n")
		builder.WriteString("Press ctrl+c or q to cancel the program.\n")
	case tuiScreenFakeData:
		builder.WriteString(m.fakeDataView())
	case tuiScreenFakerPicker:
		builder.WriteString(m.fakerPickerView())
	case tuiScreenFakerParams:
		builder.WriteString(m.fakerParamsView())
	case tuiScreenAutoSelecting:
		builder.WriteString("Analyzing columns with the configured LLM and pre-selecting fake-data functions...\n")
		builder.WriteString("Press ctrl+c or q to cancel the program.\n")
	}

	view := tea.NewView(builder.String())
	view.AltScreen = true
	return view
}

func (m tuiModel) statusLine() string {
	if strings.TrimSpace(m.status) == "" {
		return "Status: ready"
	}
	return "Status: " + m.status
}

func (m tuiModel) formView() string {
	type formRow struct {
		field int
		text  string
	}

	targetTypeLabel := "local (enter to switch to docker)"
	if m.cfg.Docker.Enabled {
		targetTypeLabel = "docker (enter to switch to local)"
	}
	runModeLabel := m.form.RunMode.label()
	rows := []formRow{
		{field: formFieldSourceServer, text: m.formTextRow(formFieldSourceServer, "Source server", m.form.Source.Server)},
		{field: formFieldSourcePort, text: m.formTextRow(formFieldSourcePort, "Source port", m.form.Source.Port)},
		{field: formFieldSourceDatabase, text: m.formTextRow(formFieldSourceDatabase, "Source database", m.form.Source.Database)},
		{field: formFieldSourceUser, text: m.formTextRow(formFieldSourceUser, "Source user", m.form.Source.Username)},
		{field: formFieldSourcePassword, text: m.formTextRow(formFieldSourcePassword, "Source password", m.form.Source.Password)},
		{field: formFieldSourceEncrypt, text: m.formTextRow(formFieldSourceEncrypt, "Source encrypt", m.form.Source.Encrypt)},
		{field: formFieldSourceTrustCert, text: m.formTextRow(formFieldSourceTrustCert, "Source trust cert", m.form.Source.TrustServerCertificate)},
		{field: formFieldSourceOptions, text: m.formTextRow(formFieldSourceOptions, "Source options", m.form.Source.Options)},
		{field: formFieldRunMode, text: m.formActionRow(formFieldRunMode, "Run mode: "+runModeLabel)},
		{field: formFieldTargetType, text: m.formTextRow(formFieldTargetType, "Target type", targetTypeLabel)},
	}

	if m.cfg.Docker.Enabled {
		dockerDir := m.form.DockerDir
		if dockerDir == "" {
			dockerDir = defaultDockerDir
		}
		dockerPort := m.form.DockerPort
		if dockerPort == "" {
			dockerPort = strconv.Itoa(defaultDockerPort)
		}
		saPasswordDisplay := m.cfg.Docker.SAPassword
		if saPasswordDisplay == "" {
			saPasswordDisplay = "<not generated>"
		}
		rows = append(rows,
			formRow{field: formFieldDockerDir, text: m.formTextRow(formFieldDockerDir, "Compose dir", dockerDir)},
			formRow{field: formFieldDockerPort, text: m.formTextRow(formFieldDockerPort, "Docker port", dockerPort)},
			formRow{field: formFieldDockerPersistent, text: m.formBoolRow(formFieldDockerPersistent, "Persistent", m.cfg.Docker.Persistent)},
			formRow{field: formFieldDockerPassword, text: m.formTextRow(formFieldDockerPassword, "SA password", saPasswordDisplay+" (enter to regenerate)")},
		)
	} else {
		rows = append(rows,
			formRow{field: formFieldTargetServer, text: m.formTextRow(formFieldTargetServer, "Target server", m.form.Target.Server)},
			formRow{field: formFieldTargetPort, text: m.formTextRow(formFieldTargetPort, "Target port", m.form.Target.Port)},
			formRow{field: formFieldTargetDatabase, text: m.formTextRow(formFieldTargetDatabase, "Target database", m.form.Target.Database)},
			formRow{field: formFieldTargetUser, text: m.formTextRow(formFieldTargetUser, "Target user", m.form.Target.Username)},
			formRow{field: formFieldTargetPassword, text: m.formTextRow(formFieldTargetPassword, "Target password", m.form.Target.Password)},
			formRow{field: formFieldTargetEncrypt, text: m.formTextRow(formFieldTargetEncrypt, "Target encrypt", m.form.Target.Encrypt)},
			formRow{field: formFieldTargetTrustCert, text: m.formTextRow(formFieldTargetTrustCert, "Target trust cert", m.form.Target.TrustServerCertificate)},
			formRow{field: formFieldTargetOptions, text: m.formTextRow(formFieldTargetOptions, "Target options", m.form.Target.Options)},
		)
	}

	rows = append(rows,
		formRow{field: formFieldWorkers, text: m.formTextRow(formFieldWorkers, "Workers", m.form.Workers)},
		formRow{field: formFieldBatchSize, text: m.formTextRow(formFieldBatchSize, "Batch size", m.form.BatchSize)},
		formRow{field: formFieldVerbose, text: m.formBoolRow(formFieldVerbose, "Verbose", m.cfg.Verbose)},
		formRow{field: formFieldDropExisting, text: m.formBoolRow(formFieldDropExisting, "Drop existing", m.cfg.DropExisting)},
		formRow{field: formFieldIncludeSchemas, text: m.formTextRow(formFieldIncludeSchemas, "Include schemas", m.form.IncludeSchemas)},
		formRow{field: formFieldExcludeSchemas, text: m.formTextRow(formFieldExcludeSchemas, "Exclude schemas", m.form.ExcludeSchemas)},
		formRow{field: formFieldIncludeTables, text: m.formTextRow(formFieldIncludeTables, "Include tables", m.form.IncludeTables)},
		formRow{field: formFieldExcludeTables, text: m.formTextRow(formFieldExcludeTables, "Exclude tables", m.form.ExcludeTables)},
		formRow{field: formFieldExportDDLPath, text: m.formTextRow(formFieldExportDDLPath, "DDL export path", m.form.ExportDDLPath)},
		formRow{field: formFieldExportDataPath, text: m.formTextRow(formFieldExportDataPath, "Data export path", m.form.ExportDataPath)},
		formRow{field: formFieldExportDataRows, text: m.formTextRow(formFieldExportDataRows, "Export data rows", m.form.ExportDataRows)},
		formRow{field: formFieldReportPath, text: m.formTextRow(formFieldReportPath, "Report path", m.form.ReportPath)},
		formRow{field: formFieldExportPath, text: m.formTextRow(formFieldExportPath, "Config path", m.form.ExportPath)},
		formRow{field: formFieldEditFakeData, text: m.formActionRow(formFieldEditFakeData, fmt.Sprintf("Edit fake data (%d exact rules)", countExactFullFakeDataRules(m.cfg.FakeData)))},
		formRow{field: formFieldExportConfig, text: m.formActionRow(formFieldExportConfig, "Export YAML config")},
		formRow{field: formFieldStartCopy, text: m.formActionRow(formFieldStartCopy, m.form.RunMode.submitLabel())},
	)

	visibleRows := make([]string, 0, len(rows))
	for _, row := range rows {
		if m.isFormFieldVisible(row.field) {
			visibleRows = append(visibleRows, row.text)
		}
	}

	sections := []string{
		"Enter source settings, choose a run mode, and then adjust only the settings used by that mode.",
		"Keys: type to edit, backspace deletes, up/down or tab moves, enter toggles/actions, ctrl+c quits, ctrl+l focuses log panel.",
		"",
		strings.Join(visibleRows, "\n"),
		"",
		m.executionView(),
		"",
		m.logFilesView(),
		"",
		m.logTailView(),
	}
	return strings.Join(sections, "\n")
}

func (m tuiModel) executionView() string {
	rows := []string{"Action"}
	if m.runInProgress {
		rows = append(rows,
			fmt.Sprintf("  State: running %s", m.form.RunMode.label()),
			fmt.Sprintf("  Active log: %s", m.currentLogPath),
			"  Start is disabled until the current action completes.",
		)
		return strings.Join(rows, "\n")
	}
	rows = append(rows, "  State: idle")
	if strings.TrimSpace(m.currentLogPath) != "" {
		rows = append(rows, fmt.Sprintf("  Last log: %s", m.currentLogPath))
	}
	return strings.Join(rows, "\n")
}

func (m tuiModel) logFilesView() string {
	rows := []string{"Log files"}
	logPaths := m.visibleLogPaths()
	if len(logPaths) == 0 {
		rows = append(rows, "  No TUI log files yet. Start an action to create one.")
		return strings.Join(rows, "\n")
	}
	for _, logPath := range logPaths {
		rows = append(rows, "  - "+logPath)
	}
	return strings.Join(rows, "\n")
}

func (m tuiModel) visibleLogPaths() []string {
	paths := make([]string, 0, len(m.recentLogPaths)+1)
	seen := make(map[string]struct{}, len(m.recentLogPaths)+1)
	if current := strings.TrimSpace(m.currentLogPath); current != "" {
		paths = append(paths, current)
		seen[current] = struct{}{}
	}
	for _, path := range m.recentLogPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		paths = append(paths, path)
		seen[path] = struct{}{}
	}
	return paths
}

func (m *tuiModel) loadLogTail() {
	path := m.bestLogPathForTail()
	if path == "" {
		m.logTailLines = nil
		m.logTailScroll = 0
		return
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		m.logTailLines = []string{fmt.Sprintf("(cannot read log: %v)", err)}
		m.logTailScroll = 0
		return
	}
	lines := strings.Split(string(data), "\n")
	// Trim trailing empty line from split.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	m.logTailLines = lines
	m.logTailScroll = 0
}

func (m tuiModel) bestLogPathForTail() string {
	if current := strings.TrimSpace(m.currentLogPath); current != "" {
		return current
	}
	if len(m.recentLogPaths) > 0 {
		return strings.TrimSpace(m.recentLogPaths[0])
	}
	return ""
}

func (m *tuiModel) scrollLogTail(delta int) {
	if len(m.logTailLines) == 0 {
		return
	}
	m.logTailScroll += delta
	m.clampLogTailScroll()
}

func (m *tuiModel) clampLogTailScroll() {
	if len(m.logTailLines) == 0 {
		m.logTailScroll = 0
		return
	}
	visible := m.visibleLogTailRows()
	maxScroll := max(0, len(m.logTailLines)-visible)
	if m.logTailScroll > maxScroll {
		m.logTailScroll = maxScroll
	}
	if m.logTailScroll < 0 {
		m.logTailScroll = 0
	}
}

func (m tuiModel) visibleLogTailRows() int {
	// Reserve at least 3 lines for the panel header and border, plus 2 for bottom margin.
	return max(3, m.height-m.configLinesEstimate()-5)
}

func (m tuiModel) configLinesEstimate() int {
	// Rough estimate: header (2) + instructions (2) + blank (1) + fields (~30 visible fields) + blank (1) + execution (4) + blank (1) + log files (3)
	visibleFields := 0
	allFields := []int{
		formFieldSourceServer, formFieldSourcePort, formFieldSourceDatabase,
		formFieldSourceUser, formFieldSourcePassword, formFieldSourceEncrypt,
		formFieldSourceTrustCert, formFieldSourceOptions,
		formFieldRunMode,
		formFieldTargetType,
		formFieldTargetServer, formFieldTargetPort, formFieldTargetDatabase,
		formFieldTargetUser, formFieldTargetPassword, formFieldTargetEncrypt,
		formFieldTargetTrustCert, formFieldTargetOptions,
		formFieldDockerDir, formFieldDockerPort, formFieldDockerPersistent, formFieldDockerPassword,
		formFieldWorkers, formFieldBatchSize, formFieldVerbose,
		formFieldDropExisting,
		formFieldIncludeSchemas, formFieldExcludeSchemas,
		formFieldIncludeTables, formFieldExcludeTables,
		formFieldExportDDLPath, formFieldExportDataPath, formFieldExportDataRows,
		formFieldReportPath, formFieldExportPath,
		formFieldEditFakeData, formFieldExportConfig, formFieldStartCopy,
	}
	for _, field := range allFields {
		if m.isFormFieldVisible(field) {
			visibleFields++
		}
	}
	// Header + instructions + blank lines + fields + execution section + log files section
	return 2 + 2 + 1 + visibleFields + 1 + 4 + 1 + 3
}

func (m tuiModel) logTailView() string {
	var builder strings.Builder
	builder.WriteString(strings.Repeat("─", max(1, m.width-2)))
	builder.WriteString("\n")

	if m.logPanelFocused {
		builder.WriteString("▶ Log tail (scroll with arrows / pgup / pgdn, esc to return to config)")
	} else {
		builder.WriteString("  Log tail (press ctrl+l to focus)")
	}
	if path := m.bestLogPathForTail(); path != "" {
		builder.WriteString(" — ")
		builder.WriteString(filepath.Base(path))
	}
	builder.WriteString("\n")
	builder.WriteString(strings.Repeat("─", max(1, m.width-2)))
	builder.WriteString("\n")

	if len(m.logTailLines) == 0 {
		builder.WriteString("  No log content yet. Start an action to see output here.\n")
		return builder.String()
	}

	visible := m.visibleLogTailRows()
	start := m.logTailScroll
	end := min(len(m.logTailLines), start+visible)
	for i := start; i < end; i++ {
		line := m.logTailLines[i]
		// Truncate lines that are too wide.
		maxLineWidth := max(1, m.width-2)
		if len(line) > maxLineWidth {
			line = line[:maxLineWidth]
		}
		builder.WriteString(" ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return builder.String()
}

func (m tuiModel) formTextRow(index int, label string, value string) string {
	prefix := "  "
	if m.formFocus == index {
		prefix = "> "
	}
	return fmt.Sprintf("%s%-20s %s", prefix, label+":", value)
}

func (m tuiModel) formBoolRow(index int, label string, value bool) string {
	state := "no"
	if value {
		state = "yes"
	}
	return m.formTextRow(index, label, state)
}

func (m tuiModel) formActionRow(index int, label string) string {
	marker := "  "
	if m.formFocus == index {
		marker = "> "
	}
	return marker + "[ " + label + " ] "
}

func (m tuiModel) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.logPanelFocused {
		return m.updateLogPanelFocus(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		if m.runInProgress {
			m.status = "Wait for the current action to finish before quitting."
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "ctrl+l":
		m.logPanelFocused = true
		m.loadLogTail()
		m.status = "Log panel focused. Use arrows/pgup/pgdn to scroll, esc to return to config."
		return m, nil
	case "tab", "down":
		m.formFocus = m.nextFormField()
		return m, nil
	case "shift+tab", "up":
		m.formFocus = m.prevFormField()
		return m, nil
	case "enter":
		return m.handleFormEnter()
	case "backspace":
		m.deleteFormText()
		return m, nil
	}

	if msg.Key().Text != "" {
		m.appendFormText(msg.Key().Text)
	}
	return m, nil
}

func (m tuiModel) updateLogPanelFocus(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.runInProgress {
			m.status = "Wait for the current action to finish before quitting."
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "esc", "ctrl+l":
		m.logPanelFocused = false
		m.status = "Returned to config form."
		return m, nil
	case "up":
		m.scrollLogTail(-1)
	case "down":
		m.scrollLogTail(1)
	case "pgup":
		m.scrollLogTail(-max(1, m.visibleLogTailRows()/2))
	case "pgdown":
		m.scrollLogTail(max(1, m.visibleLogTailRows()/2))
	case "home":
		m.logTailScroll = 0
	case "end":
		m.logTailScroll = max(0, len(m.logTailLines)-m.visibleLogTailRows())
	}
	return m, nil
}

func (m tuiModel) isFormFieldTextInput(field int) bool {
	switch field {
	case formFieldRunMode, formFieldTargetType,
		formFieldDockerPersistent, formFieldDockerPassword,
		formFieldVerbose, formFieldDropExisting,
		formFieldEditFakeData, formFieldExportConfig, formFieldStartCopy:
		return false
	default:
		return true
	}
}

func (m tuiModel) isFormFieldVisible(field int) bool {
	runMode := m.form.RunMode
	switch field {
	case formFieldTargetType:
		return runMode.showsTargetSettings()
	case formFieldTargetServer, formFieldTargetPort, formFieldTargetDatabase, formFieldTargetUser, formFieldTargetPassword, formFieldTargetEncrypt, formFieldTargetTrustCert, formFieldTargetOptions:
		return runMode.showsTargetSettings() && !m.cfg.Docker.Enabled
	case formFieldDockerDir, formFieldDockerPort, formFieldDockerPersistent, formFieldDockerPassword:
		return runMode.showsTargetSettings() && m.cfg.Docker.Enabled
	case formFieldWorkers, formFieldBatchSize, formFieldVerbose, formFieldReportPath:
		return runMode.showsCopyExecutionSettings()
	case formFieldDropExisting:
		return runMode.allowsDropExisting()
	case formFieldExportDDLPath:
		return runMode == tuiRunModeExportDDL || runMode == tuiRunModeExportDDLData
	case formFieldExportDataPath, formFieldExportDataRows:
		return runMode == tuiRunModeExportDDLData
	case formFieldEditFakeData:
		return runMode.allowsFakeData()
	default:
		return true
	}
}

func (m tuiModel) nextFormField() int {
	next := m.formFocus
	for {
		next = (next + 1) % formFieldCount
		if m.isFormFieldVisible(next) {
			return next
		}
	}
}

func (m tuiModel) prevFormField() int {
	prev := m.formFocus
	for {
		prev = (prev + formFieldCount - 1) % formFieldCount
		if m.isFormFieldVisible(prev) {
			return prev
		}
	}
}

func (m tuiModel) handleFormEnter() (tea.Model, tea.Cmd) {
	switch m.formFocus {
	case formFieldRunMode:
		m.form.RunMode = m.form.RunMode.next()
		if !m.form.RunMode.showsTargetSettings() {
			m.cfg.Docker.Enabled = false
		}
	case formFieldTargetType:
		m.cfg.Docker.Enabled = !m.cfg.Docker.Enabled
		if m.cfg.Docker.Enabled && m.cfg.Docker.SAPassword == "" {
			pw, err := randomSAPassword()
			if err != nil {
				m.status = "Failed to generate SA password: " + err.Error()
				m.cfg.Docker.Enabled = false
			} else {
				m.cfg.Docker.SAPassword = pw
				m.status = "Docker target enabled. SA password generated."
			}
		}
	case formFieldDockerPersistent:
		m.cfg.Docker.Persistent = !m.cfg.Docker.Persistent
	case formFieldDockerPassword:
		pw, err := randomSAPassword()
		if err != nil {
			m.status = "Failed to regenerate SA password: " + err.Error()
		} else {
			m.cfg.Docker.SAPassword = pw
			m.status = "Generated a new SA password."
		}
	case formFieldVerbose:
		m.cfg.Verbose = !m.cfg.Verbose
	case formFieldDropExisting:
		m.cfg.DropExisting = !m.cfg.DropExisting
	case formFieldEditFakeData:
		cfg, err := m.configFromForm(true, false)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.cfg = cfg
		if strings.TrimSpace(m.cfg.SourceDSN) == "" {
			m.status = "Source DSN is required before loading schema metadata."
			return m, nil
		}
		m.screen = tuiScreenLoadingFakeData
		m.status = "Connecting to the source to discover schema metadata..."
		return m, loadFakeDataEntriesCmd(m.cfg)
	case formFieldExportConfig:
		cfg, err := m.configFromForm(false, false)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.cfg = cfg
		m.syncFakeDataIntoConfig()
		m.status = "Writing YAML config to disk..."
		return m, exportConfigCmd(m.cfg)
	case formFieldStartCopy:
		if m.runInProgress {
			m.status = "An action is already running."
			return m, nil
		}
		configs, err := m.executionConfigs()
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.cfg = configs[0]
		m.currentLogPath = nextTUILogPath(m.form.ExportPath, m.form.RunMode)
		m.recentLogPaths = prependLogPath(m.recentLogPaths, m.currentLogPath, 5)
		m.runInProgress = true
		m.status = fmt.Sprintf("Running %s. Writing logs to %s", m.form.RunMode.label(), m.currentLogPath)
		return m, startTUIExecutionCmd(configs, m.currentLogPath)
	}
	return m, nil
}

func prependLogPath(existing []string, logPath string, limit int) []string {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return existing
	}
	paths := []string{logPath}
	for _, existingPath := range existing {
		existingPath = strings.TrimSpace(existingPath)
		if existingPath == "" || existingPath == logPath {
			continue
		}
		paths = append(paths, existingPath)
		if len(paths) == limit {
			return paths
		}
	}
	return paths
}

func (m *tuiModel) appendFormText(text string) {
	switch m.formFocus {
	case formFieldSourceServer:
		m.form.Source.Server += text
	case formFieldSourcePort:
		m.form.Source.Port += text
	case formFieldSourceDatabase:
		m.form.Source.Database += text
	case formFieldSourceUser:
		m.form.Source.Username += text
	case formFieldSourcePassword:
		m.form.Source.Password += text
	case formFieldSourceEncrypt:
		m.form.Source.Encrypt += text
	case formFieldSourceTrustCert:
		m.form.Source.TrustServerCertificate += text
	case formFieldSourceOptions:
		m.form.Source.Options += text
	case formFieldTargetServer:
		m.form.Target.Server += text
	case formFieldTargetPort:
		m.form.Target.Port += text
	case formFieldTargetDatabase:
		m.form.Target.Database += text
	case formFieldTargetUser:
		m.form.Target.Username += text
	case formFieldTargetPassword:
		m.form.Target.Password += text
	case formFieldTargetEncrypt:
		m.form.Target.Encrypt += text
	case formFieldTargetTrustCert:
		m.form.Target.TrustServerCertificate += text
	case formFieldTargetOptions:
		m.form.Target.Options += text
	case formFieldDockerDir:
		m.form.DockerDir += text
	case formFieldDockerPort:
		m.form.DockerPort += text
	case formFieldWorkers:
		m.form.Workers += text
	case formFieldBatchSize:
		m.form.BatchSize += text
	case formFieldIncludeSchemas:
		m.form.IncludeSchemas += text
	case formFieldExcludeSchemas:
		m.form.ExcludeSchemas += text
	case formFieldIncludeTables:
		m.form.IncludeTables += text
	case formFieldExcludeTables:
		m.form.ExcludeTables += text
	case formFieldExportDDLPath:
		m.form.ExportDDLPath += text
	case formFieldExportDataPath:
		m.form.ExportDataPath += text
	case formFieldExportDataRows:
		m.form.ExportDataRows += text
	case formFieldReportPath:
		m.form.ReportPath += text
	case formFieldExportPath:
		m.form.ExportPath += text
	}
	m.status = ""
}

func (m *tuiModel) deleteFormText() {
	switch m.formFocus {
	case formFieldSourceServer:
		m.form.Source.Server = trimLastRune(m.form.Source.Server)
	case formFieldSourcePort:
		m.form.Source.Port = trimLastRune(m.form.Source.Port)
	case formFieldSourceDatabase:
		m.form.Source.Database = trimLastRune(m.form.Source.Database)
	case formFieldSourceUser:
		m.form.Source.Username = trimLastRune(m.form.Source.Username)
	case formFieldSourcePassword:
		m.form.Source.Password = trimLastRune(m.form.Source.Password)
	case formFieldSourceEncrypt:
		m.form.Source.Encrypt = trimLastRune(m.form.Source.Encrypt)
	case formFieldSourceTrustCert:
		m.form.Source.TrustServerCertificate = trimLastRune(m.form.Source.TrustServerCertificate)
	case formFieldSourceOptions:
		m.form.Source.Options = trimLastRune(m.form.Source.Options)
	case formFieldTargetServer:
		m.form.Target.Server = trimLastRune(m.form.Target.Server)
	case formFieldTargetPort:
		m.form.Target.Port = trimLastRune(m.form.Target.Port)
	case formFieldTargetDatabase:
		m.form.Target.Database = trimLastRune(m.form.Target.Database)
	case formFieldTargetUser:
		m.form.Target.Username = trimLastRune(m.form.Target.Username)
	case formFieldTargetPassword:
		m.form.Target.Password = trimLastRune(m.form.Target.Password)
	case formFieldTargetEncrypt:
		m.form.Target.Encrypt = trimLastRune(m.form.Target.Encrypt)
	case formFieldTargetTrustCert:
		m.form.Target.TrustServerCertificate = trimLastRune(m.form.Target.TrustServerCertificate)
	case formFieldTargetOptions:
		m.form.Target.Options = trimLastRune(m.form.Target.Options)
	case formFieldDockerDir:
		m.form.DockerDir = trimLastRune(m.form.DockerDir)
	case formFieldDockerPort:
		m.form.DockerPort = trimLastRune(m.form.DockerPort)
	case formFieldWorkers:
		m.form.Workers = trimLastRune(m.form.Workers)
	case formFieldBatchSize:
		m.form.BatchSize = trimLastRune(m.form.BatchSize)
	case formFieldIncludeSchemas:
		m.form.IncludeSchemas = trimLastRune(m.form.IncludeSchemas)
	case formFieldExcludeSchemas:
		m.form.ExcludeSchemas = trimLastRune(m.form.ExcludeSchemas)
	case formFieldIncludeTables:
		m.form.IncludeTables = trimLastRune(m.form.IncludeTables)
	case formFieldExcludeTables:
		m.form.ExcludeTables = trimLastRune(m.form.ExcludeTables)
	case formFieldExportDDLPath:
		m.form.ExportDDLPath = trimLastRune(m.form.ExportDDLPath)
	case formFieldExportDataPath:
		m.form.ExportDataPath = trimLastRune(m.form.ExportDataPath)
	case formFieldExportDataRows:
		m.form.ExportDataRows = trimLastRune(m.form.ExportDataRows)
	case formFieldReportPath:
		m.form.ReportPath = trimLastRune(m.form.ReportPath)
	case formFieldExportPath:
		m.form.ExportPath = trimLastRune(m.form.ExportPath)
	}
}

func (m tuiModel) configFromForm(requireSource bool, requireTarget bool) (config, error) {
	cfg := m.cfg
	runMode := m.form.RunMode
	sourceDSN, err := buildSQLServerDSN(m.form.Source)
	if err != nil {
		return config{}, fmt.Errorf("source dsn: %w", err)
	}
	cfg.SourceDSN = sourceDSN
	cfg.IncludeSchemas = parseList(m.form.IncludeSchemas)
	cfg.ExcludeSchemas = parseList(m.form.ExcludeSchemas)
	cfg.IncludeTables = parseList(m.form.IncludeTables)
	cfg.ExcludeTables = parseList(m.form.ExcludeTables)
	cfg.ConfigPath = strings.TrimSpace(m.form.ExportPath)
	cfg.Plan = runMode == tuiRunModePlan
	cfg.ReportMDFile = ""
	if runMode == tuiRunModeCopy {
		cfg.ReportMDFile = strings.TrimSpace(m.form.ReportPath)
	}
	cfg.DropExisting = cfg.DropExisting && runMode.allowsDropExisting()
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = defaultConfigPath
	}
	workers, err := parsePositiveIntField(m.form.Workers, 1)
	if err != nil {
		return config{}, fmt.Errorf("workers: %w", err)
	}
	batchSize, err := parsePositiveIntField(m.form.BatchSize, 5000)
	if err != nil {
		return config{}, fmt.Errorf("batch size: %w", err)
	}
	exportDataRows, err := parseNonNegativeIntField(m.form.ExportDataRows, 0)
	if err != nil {
		return config{}, fmt.Errorf("export data rows: %w", err)
	}
	cfg.Workers = workers
	cfg.BatchSize = batchSize
	if runMode == tuiRunModeExportDDLData {
		cfg.ExportDataRows = exportDataRows
	} else {
		cfg.ExportDataRows = 0
	}
	cfg.ExportDDLFile = ""
	cfg.ExportDataFile = ""
	if cachedFakeData, found, cacheErr := loadCachedFakeDataMappings(cfg.SourceDSN); cacheErr != nil {
		return config{}, cacheErr
	} else if found {
		cfg.FakeData = cachedFakeData
	} else {
		cfg.FakeData = nil
	}
	if requireSource && cfg.SourceDSN == "" {
		return config{}, fmt.Errorf("source DSN is required")
	}
	if !runMode.showsTargetSettings() {
		cfg.TargetDSN = ""
		cfg.Docker.Enabled = false
		return cfg, nil
	}
	if cfg.Docker.Enabled {
		cfg.TargetDSN = ""
		cfg.Docker.ComposeDir = strings.TrimSpace(m.form.DockerDir)
		dockerPort, portErr := parsePositiveIntField(m.form.DockerPort, defaultDockerPort)
		if portErr != nil {
			return config{}, fmt.Errorf("docker port: %w", portErr)
		}
		cfg.Docker.Port = dockerPort
		if requireTarget && cfg.Docker.SAPassword == "" {
			return config{}, fmt.Errorf("docker SA password is not set; toggle the target type to generate one")
		}
	} else {
		targetDSN, buildErr := buildSQLServerDSN(m.form.Target)
		if buildErr != nil {
			return config{}, fmt.Errorf("target dsn: %w", buildErr)
		}
		cfg.TargetDSN = targetDSN
		if requireTarget && cfg.TargetDSN == "" {
			return config{}, fmt.Errorf("target DSN is required")
		}
		if requireTarget && !isLocalTargetDSN(cfg.TargetDSN) {
			return config{}, fmt.Errorf("target DSN must point to a local address when target type is local")
		}
	}
	return cfg, nil
}

func (m tuiModel) executionConfigs() ([]config, error) {
	requireTarget := m.form.RunMode == tuiRunModeCopy
	baseCfg, err := m.configFromForm(true, requireTarget)
	if err != nil {
		return nil, err
	}

	switch m.form.RunMode {
	case tuiRunModeCopy:
		return []config{baseCfg}, nil
	case tuiRunModePlan:
		return []config{baseCfg}, nil
	case tuiRunModeExportDDL:
		ddlPath := strings.TrimSpace(m.form.ExportDDLPath)
		if ddlPath == "" {
			return nil, fmt.Errorf("ddl export path is required")
		}
		baseCfg.ExportDDLFile = ddlPath
		return []config{baseCfg}, nil
	case tuiRunModeExportDDLData:
		ddlPath := strings.TrimSpace(m.form.ExportDDLPath)
		if ddlPath == "" {
			return nil, fmt.Errorf("ddl export path is required")
		}
		dataPath := strings.TrimSpace(m.form.ExportDataPath)
		if dataPath == "" {
			return nil, fmt.Errorf("data export path is required")
		}
		ddlCfg := baseCfg
		ddlCfg.ExportDDLFile = ddlPath
		ddlCfg.ExportDataRows = 0
		dataCfg := baseCfg
		dataCfg.ExportDataFile = dataPath
		return []config{ddlCfg, dataCfg}, nil
	default:
		return nil, fmt.Errorf("unsupported tui run mode")
	}
}

func (m tuiModel) updateFakeData(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	count := len(m.fakeDataEntries)
	if count == 0 {
		switch msg.String() {
		case "esc", "enter":
			m.screen = tuiScreenForm
			return m, nil
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "q", "esc":
		m.leaveFakeDataEditor()
		return m, nil
	case "down":
		if m.fakeDataCursor < count-1 {
			m.fakeDataCursor++
		}
	case "up":
		if m.fakeDataCursor > 0 {
			m.fakeDataCursor--
		}
	case "pgdown":
		m.fakeDataCursor = min(count-1, m.fakeDataCursor+m.visibleFakeDataRows())
	case "pgup":
		m.fakeDataCursor = max(0, m.fakeDataCursor-m.visibleFakeDataRows())
	case "enter":
		m.screen = tuiScreenFakerPicker
		m.pickerTarget = m.fakeDataCursor
		m.pickerCursor = 0
		m.pickerOffset = 0
		m.pickerQuery = ""
		m.status = "Select a supported gofakeit function."
		return m, nil
	case "x", "delete", "backspace":
		m.fakeDataEntries[m.fakeDataCursor].FunctionName = ""
		m.fakeDataEntries[m.fakeDataCursor].FunctionDisplay = ""
		m.fakeDataEntries[m.fakeDataCursor].FunctionParams = nil
		m.status = "Cleared the faker selection for the active column."
	case "a":
		if !m.cfg.LLM.isConfigured() {
			m.status = errString(m.cfg.LLM.configurationError(), "LLM auto-select is not configured.")
			return m, nil
		}
		m.screen = tuiScreenAutoSelecting
		m.status = "Requesting faker suggestions from the configured LLM..."
		return m, autoSelectFakeDataCmd(m.cfg.LLM, m.fakeDataEntries, m.fakeFunctions)
	case "s":
		m.leaveFakeDataEditor()
		return m, nil
	}

	m.adjustFakeDataOffset()
	return m, nil
}

func errString(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}

func (m *tuiModel) leaveFakeDataEditor() {
	m.syncFakeDataIntoConfig()
	m.screen = tuiScreenForm
	m.status = fmt.Sprintf("Saved %d exact fake-data rules.", countExactFullFakeDataRules(m.cfg.FakeData))
}

func (m tuiModel) fakeDataView() string {
	rows := []string{
		"Source schema columns. Press enter to choose or edit a faker, x to clear, a for LLM auto-select, s or esc to return.",
	}
	if !m.cfg.LLM.isConfigured() {
		rows = append(rows, "LLM auto-select is hidden until a usable llm config is present in YAML.")
	}
	rows = append(rows, "")

	visible := m.visibleFakeDataRows()
	start := min(m.fakeDataOffset, max(0, len(m.fakeDataEntries)-1))
	end := min(len(m.fakeDataEntries), start+visible)
	for index := start; index < end; index++ {
		entry := m.fakeDataEntries[index]
		prefix := "  "
		if index == m.fakeDataCursor {
			prefix = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%-48s %-18s %s", prefix, entry.Display, entry.TypeName, fakeDataEntrySummary(entry)))
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) adjustFakeDataOffset() {
	visible := m.visibleFakeDataRows()
	if m.fakeDataCursor < m.fakeDataOffset {
		m.fakeDataOffset = m.fakeDataCursor
	}
	if m.fakeDataCursor >= m.fakeDataOffset+visible {
		m.fakeDataOffset = m.fakeDataCursor - visible + 1
	}
	if m.fakeDataOffset < 0 {
		m.fakeDataOffset = 0
	}
	maxOffset := max(0, len(m.fakeDataEntries)-visible)
	if m.fakeDataOffset > maxOffset {
		m.fakeDataOffset = maxOffset
	}
}

func (m tuiModel) visibleFakeDataRows() int {
	return max(8, m.height-8)
}

func (m tuiModel) updateFakerPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredFakeFunctions()
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = tuiScreenFakeData
		m.status = "Canceled faker selection."
		return m, nil
	case "down":
		if m.pickerCursor < len(filtered)-1 {
			m.pickerCursor++
		}
	case "up":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "pgdown":
		m.pickerCursor = min(len(filtered)-1, m.pickerCursor+m.visiblePickerRows())
	case "pgup":
		m.pickerCursor = max(0, m.pickerCursor-m.visiblePickerRows())
	case "backspace":
		m.pickerQuery = trimLastRune(m.pickerQuery)
		m.pickerCursor = 0
		m.pickerOffset = 0
		return m, nil
	case "enter":
		if len(filtered) == 0 {
			return m, nil
		}
		selected := filtered[m.pickerCursor]
		if len(selected.Params) == 0 {
			entry := &m.fakeDataEntries[m.pickerTarget]
			entry.FunctionName = selected.LookupName
			entry.FunctionDisplay = selected.Display
			entry.FunctionParams = nil
			m.screen = tuiScreenFakeData
			m.status = fmt.Sprintf("Assigned %s to %s.", selected.Display, entry.Display)
			return m, nil
		}
		m.paramTarget = m.pickerTarget
		m.paramOption = selected
		m.paramInput = initialParamInput(selected, m.fakeDataEntries[m.pickerTarget])
		m.screen = tuiScreenFakerParams
		m.status = fmt.Sprintf("Set parameters for %s.", selected.Display)
		return m, nil
	default:
		if msg.Key().Text != "" {
			m.pickerQuery += msg.Key().Text
			m.pickerCursor = 0
			m.pickerOffset = 0
			return m, nil
		}
	}
	m.adjustPickerOffset(len(filtered))
	return m, nil
}

func (m tuiModel) fakerPickerView() string {
	filtered := m.filteredFakeFunctions()
	rows := []string{
		"Search supported gofakeit functions. Type to filter, enter to apply, esc to cancel.",
		"Filter: " + m.pickerQuery,
		"",
	}
	if len(filtered) == 0 {
		rows = append(rows, "No faker matches the current filter.")
		return strings.Join(rows, "\n")
	}

	start := min(m.pickerOffset, max(0, len(filtered)-1))
	end := min(len(filtered), start+m.visiblePickerRows())
	for index := start; index < end; index++ {
		option := filtered[index]
		prefix := "  "
		if index == m.pickerCursor {
			prefix = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%-20s %-14s %-3d %s", prefix, option.Display, option.Category, len(option.Params), option.Description))
	}
	return strings.Join(rows, "\n")
}

func (m tuiModel) updateFakerParams(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = tuiScreenFakeData
		m.status = "Canceled faker parameter editing."
		return m, nil
	case "backspace":
		m.paramInput = trimLastRune(m.paramInput)
		return m, nil
	case "enter":
		entry := &m.fakeDataEntries[m.paramTarget]
		params := parseFakeParameterInput(m.paramInput)
		functionConfig := buildFakeFunctionConfig(m.paramOption.LookupName, params)
		if _, _, err := compileFakeDataRule(entry.Selector, functionConfig); err != nil {
			m.status = err.Error()
			return m, nil
		}
		entry.FunctionName = m.paramOption.LookupName
		entry.FunctionDisplay = m.paramOption.Display
		entry.FunctionParams = params
		m.screen = tuiScreenFakeData
		m.status = fmt.Sprintf("Assigned %s to %s.", m.paramOption.Display, entry.Display)
		return m, nil
	default:
		if msg.Key().Text != "" {
			m.paramInput += msg.Key().Text
			return m, nil
		}
	}
	return m, nil
}

func (m tuiModel) fakerParamsView() string {
	rows := make([]string, 0, 3+len(m.paramOption.Params)+2)
	rows = append(rows,
		fmt.Sprintf("Configure parameters for %s (%s).", m.paramOption.Display, m.paramOption.LookupName),
		"Enter semicolon-separated parameter values in declared order, then press enter to validate and save.",
		"",
	)
	for _, param := range m.paramOption.Params {
		line := fmt.Sprintf("- %s [%s]", param.Field, param.Type)
		if param.Optional {
			line += " optional"
		}
		if param.Default != "" {
			line += " default=" + param.Default
		}
		if len(param.Options) > 0 {
			line += " options=" + strings.Join(param.Options, ",")
		}
		if param.Description != "" {
			line += " - " + param.Description
		}
		rows = append(rows, line)
	}
	rows = append(rows, "", "Parameters: "+m.paramInput)
	return strings.Join(rows, "\n")
}

func (m tuiModel) filteredFakeFunctions() []fakeFunctionOption {
	query := strings.TrimSpace(strings.ToLower(m.pickerQuery))
	if query == "" {
		return m.fakeFunctions
	}
	filtered := make([]fakeFunctionOption, 0, len(m.fakeFunctions))
	for _, option := range m.fakeFunctions {
		if strings.Contains(option.SearchText, query) {
			filtered = append(filtered, option)
		}
	}
	return filtered
}

func (m *tuiModel) adjustPickerOffset(total int) {
	visible := m.visiblePickerRows()
	if m.pickerCursor < m.pickerOffset {
		m.pickerOffset = m.pickerCursor
	}
	if m.pickerCursor >= m.pickerOffset+visible {
		m.pickerOffset = m.pickerCursor - visible + 1
	}
	if m.pickerOffset < 0 {
		m.pickerOffset = 0
	}
	maxOffset := max(0, total-visible)
	if m.pickerOffset > maxOffset {
		m.pickerOffset = maxOffset
	}
}

func (m tuiModel) visiblePickerRows() int {
	return max(8, m.height-10)
}

func (m *tuiModel) syncFakeDataIntoConfig() {
	merged := make(map[string]string, len(m.preservedFakeData)+len(m.fakeDataEntries))
	maps.Copy(merged, m.preservedFakeData)
	for _, entry := range m.fakeDataEntries {
		if entry.FunctionName == "" {
			continue
		}
		merged[entry.Selector] = buildFakeFunctionConfig(entry.FunctionName, entry.FunctionParams)
	}
	if len(merged) == 0 {
		m.cfg.FakeData = nil
	} else {
		m.cfg.FakeData = merged
	}

	if err := saveCachedFakeDataMappings(m.cfg.SourceDSN, m.cfg.FakeData); err != nil {
		m.status = err.Error()
		return
	}
	if err := saveCachedFakeDataEntries(m.cfg.SourceDSN, m.fakeDataEntries); err != nil {
		m.status = err.Error()
	}
}

func loadFakeDataEntriesCmd(cfg config) tea.Cmd {
	return func() tea.Msg {
		entries, err := loadFakeDataEntries(cfg)
		return fakeDataLoadedMsg{entries: entries, err: err}
	}
}

func autoSelectFakeDataCmd(llm llmConfig, entries []tuiFakeDataEntry, options []fakeFunctionOption) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		selections, err := autoSelectFakeDataWithLLM(ctx, llm, entries, options)
		return autoSelectDoneMsg{selections: selections, err: err}
	}
}

func exportConfigCmd(cfg config) tea.Cmd {
	return func() tea.Msg {
		path := strings.TrimSpace(cfg.ConfigPath)
		if path == "" {
			path = defaultConfigPath
		}
		return configExportedMsg{path: path, err: writePersistedConfig(path, cfg)}
	}
}

func startTUIExecutionCmd(configs []config, logPath string) tea.Cmd {
	return func() tea.Msg {
		return runTUIExecution(configs, logPath)
	}
}

func executeTUIExecution(configs []config, logPath string) tea.Msg {
	tuiExecutionMu.Lock()
	defer tuiExecutionMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return executionFinishedMsg{logPath: logPath, err: fmt.Errorf("create log dir: %w", err)}
	}

	logFile, err := os.OpenFile(filepath.Clean(logPath), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return executionFinishedMsg{logPath: logPath, err: fmt.Errorf("open log file: %w", err)}
	}
	defer closeAndLog(logFile, "tui log file")

	oldLogWriter := log.Writer()
	oldStdout := os.Stdout
	log.SetOutput(logFile)
	os.Stdout = logFile
	defer func() {
		log.SetOutput(oldLogWriter)
		os.Stdout = oldStdout
	}()

	log.Printf("tui: started %s", filepath.Base(logPath))
	for _, cfg := range configs {
		if err := executeConfig(cfg); err != nil {
			log.Printf("tui: action failed: %v", err)
			return executionFinishedMsg{logPath: logPath, err: err}
		}
	}
	log.Printf("tui: action finished successfully")
	return executionFinishedMsg{logPath: logPath}
}

func nextTUILogPath(configPath string, mode tuiRunMode) string {
	basePath := strings.TrimSpace(configPath)
	if basePath == "" {
		basePath = defaultConfigPath
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	return filepath.Join(filepath.Dir(basePath), "logs", fmt.Sprintf("%s-%s.log", stamp, mode.logSlug()))
}

func listRecentTUILogPaths(configPath string, limit int) []string {
	basePath := strings.TrimSpace(configPath)
	if basePath == "" {
		basePath = defaultConfigPath
	}
	logDir := filepath.Join(filepath.Dir(basePath), "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil
	}

	type logEntry struct {
		path    string
		modTime time.Time
	}
	logs := make([]logEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{
			path:    filepath.Join(logDir, entry.Name()),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].modTime.After(logs[j].modTime)
	})
	if len(logs) > limit {
		logs = logs[:limit]
	}
	paths := make([]string, 0, len(logs))
	for _, entry := range logs {
		paths = append(paths, entry.path)
	}
	return paths
}

func loadFakeDataEntries(cfg config) ([]tuiFakeDataEntry, error) {
	dataFaker, err := newDataFaker(cfg.FakeData)
	if err != nil {
		return nil, err
	}
	if entries, found, err := loadCachedFakeDataEntries(cfg.SourceDSN); err != nil {
		return nil, err
	} else if found {
		return entries, nil
	}

	sourceDB, err := openDB(cfg.SourceDSN, max(2, cfg.Workers)+2)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer closeAndLog(sourceDB, "source database")

	c := &copier{cfg: cfg, sourceDB: sourceDB, dataFaker: dataFaker}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tables, err := c.loadMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover source metadata: %w", err)
	}

	entries := make([]tuiFakeDataEntry, 0)
	for _, table := range tables {
		for _, col := range table.Columns {
			if !col.Copyable {
				continue
			}
			entry := tuiFakeDataEntry{
				Selector: normalizeFilterName(table.Schema + "." + table.Name + "." + col.Name),
				Display:  table.FQTN() + "." + quoteIdent(col.Name),
				TypeName: displayColumnType(col),
			}
			if rule, ok := dataFaker.matchRule(table, col); ok {
				entry.FunctionName = rule.lookupName
				entry.FunctionDisplay = rule.info.Display
				entry.FunctionParams = flattenFakeParams(rule.info, rule.params)
			}
			entries = append(entries, entry)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Selector < entries[j].Selector
	})
	return entries, nil
}

func availableFakeFunctionOptions() []fakeFunctionOption {
	options := make([]fakeFunctionOption, 0, len(gofakeit.FuncLookups))
	faker := gofakeit.New(1)
	for name, info := range gofakeit.FuncLookups {
		if !fakeFunctionInfoSupported(faker, info) {
			continue
		}
		display := strings.TrimSpace(info.Display)
		if display == "" {
			display = name
		}
		searchText := strings.ToLower(strings.Join([]string{
			name,
			display,
			info.Category,
			info.Description,
			info.Example,
			strings.Join(info.Keywords, " "),
		}, " "))
		options = append(options, fakeFunctionOption{
			LookupName:  name,
			Display:     display,
			Category:    info.Category,
			Description: info.Description,
			Example:     info.Example,
			SearchText:  searchText,
			Params:      slices.Clone(info.Params),
		})
	}

	sort.Slice(options, func(i, j int) bool {
		if options[i].Category != options[j].Category {
			return options[i].Category < options[j].Category
		}
		if options[i].Display != options[j].Display {
			return options[i].Display < options[j].Display
		}
		return options[i].LookupName < options[j].LookupName
	})
	return options
}

func fakeFunctionInfoSupported(faker *gofakeit.Faker, info gofakeit.Info) bool {
	if len(info.Params) == 0 {
		sample, err := info.Generate(faker, nil, &info)
		return err == nil && supportedFakeValue(sample)
	}
	return supportedFakeOutput(info.Output)
}

func supportedFakeOutput(output string) bool {
	output = strings.ToLower(strings.TrimSpace(output))
	switch output {
	case "string", "[]byte", "bool", "time.time",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	default:
		return false
	}
}

func flattenFakeParams(info gofakeit.Info, params gofakeit.MapParams) []string {
	if len(params) == 0 {
		return nil
	}
	values := make([]string, 0)
	for _, param := range info.Params {
		values = append(values, params.Get(param.Field)...)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func buildFakeFunctionConfig(name string, params []string) string {
	name = strings.TrimSpace(name)
	if len(params) == 0 {
		return name
	}
	return name + ";" + strings.Join(params, ";")
}

func fakeDataEntrySummary(entry tuiFakeDataEntry) string {
	if entry.FunctionDisplay == "" {
		return "-"
	}
	summary := entry.FunctionDisplay + " (" + entry.FunctionName + ")"
	if len(entry.FunctionParams) > 0 {
		summary += "; " + strings.Join(entry.FunctionParams, ";")
	}
	return summary
}

func initialParamInput(option fakeFunctionOption, entry tuiFakeDataEntry) string {
	if entry.FunctionName == option.LookupName && len(entry.FunctionParams) > 0 {
		return strings.Join(entry.FunctionParams, ";")
	}
	return ""
}

func parseFakeParameterInput(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	params := make([]string, 0, len(parts))
	for _, part := range parts {
		params = append(params, strings.TrimSpace(part))
	}
	return params
}

func preserveNonFullFakeData(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	preserved := make(map[string]string, len(values))
	for selector, functionName := range values {
		target, _, ok := exactFakeDataTarget(selector)
		if ok && target == "full" {
			continue
		}
		preserved[selector] = functionName
	}
	if len(preserved) == 0 {
		return nil
	}
	return preserved
}

func countExactFullFakeDataRules(values map[string]string) int {
	count := 0
	for selector := range values {
		target, _, ok := exactFakeDataTarget(selector)
		if ok && target == "full" {
			count++
		}
	}
	return count
}

func displayColumnType(col columnMeta) string {
	if col.IsUserDefined {
		return col.TypeSchema + "." + col.UserTypeName
	}
	return col.SystemTypeName
}

func trimLastRune(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	return string(runes[:len(runes)-1])
}

func parsePositiveIntField(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	if parsed < 1 {
		return 0, fmt.Errorf("must be greater than 0")
	}
	return parsed, nil
}

func parseNonNegativeIntField(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	if parsed < 0 {
		return 0, fmt.Errorf("must be greater than or equal to 0")
	}
	return parsed, nil
}

func initialTUIRunMode(cfg config) tuiRunMode {
	if cfg.Plan {
		return tuiRunModePlan
	}
	if strings.TrimSpace(cfg.ExportDDLFile) != "" && strings.TrimSpace(cfg.ExportDataFile) != "" {
		return tuiRunModeExportDDLData
	}
	if strings.TrimSpace(cfg.ExportDataFile) != "" {
		return tuiRunModeExportDDLData
	}
	if strings.TrimSpace(cfg.ExportDDLFile) != "" {
		return tuiRunModeExportDDL
	}
	return tuiRunModeCopy
}

func (mode tuiRunMode) next() tuiRunMode {
	return (mode + 1) % 4
}

func (mode tuiRunMode) label() string {
	switch mode {
	case tuiRunModeCopy:
		return "copy"
	case tuiRunModePlan:
		return "plan"
	case tuiRunModeExportDDL:
		return "ddl"
	case tuiRunModeExportDDLData:
		return "ddl+data"
	default:
		return "unknown"
	}
}

func (mode tuiRunMode) logSlug() string {
	switch mode {
	case tuiRunModeCopy:
		return "copy"
	case tuiRunModePlan:
		return "plan"
	case tuiRunModeExportDDL:
		return "ddl"
	case tuiRunModeExportDDLData:
		return "ddl-data"
	default:
		return "run"
	}
}

func (mode tuiRunMode) showsTargetSettings() bool {
	return mode == tuiRunModeCopy
}

func (mode tuiRunMode) showsCopyExecutionSettings() bool {
	return mode == tuiRunModeCopy
}

func (mode tuiRunMode) allowsDropExisting() bool {
	return mode == tuiRunModeCopy || mode == tuiRunModePlan
}

func (mode tuiRunMode) allowsFakeData() bool {
	return mode == tuiRunModeCopy || mode == tuiRunModeExportDDLData
}

func (mode tuiRunMode) submitLabel() string {
	switch mode {
	case tuiRunModeCopy:
		return "Start copy"
	case tuiRunModePlan:
		return "Run plan"
	case tuiRunModeExportDDL:
		return "Export DDL"
	case tuiRunModeExportDDLData:
		return "Export DDL+data"
	default:
		return "Start"
	}
}
