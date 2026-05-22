package copier

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFormAllowsEnteringQIntoTextField(t *testing.T) {
	model := newTUIModel(config{})
	model.formFocus = formFieldSourceServer

	updated, cmd := model.updateForm(tea.KeyPressMsg(tea.Key{Text: "q", Code: 'q'}))
	got, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updateForm() model type = %T, want tuiModel", updated)
	}
	if cmd != nil {
		t.Fatal("updateForm() returned quit command for text input")
	}
	if got.quitting {
		t.Fatal("updateForm() marked model as quitting for text input")
	}
	if got.form.Source.Server != "q" {
		t.Fatalf("source server = %q, want q", got.form.Source.Server)
	}
}

func TestLeaveFakeDataEditorPersistsMappings(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mssql-copier.yml")
	previousExecutablePath := executablePath
	executablePath = func() (string, error) {
		return filepath.Join(tmp, "bin", "mssql-copier.exe"), nil
	}
	defer func() {
		executablePath = previousExecutablePath
	}()

	model := newTUIModel(config{
		ConfigPath: path,
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

	cmd := model.leaveFakeDataEditor(false)
	if model.screen != tuiScreenForm {
		t.Fatalf("screen = %v, want form", model.screen)
	}
	if got := model.cfg.FakeData["dbo.users.email"]; got != "email" {
		t.Fatalf("fake-data exact mapping = %q, want email", got)
	}
	if got := model.cfg.FakeData["name.*"]; got != "FirstName" {
		t.Fatalf("fake-data preserved mapping = %q, want FirstName", got)
	}
	if cmd == nil {
		t.Fatal("leaveFakeDataEditor() returned nil cmd")
	}

	msg := cmd()
	exported, ok := msg.(configExportedMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want configExportedMsg", msg)
	}
	if exported.err != nil {
		t.Fatalf("config export error = %v", exported.err)
	}

	yamlCfg, loaded, err := loadYAMLConfig(path, true)
	if err != nil {
		t.Fatalf("loadYAMLConfig() error = %v", err)
	}
	if !loaded {
		t.Fatal("expected persisted config to load")
	}

	var got config
	yamlCfg.applyTo(&got)
	if got.FakeData != nil {
		t.Fatalf("fake-data should not be exported to YAML, got %#v", got.FakeData)
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
	if !strings.Contains(view, "> [ Export DDL ]") {
		t.Fatalf("formView() missing DDL action label: %q", view)
	}
}
