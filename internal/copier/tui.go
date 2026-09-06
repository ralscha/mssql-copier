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

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	formFieldDockerBundleDir
	formFieldDockerPort
	formFieldDockerPersistent
	formFieldDockerPassword
	formFieldWorkers
	formFieldBatchSize
	formFieldVerbose
	formFieldDropExisting
	formFieldEnableFakeData
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
	Source          sqlServerDSNForm
	Target          sqlServerDSNForm
	RunMode         tuiRunMode
	DockerDir       string
	DockerBundleDir string
	DockerPort      string
	Workers         string
	BatchSize       string
	IncludeSchemas  string
	ExcludeSchemas  string
	IncludeTables   string
	ExcludeTables   string
	ExportDDLPath   string
	ExportDataPath  string
	ExportDataRows  string
	ReportPath      string
	ExportPath      string
}

type tuiFakeDataEntry struct {
	Selector        string
	Display         string
	TypeName        string
	FunctionName    string
	FunctionDisplay string
	FunctionParams  []string
	HasUnique       bool
	RequireUnique   bool
}

type fakeFunctionOption struct {
	LookupName  string
	Display     string
	Category    string
	Description string
	Example     string
	SearchText  string
	Output      string // gofakeit output type, e.g. "string", "int", "bool", "time.Time"
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
	cfg                      config
	form                     tuiFormState
	screen                   tuiScreen
	formFocus                int
	status                   string
	width                    int
	height                   int
	fakeDataEntries          []tuiFakeDataEntry
	pickerTarget             int
	pickerCursor             int
	paramTarget              int
	paramOption              fakeFunctionOption
	fakeFunctions            []fakeFunctionOption
	preservedFakeData        map[string]string
	preservedUniqueSelectors map[string]bool
	runInProgress            bool
	currentLogPath           string
	recentLogPaths           []string
	quitting                 bool
	logTailLines             []string
	logTailScroll            int
	logPanelFocused          bool

	// Bubbles v2 components.
	formInputs    []textinput.Model
	fakeDataTable table.Model
	pickerInput   textinput.Model
	paramInput    textinput.Model
	spinner       spinner.Model
}

func runTUI(cfg config) error {
	program := tea.NewProgram(newTUIModel(cfg))
	_, err := program.Run()
	return err
}

// formTextFields lists form field indices that are text inputs.
var formTextFields = []int{
	formFieldSourceServer, formFieldSourcePort, formFieldSourceDatabase,
	formFieldSourceUser, formFieldSourcePassword, formFieldSourceEncrypt,
	formFieldSourceTrustCert, formFieldSourceOptions,
	formFieldTargetServer, formFieldTargetPort, formFieldTargetDatabase,
	formFieldTargetUser, formFieldTargetPassword, formFieldTargetEncrypt,
	formFieldTargetTrustCert, formFieldTargetOptions,
	formFieldDockerDir, formFieldDockerBundleDir, formFieldDockerPort,
	formFieldWorkers, formFieldBatchSize,
	formFieldIncludeSchemas, formFieldExcludeSchemas,
	formFieldIncludeTables, formFieldExcludeTables,
	formFieldExportDDLPath, formFieldExportDataPath, formFieldExportDataRows,
	formFieldReportPath, formFieldExportPath,
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

	form := tuiFormState{
		Source:          parseSQLServerDSNForm(cfg.SourceDSN),
		Target:          parseSQLServerDSNForm(cfg.TargetDSN),
		RunMode:         initialTUIRunMode(cfg),
		DockerDir:       cfg.Docker.ComposeDir,
		DockerBundleDir: cfg.Docker.BundleDir,
		DockerPort:      dockerPortStr,
		Workers:         strconv.Itoa(max(1, cfg.Workers)),
		BatchSize:       strconv.Itoa(max(1, cfg.BatchSize)),
		IncludeSchemas:  strings.Join(cfg.IncludeSchemas, ","),
		ExcludeSchemas:  strings.Join(cfg.ExcludeSchemas, ","),
		IncludeTables:   strings.Join(cfg.IncludeTables, ","),
		ExcludeTables:   strings.Join(cfg.ExcludeTables, ","),
		ExportDDLPath:   strings.TrimSpace(cfg.ExportDDLFile),
		ExportDataPath:  strings.TrimSpace(cfg.ExportDataFile),
		ExportDataRows:  strconv.Itoa(max(0, cfg.ExportDataRows)),
		ReportPath:      strings.TrimSpace(cfg.ReportMDFile),
		ExportPath:      exportPath,
	}

	// Initialize Bubbles v2 text inputs for all text-capable form fields.
	formInputs := make([]textinput.Model, formFieldCount)
	placeholderSty := lipgloss.NewStyle().Foreground(colorMuted)
	focusedSty := textinput.DefaultStyles(true)
	focusedSty.Focused.Prompt = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	focusedSty.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.White)
	focusedSty.Blurred.Placeholder = placeholderSty
	focusedSty.Focused.Placeholder = placeholderSty

	for _, field := range formTextFields {
		formInputs[field] = textinput.New()
		formInputs[field].Prompt = "  "
		formInputs[field].SetWidth(40)
		formInputs[field].SetStyles(focusedSty)
	}

	// Set placeholders and initial values.
	formInputs[formFieldSourceServer].Placeholder = "localhost"
	formInputs[formFieldSourcePort].Placeholder = "1433"
	formInputs[formFieldSourcePort].SetValue(form.Source.Port)
	formInputs[formFieldSourceDatabase].Placeholder = "mydb"
	formInputs[formFieldSourceDatabase].SetValue(form.Source.Database)
	formInputs[formFieldSourceUser].Placeholder = "sa"
	formInputs[formFieldSourceUser].SetValue(form.Source.Username)
	formInputs[formFieldSourcePassword].SetValue(form.Source.Password)
	formInputs[formFieldSourcePassword].EchoMode = textinput.EchoPassword
	formInputs[formFieldSourcePassword].EchoCharacter = rune(8226)
	formInputs[formFieldSourceEncrypt].Placeholder = "disable"
	formInputs[formFieldSourceEncrypt].SetValue(form.Source.Encrypt)
	formInputs[formFieldSourceTrustCert].Placeholder = "true"
	formInputs[formFieldSourceTrustCert].SetValue(form.Source.TrustServerCertificate)
	formInputs[formFieldSourceOptions].SetValue(form.Source.Options)

	formInputs[formFieldTargetServer].Placeholder = "localhost"
	formInputs[formFieldTargetPort].Placeholder = "1433"
	formInputs[formFieldTargetPort].SetValue(form.Target.Port)
	formInputs[formFieldTargetDatabase].Placeholder = "mydb"
	formInputs[formFieldTargetDatabase].SetValue(form.Target.Database)
	formInputs[formFieldTargetUser].Placeholder = "sa"
	formInputs[formFieldTargetUser].SetValue(form.Target.Username)
	formInputs[formFieldTargetPassword].SetValue(form.Target.Password)
	formInputs[formFieldTargetPassword].EchoMode = textinput.EchoPassword
	formInputs[formFieldTargetPassword].EchoCharacter = rune(8226)
	formInputs[formFieldTargetEncrypt].Placeholder = "disable"
	formInputs[formFieldTargetEncrypt].SetValue(form.Target.Encrypt)
	formInputs[formFieldTargetTrustCert].Placeholder = "true"
	formInputs[formFieldTargetTrustCert].SetValue(form.Target.TrustServerCertificate)
	formInputs[formFieldTargetOptions].SetValue(form.Target.Options)

	formInputs[formFieldDockerDir].Placeholder = defaultDockerDir
	formInputs[formFieldDockerDir].SetValue(form.DockerDir)
	formInputs[formFieldDockerBundleDir].Placeholder = defaultDockerBundleDir
	formInputs[formFieldDockerBundleDir].SetValue(form.DockerBundleDir)
	formInputs[formFieldDockerPort].Placeholder = strconv.Itoa(defaultDockerPort)
	formInputs[formFieldDockerPort].SetValue(form.DockerPort)
	formInputs[formFieldWorkers].Placeholder = "4"
	formInputs[formFieldWorkers].SetValue(form.Workers)
	formInputs[formFieldBatchSize].Placeholder = "5000"
	formInputs[formFieldBatchSize].SetValue(form.BatchSize)
	formInputs[formFieldIncludeSchemas].SetValue(form.IncludeSchemas)
	formInputs[formFieldExcludeSchemas].SetValue(form.ExcludeSchemas)
	formInputs[formFieldIncludeTables].SetValue(form.IncludeTables)
	formInputs[formFieldExcludeTables].SetValue(form.ExcludeTables)
	formInputs[formFieldExportDDLPath].SetValue(form.ExportDDLPath)
	formInputs[formFieldExportDataPath].SetValue(form.ExportDataPath)
	formInputs[formFieldExportDataRows].SetValue(form.ExportDataRows)
	formInputs[formFieldReportPath].SetValue(form.ReportPath)
	formInputs[formFieldExportPath].SetValue(form.ExportPath)

	// Focus the first field.
	formInputs[formFieldSourceServer].Focus()

	// Custom table styles for the fake data screen.
	tableStyles := table.DefaultStyles()
	tableStyles.Header = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 1).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorSubtle)
	tableStyles.Cell = lipgloss.NewStyle().Padding(0, 1)
	tableStyles.Selected = lipgloss.NewStyle().
		Foreground(lipgloss.White).
		Background(colorPrimary).
		Bold(true)

	fakeDataTable := table.New(
		table.WithColumns([]table.Column{
			{Title: "Column", Width: 48},
			{Title: "Type", Width: 20},
			{Title: "Faker Function", Width: 36},
		}),
		table.WithFocused(true),
		table.WithHeight(20),
		table.WithStyles(tableStyles),
	)

	// Picker text input for filtering.
	pickerInput := textinput.New()
	pickerInput.Placeholder = "type to filter..."
	pickerInput.Prompt = "  search: "
	pickerInput.SetWidth(40)
	pickerInput.Focus()

	// Params text input.
	paramInput := textinput.New()
	paramInput.Placeholder = "value"
	paramInput.Prompt = "  > "
	paramInput.SetWidth(40)
	paramInput.Focus()

	// Spinner.
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	// Load cached fake data mappings if a source DSN is available
	if cfg.SourceDSN != "" {
		if cachedFakeData, found, err := loadCachedFakeDataMappings(cfg.SourceDSN); err == nil && found {
			cfg.FakeData = cachedFakeData
		}
		if cachedUnique, found, err := loadCachedFakeDataUnique(cfg.SourceDSN); err == nil && found {
			cfg.FakeDataUnique = cachedUnique
		}
	}

	return tuiModel{
		cfg:                      cfg,
		form:                     form,
		preservedUniqueSelectors: cloneStringBoolMap(cfg.FakeDataUnique),
		screen:                   tuiScreenForm,
		formFocus:                formFieldSourceServer,
		width:                    100,
		height:                   30,
		formInputs:               formInputs,
		fakeDataTable:            fakeDataTable,
		pickerInput:              pickerInput,
		paramInput:               paramInput,
		fakeFunctions:            availableFakeFunctionOptions(),
		preservedFakeData:        preserveNonFullFakeData(cfg.FakeData),
		recentLogPaths:           listRecentTUILogPaths(exportPath, 5),
		spinner:                  s,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.formInputs[formFieldSourceServer].Focus())
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.fakeDataTable.SetWidth(msg.Width - 4)
		m.fakeDataTable.SetHeight(msg.Height - 12)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case fakeDataLoadedMsg:
		if msg.err != nil {
			m.screen = tuiScreenForm
			m.status = msg.err.Error()
			return m, nil
		}
		m.screen = tuiScreenFakeData
		m.fakeDataEntries = msg.entries
		m.rebuildFakeDataTable()
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
		m.rebuildFakeDataTable()
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
				var cmd tea.Cmd
				m.formInputs[m.formFocus], cmd = m.formInputs[m.formFocus].Update(msg)
				return m, cmd
			}
		case tuiScreenFakerPicker:
			var cmd tea.Cmd
			m.pickerInput, cmd = m.pickerInput.Update(msg)
			return m, cmd
		case tuiScreenFakerParams:
			var cmd tea.Cmd
			m.paramInput, cmd = m.paramInput.Update(msg)
			return m, cmd
		case tuiScreenLoadingFakeData, tuiScreenFakeData, tuiScreenAutoSelecting:
			// paste is ignored in these screens
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
	var b strings.Builder

	b.WriteString(titleStyle.Render("mssql-copier -- SQL Server Data Copier & Faker"))
	b.WriteString("\n")
	b.WriteString(m.statusView())
	b.WriteString("\n\n")

	switch m.screen {
	case tuiScreenForm:
		b.WriteString(m.formView())
	case tuiScreenLoadingFakeData:
		b.WriteString(m.workingView("Connecting to source and loading schema metadata..."))
	case tuiScreenFakeData:
		b.WriteString(m.fakeDataView())
	case tuiScreenFakerPicker:
		b.WriteString(m.fakerPickerView())
	case tuiScreenFakerParams:
		b.WriteString(m.fakerParamsView())
	case tuiScreenAutoSelecting:
		b.WriteString(m.workingView("Analyzing columns with the configured LLM..."))
	}

	view := tea.NewView(b.String())
	view.AltScreen = true
	return view
}

func (m tuiModel) statusView() string {
	if strings.TrimSpace(m.status) == "" {
		return statusStyle.Render("  Status: ready")
	}
	if strings.Contains(m.status, "failed") || strings.Contains(m.status, "Error") || strings.Contains(m.status, "error") || strings.Contains(m.status, "required") {
		return statusErrStyle.Render("  Status: " + m.status)
	}
	return statusOKStyle.Render("  Status: " + m.status)
}

func (m tuiModel) workingView(message string) string {
	return fmt.Sprintf("  %s %s\n\n%s",
		m.spinner.View(),
		message,
		helpStyle.Render("  Press ctrl+c or q to cancel."),
	)
}

func (m tuiModel) formView() string {
	var b strings.Builder

	b.WriteString(sectionHeaderStyle.Render("  Source Connection"))
	b.WriteString("\n")
	b.WriteString(m.formTextRow(formFieldSourceServer, "Source server"))
	b.WriteString(m.formTextRow(formFieldSourcePort, "Source port"))
	b.WriteString(m.formTextRow(formFieldSourceDatabase, "Source database"))
	b.WriteString(m.formTextRow(formFieldSourceUser, "Source user"))
	b.WriteString(m.formTextRow(formFieldSourcePassword, "Source password"))
	b.WriteString(m.formTextRow(formFieldSourceEncrypt, "Source encrypt"))
	b.WriteString(m.formTextRow(formFieldSourceTrustCert, "Source trust cert"))
	b.WriteString(m.formTextRow(formFieldSourceOptions, "Source options"))

	b.WriteString("\n")
	b.WriteString(sectionHeaderStyle.Render("  Run Mode & Target"))
	b.WriteString("\n")
	b.WriteString(m.formActionRow(formFieldRunMode, "Run mode: "+m.form.RunMode.label()))

	if m.isFormFieldVisible(formFieldTargetType) {
		targetTypeLabel := "local (enter to switch to docker)"
		if m.cfg.Docker.Enabled {
			targetTypeLabel = "docker (enter to switch to local)"
		}
		b.WriteString(m.formActionRow(formFieldTargetType, "Target type: "+targetTypeLabel))
	}

	if m.cfg.Docker.Enabled {
		saPasswordDisplay := m.cfg.Docker.SAPassword
		if saPasswordDisplay == "" {
			saPasswordDisplay = "<not generated>"
		}
		b.WriteString(m.formTextRow(formFieldDockerDir, "Compose dir"))
		b.WriteString(m.formTextRow(formFieldDockerPort, "Docker port"))
		b.WriteString(m.formActionRow(formFieldDockerPersistent, "Storage: "+m.cfg.Docker.storageLabel()+" (enter to change)"))
		if m.cfg.Docker.Portable {
			b.WriteString(m.formTextRow(formFieldDockerBundleDir, "Portable bundle dir"))
		}
		b.WriteString(m.formActionRow(formFieldDockerPassword, "SA password: "+saPasswordDisplay+" (enter to regenerate)"))
	} else if m.isFormFieldVisible(formFieldTargetServer) {
		b.WriteString(m.formTextRow(formFieldTargetServer, "Target server"))
		b.WriteString(m.formTextRow(formFieldTargetPort, "Target port"))
		b.WriteString(m.formTextRow(formFieldTargetDatabase, "Target database"))
		b.WriteString(m.formTextRow(formFieldTargetUser, "Target user"))
		b.WriteString(m.formTextRow(formFieldTargetPassword, "Target password"))
		b.WriteString(m.formTextRow(formFieldTargetEncrypt, "Target encrypt"))
		b.WriteString(m.formTextRow(formFieldTargetTrustCert, "Target trust cert"))
		b.WriteString(m.formTextRow(formFieldTargetOptions, "Target options"))
	}

	b.WriteString("\n")
	b.WriteString(sectionHeaderStyle.Render("  Execution Settings"))
	b.WriteString("\n")
	if m.isFormFieldVisible(formFieldWorkers) {
		b.WriteString(m.formTextRow(formFieldWorkers, "Workers"))
		b.WriteString(m.formTextRow(formFieldBatchSize, "Batch size"))
	}
	if m.isFormFieldVisible(formFieldVerbose) {
		b.WriteString(m.formBoolRow(formFieldVerbose, "Verbose", m.cfg.Verbose))
	}
	if m.isFormFieldVisible(formFieldDropExisting) {
		b.WriteString(m.formBoolRow(formFieldDropExisting, "Drop existing", m.cfg.DropExisting))
	}
	if m.isFormFieldVisible(formFieldEnableFakeData) {
		b.WriteString(m.formBoolRow(formFieldEnableFakeData, "Enable fake data", m.cfg.EnableFakeData))
	}
	b.WriteString(m.formTextRow(formFieldIncludeSchemas, "Include schemas"))
	b.WriteString(m.formTextRow(formFieldExcludeSchemas, "Exclude schemas"))
	b.WriteString(m.formTextRow(formFieldIncludeTables, "Include tables"))
	b.WriteString(m.formTextRow(formFieldExcludeTables, "Exclude tables"))
	if m.isFormFieldVisible(formFieldExportDDLPath) {
		b.WriteString(m.formTextRow(formFieldExportDDLPath, "DDL export path"))
	}
	if m.isFormFieldVisible(formFieldExportDataPath) {
		b.WriteString(m.formTextRow(formFieldExportDataPath, "Data export path"))
		b.WriteString(m.formTextRow(formFieldExportDataRows, "Export data rows"))
	}
	if m.isFormFieldVisible(formFieldReportPath) {
		b.WriteString(m.formTextRow(formFieldReportPath, "Report path"))
	}
	b.WriteString(m.formTextRow(formFieldExportPath, "Config path"))

	b.WriteString("\n")
	if m.isFormFieldVisible(formFieldEditFakeData) {
		b.WriteString(m.formActionRow(formFieldEditFakeData, fmt.Sprintf("[^F] Edit fake data (%d exact rules)", countExactFullFakeDataRules(m.cfg.FakeData))))
	}
	b.WriteString(m.formActionRow(formFieldExportConfig, "[^E] Export YAML config"))
	b.WriteString(m.formActionRow(formFieldStartCopy, fmt.Sprintf("[^R] %s", m.form.RunMode.submitLabel())))

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  Keys: type to edit, tab/\u2191\u2193 navigate, enter toggles/actions, ^F=edit rules, ^E=export config, ^R=start, ctrl+c quits, ctrl+l focuses log panel."))

	b.WriteString("\n")
	b.WriteString(m.executionView())
	b.WriteString("\n")
	b.WriteString(m.logFilesView())
	b.WriteString("\n")
	b.WriteString(m.logTailView())

	return b.String()
}

func (m tuiModel) formTextRow(field int, labelText string) string {
	lbl := labelStyle.Render(fmt.Sprintf("%-20s", labelText))
	if m.formFocus == field {
		lbl = activeLabelStyle.Render(fmt.Sprintf("%-20s", labelText))
		return fmt.Sprintf("  %s %s\n", lbl, m.formInputs[field].View())
	}
	return fmt.Sprintf("  %s %s\n", lbl, m.formInputs[field].View())
}

func (m tuiModel) formBoolRow(field int, labelText string, value bool) string {
	state := lipgloss.NewStyle().Foreground(colorMuted).Render("no")
	if value {
		state = lipgloss.NewStyle().Foreground(colorAccent).Render("yes")
	}
	lbl := labelStyle.Render(fmt.Sprintf("%-20s", labelText))
	if m.formFocus == field {
		lbl = activeLabelStyle.Render(fmt.Sprintf("%-20s", labelText))
	}
	marker := "  "
	if m.formFocus == field {
		marker = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render("> ")
	}
	return fmt.Sprintf("  %s %s%s\n", lbl, marker, state)
}

func (m tuiModel) formActionRow(field int, labelText string) string {
	if m.formFocus == field {
		return "  " + activeButtonStyle.Render("[ "+labelText+" ]") + "\n"
	}
	return "  " + buttonStyle.Render("[ "+labelText+" ]") + "\n"
}

func (m tuiModel) executionView() string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("  Action"))
	b.WriteString("\n")
	if m.runInProgress {
		b.WriteString(statusStyle.Render(fmt.Sprintf("  State: running %s", m.form.RunMode.label())))
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  Active log: %s", m.currentLogPath)))
		b.WriteString("\n")
		b.WriteString(statusStyle.Render("  Start is disabled until the current action completes."))
	} else {
		b.WriteString(statusOKStyle.Render("  State: idle"))
		b.WriteString("\n")
		if strings.TrimSpace(m.currentLogPath) != "" {
			b.WriteString(statusStyle.Render(fmt.Sprintf("  Last log: %s", m.currentLogPath)))
		}
	}
	return b.String()
}

func (m tuiModel) logFilesView() string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("  Log files"))
	b.WriteString("\n")
	logPaths := m.visibleLogPaths()
	if len(logPaths) == 0 {
		b.WriteString(statusStyle.Render("  No TUI log files yet. Start an action to create one."))
		return b.String()
	}
	for _, logPath := range logPaths {
		b.WriteString(statusStyle.Render("  - " + logPath))
		b.WriteString("\n")
	}
	return b.String()
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
		formFieldDockerDir, formFieldDockerBundleDir, formFieldDockerPort, formFieldDockerPersistent, formFieldDockerPassword,
		formFieldWorkers, formFieldBatchSize, formFieldVerbose,
		formFieldDropExisting, formFieldEnableFakeData,
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
	var b strings.Builder
	b.WriteString(strings.Repeat("─", max(1, m.width-2)))
	b.WriteString("\n")

	if m.logPanelFocused {
		b.WriteString(lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render("▶ Log tail"))
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render(" (scroll with arrows / pgup / pgdn, esc to return to config)"))
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("  Log tail (press ctrl+l to focus)"))
	}
	if path := m.bestLogPathForTail(); path != "" {
		b.WriteString(" — ")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render(filepath.Base(path)))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", max(1, m.width-2)))
	b.WriteString("\n")

	if len(m.logTailLines) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("  No log content yet. Start an action to see output here."))
		b.WriteString("\n")
		return b.String()
	}

	visible := m.visibleLogTailRows()
	start := m.logTailScroll
	end := min(len(m.logTailLines), start+visible)
	for i := start; i < end; i++ {
		line := m.logTailLines[i]
		maxLineWidth := max(1, m.width-2)
		if len(line) > maxLineWidth {
			line = line[:maxLineWidth]
		}
		b.WriteString(" ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
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
	case "ctrl+f":
		if !m.isFormFieldVisible(formFieldEditFakeData) {
			return m, nil
		}
		m.syncInputsToForm()
		m.formFocus = formFieldEditFakeData
		m.blurAllInputs()
		return m.handleFormEnter()
	case "ctrl+e":
		m.syncInputsToForm()
		m.formFocus = formFieldExportConfig
		m.blurAllInputs()
		return m.handleFormEnter()
	case "ctrl+r":
		m.syncInputsToForm()
		m.formFocus = formFieldStartCopy
		m.blurAllInputs()
		return m.handleFormEnter()
	case "tab":
		return m.cycleFormFocus(1), nil
	case "shift+tab":
		return m.cycleFormFocus(-1), nil
	case "down":
		return m.cycleFormFocus(1), nil
	case "up":
		return m.cycleFormFocus(-1), nil
	case "enter":
		return m.handleFormEnter()
	}

	// Forward unmatched keys to the focused text input.
	if m.isFormFieldTextInput(m.formFocus) {
		var cmd tea.Cmd
		m.formInputs[m.formFocus], cmd = m.formInputs[m.formFocus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) cycleFormFocus(direction int) tuiModel {
	if direction > 0 {
		m.formFocus = m.nextFormField()
	} else {
		m.formFocus = m.prevFormField()
	}
	// Blur all text inputs, then focus the current one if it's a text field.
	m.blurAllInputs()
	if m.isFormFieldTextInput(m.formFocus) {
		m.formInputs[m.formFocus].Focus()
	}
	return m
}

func (m *tuiModel) blurAllInputs() {
	for i := range m.formInputs {
		m.formInputs[i].Blur()
	}
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
		formFieldVerbose, formFieldDropExisting, formFieldEnableFakeData,
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
	case formFieldDockerBundleDir:
		return runMode.showsTargetSettings() && m.cfg.Docker.Enabled && m.cfg.Docker.Portable
	case formFieldWorkers, formFieldBatchSize, formFieldVerbose, formFieldReportPath:
		return runMode.showsCopyExecutionSettings()
	case formFieldDropExisting:
		return runMode.allowsDropExisting()
	case formFieldEnableFakeData, formFieldEditFakeData:
		return runMode.allowsFakeData()
	case formFieldExportDDLPath:
		return runMode == tuiRunModeExportDDL || runMode == tuiRunModeExportDDLData
	case formFieldExportDataPath, formFieldExportDataRows:
		return runMode == tuiRunModeExportDDLData
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
	// Sync textinput values back to form state before processing.
	m.syncInputsToForm()

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
		m.cfg.Docker.cycleStorage()
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
	case formFieldEnableFakeData:
		m.cfg.EnableFakeData = !m.cfg.EnableFakeData
	case formFieldEditFakeData:
		cfg, err := m.configFromForm(true, false)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.adoptConfig(cfg)
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
		m.adoptConfig(cfg)
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
		m.adoptConfig(configs[0])
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

// syncFormToInputs copies form state values into textinput.Model instances.
func (m *tuiModel) syncFormToInputs() {
	m.formInputs[formFieldSourceServer].SetValue(m.form.Source.Server)
	m.formInputs[formFieldSourcePort].SetValue(m.form.Source.Port)
	m.formInputs[formFieldSourceDatabase].SetValue(m.form.Source.Database)
	m.formInputs[formFieldSourceUser].SetValue(m.form.Source.Username)
	m.formInputs[formFieldSourcePassword].SetValue(m.form.Source.Password)
	m.formInputs[formFieldSourceEncrypt].SetValue(m.form.Source.Encrypt)
	m.formInputs[formFieldSourceTrustCert].SetValue(m.form.Source.TrustServerCertificate)
	m.formInputs[formFieldSourceOptions].SetValue(m.form.Source.Options)
	m.formInputs[formFieldTargetServer].SetValue(m.form.Target.Server)
	m.formInputs[formFieldTargetPort].SetValue(m.form.Target.Port)
	m.formInputs[formFieldTargetDatabase].SetValue(m.form.Target.Database)
	m.formInputs[formFieldTargetUser].SetValue(m.form.Target.Username)
	m.formInputs[formFieldTargetPassword].SetValue(m.form.Target.Password)
	m.formInputs[formFieldTargetEncrypt].SetValue(m.form.Target.Encrypt)
	m.formInputs[formFieldTargetTrustCert].SetValue(m.form.Target.TrustServerCertificate)
	m.formInputs[formFieldTargetOptions].SetValue(m.form.Target.Options)
	m.formInputs[formFieldDockerDir].SetValue(m.form.DockerDir)
	m.formInputs[formFieldDockerBundleDir].SetValue(m.form.DockerBundleDir)
	m.formInputs[formFieldDockerPort].SetValue(m.form.DockerPort)
	m.formInputs[formFieldWorkers].SetValue(m.form.Workers)
	m.formInputs[formFieldBatchSize].SetValue(m.form.BatchSize)
	m.formInputs[formFieldIncludeSchemas].SetValue(m.form.IncludeSchemas)
	m.formInputs[formFieldExcludeSchemas].SetValue(m.form.ExcludeSchemas)
	m.formInputs[formFieldIncludeTables].SetValue(m.form.IncludeTables)
	m.formInputs[formFieldExcludeTables].SetValue(m.form.ExcludeTables)
	m.formInputs[formFieldExportDDLPath].SetValue(m.form.ExportDDLPath)
	m.formInputs[formFieldExportDataPath].SetValue(m.form.ExportDataPath)
	m.formInputs[formFieldExportDataRows].SetValue(m.form.ExportDataRows)
	m.formInputs[formFieldReportPath].SetValue(m.form.ReportPath)
	m.formInputs[formFieldExportPath].SetValue(m.form.ExportPath)
}

// syncInputsToForm copies textinput.Model values back into form state.
func (m *tuiModel) syncInputsToForm() {
	m.form.Source.Server = m.formInputs[formFieldSourceServer].Value()
	m.form.Source.Port = m.formInputs[formFieldSourcePort].Value()
	m.form.Source.Database = m.formInputs[formFieldSourceDatabase].Value()
	m.form.Source.Username = m.formInputs[formFieldSourceUser].Value()
	m.form.Source.Password = m.formInputs[formFieldSourcePassword].Value()
	m.form.Source.Encrypt = m.formInputs[formFieldSourceEncrypt].Value()
	m.form.Source.TrustServerCertificate = m.formInputs[formFieldSourceTrustCert].Value()
	m.form.Source.Options = m.formInputs[formFieldSourceOptions].Value()
	m.form.Target.Server = m.formInputs[formFieldTargetServer].Value()
	m.form.Target.Port = m.formInputs[formFieldTargetPort].Value()
	m.form.Target.Database = m.formInputs[formFieldTargetDatabase].Value()
	m.form.Target.Username = m.formInputs[formFieldTargetUser].Value()
	m.form.Target.Password = m.formInputs[formFieldTargetPassword].Value()
	m.form.Target.Encrypt = m.formInputs[formFieldTargetEncrypt].Value()
	m.form.Target.TrustServerCertificate = m.formInputs[formFieldTargetTrustCert].Value()
	m.form.Target.Options = m.formInputs[formFieldTargetOptions].Value()
	m.form.DockerDir = m.formInputs[formFieldDockerDir].Value()
	m.form.DockerBundleDir = m.formInputs[formFieldDockerBundleDir].Value()
	m.form.DockerPort = m.formInputs[formFieldDockerPort].Value()
	m.form.Workers = m.formInputs[formFieldWorkers].Value()
	m.form.BatchSize = m.formInputs[formFieldBatchSize].Value()
	m.form.IncludeSchemas = m.formInputs[formFieldIncludeSchemas].Value()
	m.form.ExcludeSchemas = m.formInputs[formFieldExcludeSchemas].Value()
	m.form.IncludeTables = m.formInputs[formFieldIncludeTables].Value()
	m.form.ExcludeTables = m.formInputs[formFieldExcludeTables].Value()
	m.form.ExportDDLPath = m.formInputs[formFieldExportDDLPath].Value()
	m.form.ExportDataPath = m.formInputs[formFieldExportDataPath].Value()
	m.form.ExportDataRows = m.formInputs[formFieldExportDataRows].Value()
	m.form.ReportPath = m.formInputs[formFieldReportPath].Value()
	m.form.ExportPath = m.formInputs[formFieldExportPath].Value()
}

// rebuildFakeDataTable refreshes the table.Model rows from fakeDataEntries.
func (m *tuiModel) rebuildFakeDataTable() {
	rows := make([]table.Row, len(m.fakeDataEntries))
	for i, entry := range m.fakeDataEntries {
		fakerName := "-"
		if entry.FunctionDisplay != "" {
			fakerName = entry.FunctionDisplay
			if len(entry.FunctionParams) > 0 {
				fakerName += "; " + strings.Join(entry.FunctionParams, ";")
			}
			// Add unique indicator
			if entry.HasUnique {
				if entry.RequireUnique {
					fakerName += " [U*]" // Enforced unique
				} else {
					fakerName += " [U]" // Optional unique
				}
			}
		}
		rows[i] = table.Row{entry.Display, entry.TypeName, fakerName}
	}
	m.fakeDataTable.SetRows(rows)
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
	sameFakeDataSource := sameFakeDataSourceDSN(cfg.SourceDSN, m.cfg.SourceDSN)
	if cachedFakeData, found, cacheErr := loadCachedFakeDataMappings(cfg.SourceDSN); cacheErr != nil {
		return config{}, cacheErr
	} else if found {
		cfg.FakeData = cachedFakeData
	} else if !sameFakeDataSource {
		cfg.FakeData = nil
	}
	cachedUnique, found, cacheErr := loadCachedFakeDataUnique(cfg.SourceDSN)
	if cacheErr != nil {
		return config{}, cacheErr
	}
	switch {
	case found:
		cfg.FakeDataUnique = cachedUnique
	case sameFakeDataSource:
		cfg.FakeDataUnique = cloneStringBoolMap(m.preservedUniqueSelectors)
	default:
		cfg.FakeDataUnique = nil
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
		cfg.Docker.BundleDir = strings.TrimSpace(m.form.DockerBundleDir)
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

func sameFakeDataSourceDSN(left, right string) bool {
	leftKey := fakeDataCacheKey(left)
	rightKey := fakeDataCacheKey(right)
	if leftKey != "" || rightKey != "" {
		return strings.EqualFold(leftKey, rightKey)
	}
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func (m *tuiModel) adoptConfig(cfg config) {
	if !sameFakeDataSourceDSN(cfg.SourceDSN, m.cfg.SourceDSN) {
		m.fakeDataEntries = nil
		m.preservedFakeData = preserveNonFullFakeData(cfg.FakeData)
		m.preservedUniqueSelectors = cloneStringBoolMap(cfg.FakeDataUnique)
	}
	m.cfg = cfg
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
	case "enter":
		cursor := m.fakeDataTable.Cursor()
		if cursor >= 0 && cursor < len(m.fakeDataEntries) {
			m.pickerTarget = cursor
			m.pickerInput.Reset()
			m.pickerInput.Focus()
			m.screen = tuiScreenFakerPicker
			m.status = "Select a supported gofakeit function."
		}
		return m, nil
	case "x", "delete":
		cursor := m.fakeDataTable.Cursor()
		if cursor >= 0 && cursor < len(m.fakeDataEntries) {
			m.fakeDataEntries[cursor].FunctionName = ""
			m.fakeDataEntries[cursor].FunctionDisplay = ""
			m.fakeDataEntries[cursor].FunctionParams = nil
			m.rebuildFakeDataTable()
			m.status = "Cleared the faker selection for the active column."
		}
		return m, nil
	case "u":
		cursor := m.fakeDataTable.Cursor()
		if cursor >= 0 && cursor < len(m.fakeDataEntries) && m.fakeDataEntries[cursor].FunctionName != "" {
			if !m.fakeDataEntries[cursor].HasUnique {
				m.status = "This column does not have a unique constraint or index."
				return m, nil
			}
			m.fakeDataEntries[cursor].RequireUnique = !m.fakeDataEntries[cursor].RequireUnique
			m.preservedUniqueSelectors[m.fakeDataEntries[cursor].Selector] = m.fakeDataEntries[cursor].RequireUnique
			m.rebuildFakeDataTable()
			if m.fakeDataEntries[cursor].RequireUnique {
				m.status = "Enabled unique value generation for this column."
			} else {
				m.status = "Disabled unique value generation for this column."
			}
		}
		return m, nil
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

	var cmd tea.Cmd
	m.fakeDataTable, cmd = m.fakeDataTable.Update(msg)
	return m, cmd
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
	if len(m.fakeDataEntries) == 0 {
		return "  No columns found.\n\n" + helpStyle.Render("  Press 'q' to go back.")
	}

	var b strings.Builder
	b.WriteString(m.fakeDataTable.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  up/dn navigate  |  enter pick faker  |  u toggle unique  |  x clear  |  a auto-select LLM  |  s/q back"))
	return b.String()
}

func (m tuiModel) updateFakerPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	filtered := m.filteredFakeFunctions()

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = tuiScreenFakeData
		m.pickerInput.Blur()
		m.status = "Canceled faker selection."
		return m, nil
	case "up":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
		return m, nil
	case "down":
		if m.pickerCursor < len(filtered)-1 {
			m.pickerCursor++
		}
		return m, nil
	case "enter":
		if m.pickerCursor >= 0 && m.pickerCursor < len(filtered) {
			selected := filtered[m.pickerCursor]
			entry := &m.fakeDataEntries[m.pickerTarget]
			if len(selected.Params) == 0 {
				entry.FunctionName = selected.LookupName
				entry.FunctionDisplay = selected.Display
				entry.FunctionParams = nil
				m.screen = tuiScreenFakeData
				m.rebuildFakeDataTable()
				m.status = fmt.Sprintf("Assigned %s to %s.", selected.Display, entry.Display)
				return m, nil
			}
			m.paramTarget = m.pickerTarget
			m.paramOption = selected
			m.paramInput.SetValue(initialParamInput(selected, m.fakeDataEntries[m.pickerTarget]))
			m.paramInput.Focus()
			m.screen = tuiScreenFakerParams
			m.status = fmt.Sprintf("Set parameters for %s.", selected.Display)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.pickerInput, cmd = m.pickerInput.Update(msg)
	m.pickerCursor = 0
	return m, cmd
}

func (m tuiModel) fakerPickerView() string {
	var b strings.Builder

	if m.pickerTarget >= 0 && m.pickerTarget < len(m.fakeDataEntries) {
		entry := m.fakeDataEntries[m.pickerTarget]
		b.WriteString(sectionHeaderStyle.Render(fmt.Sprintf("  Column: %s  (%s)", entry.Display, entry.TypeName)))
		b.WriteString("\n")
	}

	b.WriteString("  " + m.pickerInput.View())
	b.WriteString("\n\n")

	filtered := m.filteredFakeFunctions()
	if len(filtered) == 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("  No faker matches the current filter."))
		return b.String()
	}

	visible := max(8, m.height-12)
	end := min(visible, len(filtered))

	for i := range end {
		opt := filtered[i]
		cursor := "  "
		if i == m.pickerCursor {
			cursor = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true).Render("> ")
		}
		name := lipgloss.NewStyle().Foreground(colorAccent).Width(30).Render(opt.Display)
		desc := lipgloss.NewStyle().Foreground(colorMuted).Render(fmt.Sprintf("%-14s %d params", opt.Category, len(opt.Params)))
		fmt.Fprintf(&b, "  %s%s %s\n", cursor, name, desc)
	}

	if end < len(filtered) {
		more := lipgloss.NewStyle().Foreground(colorMuted).Render(fmt.Sprintf("... and %d more functions", len(filtered)-end))
		fmt.Fprintf(&b, "\n  %s\n", more)
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  type to filter  |  up/dn navigate  |  enter select  |  esc cancel"))
	return b.String()
}

func (m tuiModel) updateFakerParams(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.screen = tuiScreenFakeData
		m.paramInput.Blur()
		m.status = "Canceled faker parameter editing."
		return m, nil
	case "enter":
		entry := &m.fakeDataEntries[m.paramTarget]
		params := parseFakeParameterInput(m.paramInput.Value())
		functionConfig := buildFakeFunctionConfig(m.paramOption.LookupName, params)
		if _, _, err := compileFakeDataRule(entry.Selector, functionConfig); err != nil {
			m.status = err.Error()
			return m, nil
		}
		entry.FunctionName = m.paramOption.LookupName
		entry.FunctionDisplay = m.paramOption.Display
		entry.FunctionParams = params
		m.screen = tuiScreenFakeData
		m.rebuildFakeDataTable()
		m.status = fmt.Sprintf("Assigned %s to %s.", m.paramOption.Display, entry.Display)
		return m, nil
	}

	var cmd tea.Cmd
	m.paramInput, cmd = m.paramInput.Update(msg)
	return m, cmd
}

func (m tuiModel) fakerParamsView() string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render(fmt.Sprintf("  Configure parameters for %s", m.paramOption.LookupName)))
	b.WriteString("\n\n")

	for _, param := range m.paramOption.Params {
		lbl := lipgloss.NewStyle().Foreground(colorAccent).Render(param.Field + " (" + param.Type + ")")
		extra := ""
		if param.Optional {
			extra += " optional"
		}
		if param.Default != "" {
			extra += " default=" + param.Default
		}
		if len(param.Options) > 0 {
			extra += " options=" + strings.Join(param.Options, ",")
		}
		desc := lipgloss.NewStyle().Foreground(colorMuted).Render(param.Description + extra)
		fmt.Fprintf(&b, "  %s: %s\n", lbl, desc)
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "  Value: %s\n", m.paramInput.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  Type semicolon-separated parameter values and press enter. Press esc to skip."))
	return b.String()
}

func (m tuiModel) filteredFakeFunctions() []fakeFunctionOption {
	var allowedOutputs map[string]bool
	target := m.pickerTarget
	if target >= 0 && target < len(m.fakeDataEntries) {
		outputs := matchingOutputTypes(m.fakeDataEntries[target].TypeName)
		if len(outputs) > 0 {
			allowedOutputs = make(map[string]bool, len(outputs))
			for _, o := range outputs {
				allowedOutputs[o] = true
			}
		}
	}

	query := strings.TrimSpace(strings.ToLower(m.pickerInput.Value()))
	if query == "" && allowedOutputs == nil {
		return m.fakeFunctions
	}

	filtered := make([]fakeFunctionOption, 0, len(m.fakeFunctions))
	for _, option := range m.fakeFunctions {
		if allowedOutputs != nil && !allowedOutputs[option.Output] {
			continue
		}
		if query == "" || strings.Contains(option.SearchText, query) {
			filtered = append(filtered, option)
		}
	}
	return filtered
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
	dataFaker, err := newDataFaker(cfg.FakeData, cfg.FakeDataUnique)
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
			selector := normalizeFilterName(table.Schema + "." + table.Name + "." + col.Name)
			entry := tuiFakeDataEntry{
				Selector:  selector,
				Display:   table.FQTN() + "." + quoteIdent(col.Name),
				TypeName:  displayColumnType(col),
				HasUnique: columnHasUniqueConstraint(table, col),
			}
			if rule, ok := dataFaker.matchRule(table, col); ok {
				entry.FunctionName = rule.lookupName
				entry.FunctionDisplay = rule.info.Display
				entry.FunctionParams = flattenFakeParams(rule.info, rule.params)
				entry.RequireUnique = rule.requiresUnique
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
			Output:      info.Output,
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
