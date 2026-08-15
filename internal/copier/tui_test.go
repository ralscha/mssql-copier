package copier

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFormAllowsEnteringQIntoTextField(t *testing.T) {
	model := newTUIModel(config{})
	model.formFocus = formFieldSourceServer

	updated, _ := model.updateForm(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updateForm() model type = %T, want tuiModel", updated)
	}
	if got.quitting {
		t.Fatal("updateForm() marked model as quitting for text input")
	}
	if got.formInputs[formFieldSourceServer].Value() != "q" {
		t.Fatalf("source server input = %q, want q", got.formInputs[formFieldSourceServer].Value())
	}
}

func TestLeaveFakeDataEditorPersistsMappings(t *testing.T) {
	tmp := t.TempDir()
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	model := newTUIModel(config{
		ConfigPath: filepath.Join(tmp, "mssql-copier.yml"),
		SourceDSN:  "sqlserver://source",
		Workers:    2,
		BatchSize:  5000,
		Verbose:    true,
		FakeData: map[string]string{
			"name.*": "FirstName",
		},
	})
	model.screen = tuiScreenFakeData
	model.fakeDataEntries = []tuiFakeDataEntry{{
		Selector:        "dbo.users.email",
		Display:         "[dbo].[users].[email]",
		TypeName:        "nvarchar",
		FunctionName:    "email",
		FunctionDisplay: "Email",
	}}

	model.leaveFakeDataEditor()
	if model.screen != tuiScreenForm {
		t.Fatalf("screen = %v, want form", model.screen)
	}
	if got := model.cfg.FakeData["dbo.users.email"]; got != "email" {
		t.Fatalf("fake-data exact mapping = %q, want email", got)
	}
	if got := model.cfg.FakeData["name.*"]; got != "FirstName" {
		t.Fatalf("fake-data preserved mapping = %q, want FirstName", got)
	}

	cached, found, err := loadCachedFakeDataMappings("sqlserver://source")
	if err != nil {
		t.Fatalf("loadCachedFakeDataMappings() error = %v", err)
	}
	if !found {
		t.Fatal("expected cached fake-data mappings")
	}
	want := map[string]string{"dbo.users.email": "email", "name.*": "FirstName"}
	if !reflect.DeepEqual(cached, want) {
		t.Fatalf("cached fake-data mappings = %#v, want %#v", cached, want)
	}
}

func TestTUIVisibleFieldsTrackRunMode(t *testing.T) {
	model := newTUIModel(config{DropExisting: true})

	if !model.isFormFieldVisible(formFieldTargetServer) || !model.isFormFieldVisible(formFieldReportPath) || !model.isFormFieldVisible(formFieldEditFakeData) {
		t.Fatal("copy mode should show target, report, and fake-data fields")
	}
	if model.isFormFieldVisible(formFieldExportDDLPath) {
		t.Fatal("copy mode should hide ddl export path")
	}

	model.form.RunMode = tuiRunModePlan
	if model.isFormFieldVisible(formFieldTargetServer) || model.isFormFieldVisible(formFieldReportPath) || model.isFormFieldVisible(formFieldEditFakeData) {
		t.Fatal("plan mode should hide target, report, and fake-data fields")
	}
	if !model.isFormFieldVisible(formFieldDropExisting) {
		t.Fatal("plan mode should keep drop-existing visible")
	}

	model.form.RunMode = tuiRunModeExportDDL
	if !model.isFormFieldVisible(formFieldExportDDLPath) {
		t.Fatal("ddl mode should show ddl export path")
	}
	if model.isFormFieldVisible(formFieldExportDataPath) || model.isFormFieldVisible(formFieldDropExisting) || model.isFormFieldVisible(formFieldEditFakeData) {
		t.Fatal("ddl mode should hide data-only, drop-existing, and fake-data fields")
	}

	model.form.RunMode = tuiRunModeExportDDLData
	if !model.isFormFieldVisible(formFieldExportDDLPath) || !model.isFormFieldVisible(formFieldExportDataPath) || !model.isFormFieldVisible(formFieldExportDataRows) || !model.isFormFieldVisible(formFieldEditFakeData) {
		t.Fatal("ddl+data mode should show ddl/data export and fake-data fields")
	}
	if model.isFormFieldVisible(formFieldReportPath) {
		t.Fatal("ddl+data mode should hide report path")
	}
}

func TestTUIRunModeCycleIncludesPlan(t *testing.T) {
	mode := tuiRunModeCopy
	sequence := []tuiRunMode{mode, mode.next(), mode.next().next(), mode.next().next().next(), mode.next().next().next().next()}
	want := []tuiRunMode{tuiRunModeCopy, tuiRunModePlan, tuiRunModeExportDDL, tuiRunModeExportDDLData, tuiRunModeCopy}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("run mode cycle = %#v, want %#v", sequence, want)
	}
}

func TestTUIDockerStorageCyclesToPortableBundle(t *testing.T) {
	model := newTUIModel(config{
		Workers:   2,
		BatchSize: 5000,
		Docker:    dockerTargetConfig{Enabled: true, Persistent: true},
	})
	model.formFocus = formFieldDockerPersistent

	updated, cmd := model.handleFormEnter()
	if cmd != nil {
		t.Fatal("storage change returned an unexpected command")
	}
	got := updated.(tuiModel)
	if !got.cfg.Docker.Portable || !got.cfg.Docker.Persistent {
		t.Fatalf("Docker storage = %#v, want portable", got.cfg.Docker)
	}
	if !got.isFormFieldVisible(formFieldDockerBundleDir) {
		t.Fatal("portable bundle directory should be visible")
	}
	if !strings.Contains(stripANSI(got.formView()), "Storage: portable bundle") {
		t.Fatal("form does not show portable bundle storage")
	}
}

func TestStartActionKeepsTUIRunningAndRecordsLogFile(t *testing.T) {
	tmp := t.TempDir()
	oldRun := runTUIExecution
	runTUIExecution = func(configs []config, logPath string) tea.Msg {
		if len(configs) != 1 || !configs[0].Plan {
			t.Fatalf("unexpected configs = %#v", configs)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return executionFinishedMsg{logPath: logPath, err: err}
		}
		if err := os.WriteFile(logPath, []byte("plan completed\n"), 0o600); err != nil {
			return executionFinishedMsg{logPath: logPath, err: err}
		}
		return executionFinishedMsg{logPath: logPath}
	}
	defer func() {
		runTUIExecution = oldRun
	}()

	model := newTUIModel(config{ConfigPath: filepath.Join(tmp, "mssql-copier.yml"), Workers: 2, BatchSize: 5000, Verbose: true})
	model.form.RunMode = tuiRunModePlan
	model.form.Source = sqlServerDSNForm{Server: "source-host", Database: "SourceDB"}
	model.syncFormToInputs()
	model.formFocus = formFieldStartCopy

	updated, cmd := model.handleFormEnter()
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("handleFormEnter() model type = %T, want tuiModel", updated)
	}
	if cmd == nil {
		t.Fatal("handleFormEnter() returned nil cmd")
	}
	if got.quitting {
		t.Fatal("handleFormEnter() marked model as quitting")
	}
	if !got.runInProgress {
		t.Fatal("handleFormEnter() did not mark action as running")
	}
	if !strings.HasSuffix(got.currentLogPath, "-plan.log") {
		t.Fatalf("currentLogPath = %q", got.currentLogPath)
	}

	msg := cmd()
	finished, nextCmd := got.Update(msg)
	if nextCmd != nil {
		t.Fatal("execution update returned unexpected cmd")
	}
	finishedModel, ok := finished.(tuiModel)
	if !ok {
		t.Fatalf("finished model type = %T, want tuiModel", finished)
	}
	if finishedModel.runInProgress {
		t.Fatal("runInProgress stayed true after execution finished")
	}
	if len(finishedModel.recentLogPaths) == 0 || finishedModel.recentLogPaths[0] != got.currentLogPath {
		t.Fatalf("recentLogPaths = %#v, want first path %q", finishedModel.recentLogPaths, got.currentLogPath)
	}
	if !strings.Contains(finishedModel.formView(), "Log files") {
		t.Fatal("formView() did not render log files section")
	}
	if !strings.Contains(finishedModel.formView(), got.currentLogPath) {
		t.Fatalf("formView() missing log path %q", got.currentLogPath)
	}
}

func TestStartActionIsIgnoredWhileAnotherActionIsRunning(t *testing.T) {
	model := newTUIModel(config{})
	model.runInProgress = true
	model.formFocus = formFieldStartCopy

	updated, cmd := model.handleFormEnter()
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("handleFormEnter() model type = %T, want tuiModel", updated)
	}
	if cmd != nil {
		t.Fatal("handleFormEnter() returned a command while another action was running")
	}
	if got.quitting {
		t.Fatal("handleFormEnter() marked model as quitting while another action was running")
	}
	if got.status != "An action is already running." {
		t.Fatalf("status = %q", got.status)
	}
}

func TestDDLModeFormViewShowsCompleteActionLabel(t *testing.T) {
	model := newTUIModel(config{})
	model.form.RunMode = tuiRunModeExportDDL
	model.formFocus = formFieldStartCopy

	view := model.formView()
	// The view now uses lipgloss styling with ANSI codes, so strip them for comparison.
	stripped := stripANSI(view)
	if !strings.Contains(stripped, "[ [^R] Export DDL ]") {
		t.Fatalf("formView() missing DDL action label: %q", stripped)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestLogTailLoadsFromCurrentLogPath(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "logs", "test.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("line 1\nline 2\nline 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := newTUIModel(config{})
	model.currentLogPath = logPath
	model.loadLogTail()

	if len(model.logTailLines) != 3 {
		t.Fatalf("logTailLines length = %d, want 3", len(model.logTailLines))
	}
	if model.logTailLines[0] != "line 1" {
		t.Fatalf("logTailLines[0] = %q, want line 1", model.logTailLines[0])
	}
}

func TestLogTailFallsBackToRecentLogPath(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "logs", "recent.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("recent content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := newTUIModel(config{})
	model.currentLogPath = ""
	model.recentLogPaths = []string{logPath}
	model.loadLogTail()

	if len(model.logTailLines) != 1 {
		t.Fatalf("logTailLines length = %d, want 1", len(model.logTailLines))
	}
	if model.logTailLines[0] != "recent content" {
		t.Fatalf("logTailLines[0] = %q, want recent content", model.logTailLines[0])
	}
}

func TestLogTailEmptyWhenNoLogPath(t *testing.T) {
	model := newTUIModel(config{})
	model.currentLogPath = ""
	model.recentLogPaths = nil
	model.loadLogTail()

	if model.logTailLines != nil {
		t.Fatalf("logTailLines = %#v, want nil", model.logTailLines)
	}
}

func TestLogTailViewShowsContent(t *testing.T) {
	model := newTUIModel(config{})
	model.width = 80
	model.height = 50
	model.logTailLines = []string{"log line one", "log line two"}
	model.logTailScroll = 0

	view := model.logTailView()
	if !strings.Contains(view, "log line one") {
		t.Fatalf("logTailView() missing log content: %q", view)
	}
	if !strings.Contains(view, "Log tail") {
		t.Fatalf("logTailView() missing header: %q", view)
	}
}

func TestLogTailScrolling(t *testing.T) {
	model := newTUIModel(config{})
	model.height = 50
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i)
	}
	model.logTailLines = lines
	model.logTailScroll = 0

	model.scrollLogTail(5)
	if model.logTailScroll != 5 {
		t.Fatalf("logTailScroll = %d, want 5", model.logTailScroll)
	}

	model.scrollLogTail(-3)
	if model.logTailScroll != 2 {
		t.Fatalf("logTailScroll = %d, want 2", model.logTailScroll)
	}

	// Scroll past end should clamp.
	model.logTailScroll = 95
	model.scrollLogTail(20)
	visible := model.visibleLogTailRows()
	maxScroll := max(0, len(model.logTailLines)-visible)
	if model.logTailScroll != maxScroll {
		t.Fatalf("logTailScroll = %d, want maxScroll %d", model.logTailScroll, maxScroll)
	}
}

func TestLogPanelFocusTogglesWithCtrlL(t *testing.T) {
	model := newTUIModel(config{})
	model.screen = tuiScreenForm

	if model.logPanelFocused {
		t.Fatal("log panel should not be focused initially")
	}

	updated, _ := model.updateForm(tea.KeyPressMsg(tea.Key{Text: "ctrl+l"}))
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updateForm() model type = %T, want tuiModel", updated)
	}
	if !got.logPanelFocused {
		t.Fatal("ctrl+l did not focus log panel")
	}

	// Esc in log panel focus returns to form.
	updated2, _ := got.updateLogPanelFocus(tea.KeyPressMsg(tea.Key{Text: "esc"}))
	got2, ok := updated2.(tuiModel)
	if !ok {
		t.Fatalf("updateLogPanelFocus() model type = %T, want tuiModel", updated2)
	}
	if got2.logPanelFocused {
		t.Fatal("esc did not defocus log panel")
	}
}

func TestFormViewIncludesLogTail(t *testing.T) {
	model := newTUIModel(config{})
	model.width = 80
	model.height = 50
	model.logTailLines = []string{"sample log output"}
	model.screen = tuiScreenForm

	view := model.formView()
	if !strings.Contains(view, "Log tail") {
		t.Fatalf("formView() missing log tail panel: %q", view)
	}
	if !strings.Contains(view, "sample log output") {
		t.Fatalf("formView() missing log content: %q", view)
	}
}

func TestExecutionFinishedLoadsLogTail(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "logs", "exec-finished.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("execution completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	model := newTUIModel(config{})
	model.runInProgress = true
	model.currentLogPath = logPath
	model.screen = tuiScreenForm

	msg := executionFinishedMsg{logPath: logPath}
	updated, _ := model.Update(msg)
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("Update() model type = %T, want tuiModel", updated)
	}
	if got.runInProgress {
		t.Fatal("runInProgress stayed true after execution finished")
	}
	if len(got.logTailLines) != 1 || got.logTailLines[0] != "execution completed" {
		t.Fatalf("logTailLines = %#v, want [execution completed]", got.logTailLines)
	}
}
