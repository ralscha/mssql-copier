package copier

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/brianvoe/gofakeit/v7"
)

type tuiScreen int

const (
	tuiScreenForm tuiScreen = iota
	tuiScreenLoadingFakeData
	tuiScreenFakeData
	tuiScreenFakerPicker
	tuiScreenFakerParams
	tuiScreenAutoSelecting
)

const (
	formFieldSource = iota
	formFieldTarget
	formFieldWorkers
	formFieldBatchSize
	formFieldVerbose
	formFieldDropExisting
	formFieldIncludeSchemas
	formFieldExcludeSchemas
	formFieldIncludeTables
	formFieldExcludeTables
	formFieldExportPath
	formFieldEditFakeData
	formFieldExportConfig
	formFieldStartCopy
	formFieldCount
)

type tuiFormState struct {
	SourceDSN      string
	TargetDSN      string
	Workers        string
	BatchSize      string
	IncludeSchemas string
	ExcludeSchemas string
	IncludeTables  string
	ExcludeTables  string
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
	submitted         bool
	quitting          bool
}

func runTUI(cfg config) error {
	program := tea.NewProgram(newTUIModel(cfg))
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	model, ok := finalModel.(tuiModel)
	if !ok {
		return fmt.Errorf("unexpected tui model type %T", finalModel)
	}
	if !model.submitted {
		return nil
	}
	return executeConfig(model.cfg)
}

func newTUIModel(cfg config) tuiModel {
	exportPath := strings.TrimSpace(cfg.ConfigPath)
	if exportPath == "" {
		exportPath = defaultConfigPath
	}
	return tuiModel{
		cfg: cfg,
		form: tuiFormState{
			SourceDSN:      cfg.SourceDSN,
			TargetDSN:      cfg.TargetDSN,
			Workers:        strconv.Itoa(max(1, cfg.Workers)),
			BatchSize:      strconv.Itoa(max(1, cfg.BatchSize)),
			IncludeSchemas: strings.Join(cfg.IncludeSchemas, ","),
			ExcludeSchemas: strings.Join(cfg.ExcludeSchemas, ","),
			IncludeTables:  strings.Join(cfg.IncludeTables, ","),
			ExcludeTables:  strings.Join(cfg.ExcludeTables, ","),
			ExportPath:     exportPath,
		},
		screen:            tuiScreenForm,
		formFocus:         formFieldSource,
		width:             100,
		height:            30,
		fakeFunctions:     availableFakeFunctionOptions(),
		preservedFakeData: preserveNonFullFakeData(cfg.FakeData),
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
	rows := []string{
		m.formTextRow(formFieldSource, "Source DSN", m.form.SourceDSN),
		m.formTextRow(formFieldTarget, "Target DSN", m.form.TargetDSN),
		m.formTextRow(formFieldWorkers, "Workers", m.form.Workers),
		m.formTextRow(formFieldBatchSize, "Batch size", m.form.BatchSize),
		m.formBoolRow(formFieldVerbose, "Verbose", m.cfg.Verbose),
		m.formBoolRow(formFieldDropExisting, "Drop existing", m.cfg.DropExisting),
		m.formTextRow(formFieldIncludeSchemas, "Include schemas", m.form.IncludeSchemas),
		m.formTextRow(formFieldExcludeSchemas, "Exclude schemas", m.form.ExcludeSchemas),
		m.formTextRow(formFieldIncludeTables, "Include tables", m.form.IncludeTables),
		m.formTextRow(formFieldExcludeTables, "Exclude tables", m.form.ExcludeTables),
		m.formTextRow(formFieldExportPath, "Config path", m.form.ExportPath),
		m.formActionRow(formFieldEditFakeData, fmt.Sprintf("Edit fake data (%d exact rules)", countExactFullFakeDataRules(m.cfg.FakeData))),
		m.formActionRow(formFieldExportConfig, "Export YAML config"),
		m.formActionRow(formFieldStartCopy, "Start copy"),
	}

	return strings.Join([]string{
		"Enter source and target parameters here, including include/exclude schema and table filters.",
		"Keys: type to edit, backspace deletes, up/down or tab moves, enter toggles/actions, q quits.",
		"",
		strings.Join(rows, "\n"),
	}, "\n")
}

func (m tuiModel) formTextRow(index int, label string, value string) string {
	prefix := "  "
	if m.formFocus == index {
		prefix = "> "
	}
	return fmt.Sprintf("%s%-16s %s", prefix, label+":", value)
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
	return marker + "[ " + label + " ]"
}

func (m tuiModel) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "tab", "down", "j":
		m.formFocus = (m.formFocus + 1) % formFieldCount
		return m, nil
	case "shift+tab", "up", "k":
		m.formFocus = (m.formFocus + formFieldCount - 1) % formFieldCount
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

func (m tuiModel) handleFormEnter() (tea.Model, tea.Cmd) {
	switch m.formFocus {
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
		cfg, err := m.configFromForm(true, true)
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.cfg = cfg
		m.submitted = true
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *tuiModel) appendFormText(text string) {
	switch m.formFocus {
	case formFieldSource:
		m.form.SourceDSN += text
	case formFieldTarget:
		m.form.TargetDSN += text
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
	case formFieldExportPath:
		m.form.ExportPath += text
	}
	m.status = ""
}

func (m *tuiModel) deleteFormText() {
	switch m.formFocus {
	case formFieldSource:
		m.form.SourceDSN = trimLastRune(m.form.SourceDSN)
	case formFieldTarget:
		m.form.TargetDSN = trimLastRune(m.form.TargetDSN)
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
	case formFieldExportPath:
		m.form.ExportPath = trimLastRune(m.form.ExportPath)
	}
}

func (m tuiModel) configFromForm(requireSource bool, requireTarget bool) (config, error) {
	cfg := m.cfg
	cfg.SourceDSN = strings.TrimSpace(m.form.SourceDSN)
	cfg.TargetDSN = strings.TrimSpace(m.form.TargetDSN)
	cfg.IncludeSchemas = parseList(m.form.IncludeSchemas)
	cfg.ExcludeSchemas = parseList(m.form.ExcludeSchemas)
	cfg.IncludeTables = parseList(m.form.IncludeTables)
	cfg.ExcludeTables = parseList(m.form.ExcludeTables)
	cfg.ConfigPath = strings.TrimSpace(m.form.ExportPath)
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
	cfg.Workers = workers
	cfg.BatchSize = batchSize
	if requireSource && cfg.SourceDSN == "" {
		return config{}, fmt.Errorf("source DSN is required")
	}
	if requireTarget && cfg.TargetDSN == "" {
		return config{}, fmt.Errorf("target DSN is required")
	}
	return cfg, nil
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
		m.syncFakeDataIntoConfig()
		m.screen = tuiScreenForm
		return m, nil
	case "down", "j":
		if m.fakeDataCursor < count-1 {
			m.fakeDataCursor++
		}
	case "up", "k":
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
			m.status = "LLM auto-select is not configured."
			return m, nil
		}
		m.screen = tuiScreenAutoSelecting
		m.status = "Requesting faker suggestions from the configured LLM..."
		return m, autoSelectFakeDataCmd(m.cfg.LLM, m.fakeDataEntries, m.fakeFunctions)
	case "s":
		m.syncFakeDataIntoConfig()
		m.screen = tuiScreenForm
		m.status = fmt.Sprintf("Saved %d exact fake-data rules.", countExactFullFakeDataRules(m.cfg.FakeData))
		return m, nil
	}

	m.adjustFakeDataOffset()
	return m, nil
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
	case "down", "j":
		if m.pickerCursor < len(filtered)-1 {
			m.pickerCursor++
		}
	case "up", "k":
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
		return
	}
	m.cfg.FakeData = merged
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

func loadFakeDataEntries(cfg config) ([]tuiFakeDataEntry, error) {
	dataFaker, err := newDataFaker(cfg.FakeData)
	if err != nil {
		return nil, err
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
