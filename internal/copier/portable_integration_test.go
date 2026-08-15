package copier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPortableDockerBundleIntegration(t *testing.T) {
	if os.Getenv("COPY_MSSQL_RUN_PORTABLE_INTEGRATION") == "" {
		t.Skip("set COPY_MSSQL_RUN_PORTABLE_INTEGRATION=1 to run the portable bundle integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	testRoot := t.TempDir()
	builtExecutable := strings.TrimSpace(os.Getenv("COPY_MSSQL_PORTABLE_TEST_EXECUTABLE"))
	if builtExecutable == "" {
		builtExecutable = buildPortableTestExecutable(ctx, t, testRoot)
	}
	if info, err := os.Stat(builtExecutable); err != nil || info.IsDir() {
		t.Fatalf("inspect test executable %q: info=%v err=%v", builtExecutable, info, err)
	}

	composeDir := filepath.Join(testRoot, "copy-project")
	bundleDir := filepath.Join(testRoot, "portable-bundle")
	port := availableTCPPort(t)
	password := "Portable_Test_Passw0rd!"
	cfg := config{
		SourceDSN:    "set after source startup",
		Workers:      2,
		BatchSize:    1000,
		Verbose:      false,
		DropExisting: true,
		Docker: dockerTargetConfig{
			Enabled:    true,
			Persistent: true,
			Portable:   true,
			ComposeDir: composeDir,
			BundleDir:  bundleDir,
			Port:       port,
			SAPassword: password,
		},
	}

	runner := &portableRunner{stdout: io.Discard, stderr: io.Discard}
	defer cleanupPortableIntegrationProject(t, runner, dockerComposePath(composeDir))
	defer cleanupPortableIntegrationProject(t, runner, filepath.Join(bundleDir, "docker-compose.yml"))

	sourceName := fmt.Sprintf("mssql-copier-portable-source-%d", time.Now().UnixNano())
	sourceContainer, sourceMasterDSN := startSQLServerContainer(ctx, t, sourceName)
	defer terminateContainer(context.Background(), t, sourceContainer)

	const databaseName = "PortableFeatureIT"
	sourceDSN, err := sqlServerDSNWithDatabase(sourceMasterDSN, databaseName)
	if err != nil {
		t.Fatal(err)
	}
	sourceMasterDB, err := openTestDB(ctx, sourceMasterDSN, 4)
	if err != nil {
		t.Fatalf("wait for authenticated source connection: %v", err)
	}
	if _, err := sourceMasterDB.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(databaseName)); err != nil {
		sourceMasterDB.Close()
		t.Fatalf("create source test database: %v", err)
	}
	sourceMasterDB.Close()
	cfg.SourceDSN = sourceDSN
	sourceDB, err := openTestDB(ctx, sourceDSN, 4)
	if err != nil {
		t.Fatalf("open source test database: %v", err)
	}
	defer sourceDB.Close()

	seed := []string{
		"CREATE SCHEMA [portable_it]",
		"CREATE TABLE [portable_it].[customers] ([id] int IDENTITY(1,1) NOT NULL CONSTRAINT [PK_portable_customers] PRIMARY KEY, [name] nvarchar(100) NOT NULL, [city] nvarchar(100) NULL)",
		"CREATE TABLE [portable_it].[orders] ([id] int NOT NULL CONSTRAINT [PK_portable_orders] PRIMARY KEY, [customer_id] int NOT NULL, [amount] decimal(12,2) NOT NULL, [payload] varbinary(32) NULL, CONSTRAINT [FK_portable_orders_customer] FOREIGN KEY ([customer_id]) REFERENCES [portable_it].[customers]([id]))",
		"SET IDENTITY_INSERT [portable_it].[customers] ON; INSERT INTO [portable_it].[customers] ([id], [name], [city]) VALUES (1, N'Ada Lovelace', N'London'), (2, N'Grüezi 東京', N'Zürich'); SET IDENTITY_INSERT [portable_it].[customers] OFF",
		"INSERT INTO [portable_it].[orders] ([id], [customer_id], [amount], [payload]) VALUES (10, 1, 12.50, 0x010203), (20, 2, 99.95, 0xDEADBEEF), (30, 2, 7.25, NULL)",
		"CREATE VIEW [portable_it].[customer_totals] AS SELECT c.[id], c.[name], COUNT_BIG(*) AS [order_count], SUM(o.[amount]) AS [total_amount] FROM [portable_it].[customers] c JOIN [portable_it].[orders] o ON o.[customer_id] = c.[id] GROUP BY c.[id], c.[name]",
		"CREATE PROCEDURE [portable_it].[get_order_count] AS BEGIN SET NOCOUNT ON; SELECT COUNT(*) AS [order_count] FROM [portable_it].[orders]; END",
	}
	for _, statement := range seed {
		if _, err := sourceDB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed source database: %v\nstatement: %s", err, statement)
		}
	}

	previousExecutablePath := executablePath
	executablePath = func() (string, error) { return builtExecutable, nil }
	defer func() { executablePath = previousExecutablePath }()

	if err := executeConfig(cfg); err != nil {
		t.Fatalf("copy database and create portable bundle: %v", err)
	}

	manifestPath := filepath.Join(bundleDir, portableManifestName)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	var manifest portableManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	if manifest.FormatVersion != 1 || manifest.Database != databaseName || manifest.Service != portableServiceName {
		t.Fatalf("generated manifest = %#v", manifest)
	}
	archivePath := filepath.Join(bundleDir, portableArchiveName)
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("inspect generated archive: %v", err)
	}
	if archiveInfo.Size() == 0 {
		t.Fatal("generated archive is empty")
	}
	actualChecksum, err := portableFileSHA256(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(actualChecksum, manifest.ArchiveSHA256) {
		t.Fatalf("archive checksum = %s, manifest = %s", actualChecksum, manifest.ArchiveSHA256)
	}
	bundledExecutable := filepath.Join(bundleDir, manifest.BundledCLIFile)
	if bundledHash, err := portableFileSHA256(bundledExecutable); err != nil {
		t.Fatalf("hash bundled executable: %v", err)
	} else if sourceHash, err := portableFileSHA256(builtExecutable); err != nil {
		t.Fatalf("hash source executable: %v", err)
	} else if bundledHash != sourceHash {
		t.Fatalf("bundled executable hash = %s, source executable hash = %s", bundledHash, sourceHash)
	}

	copyContainerID, err := portableComposeContainerID(ctx, runner, dockerComposePath(composeDir), portableServiceName)
	if err != nil {
		t.Fatalf("find copy target after bundling: %v", err)
	}
	copyInfo, err := inspectDockerContainer(ctx, runner, copyContainerID)
	if err != nil {
		t.Fatalf("inspect copy target after bundling: %v", err)
	}
	if !copyInfo.State.Running {
		t.Fatal("copy target was not restarted after the portable snapshot")
	}

	if err := runner.run(ctx, "docker", portableComposeArgs(dockerComposePath(composeDir), "down")...); err != nil {
		t.Fatalf("stop original copy target before isolated restore: %v", err)
	}
	restoreOutput, restoreErr := runBundledRestore(ctx, bundledExecutable, bundleDir)
	if restoreErr != nil {
		t.Fatalf("restore generated bundle: %v\n%s", restoreErr, restoreOutput)
	}
	if !strings.Contains(restoreOutput, "Restore complete.") {
		t.Fatalf("restore output did not report completion:\n%s", restoreOutput)
	}

	restoredDSN := dockerComposeDSN(cfg.Docker)
	restoredDSN, err = sqlServerDSNWithDatabase(restoredDSN, databaseName)
	if err != nil {
		t.Fatal(err)
	}
	restoredDB, err := openTestDB(ctx, restoredDSN, 4)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer restoredDB.Close()

	var customerCount, orderCount, orderIDSum int
	if err := restoredDB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM [portable_it].[customers]), (SELECT COUNT(*) FROM [portable_it].[orders]), (SELECT SUM([id]) FROM [portable_it].[orders])").Scan(&customerCount, &orderCount, &orderIDSum); err != nil {
		t.Fatalf("verify restored row counts: %v", err)
	}
	if customerCount != 2 || orderCount != 3 || orderIDSum != 60 {
		t.Fatalf("restored counts customers=%d orders=%d order-id-sum=%d", customerCount, orderCount, orderIDSum)
	}
	var name, payloadHex string
	if err := restoredDB.QueryRowContext(ctx, "SELECT c.[name], CONVERT(varchar(64), o.[payload], 2) FROM [portable_it].[customers] c JOIN [portable_it].[orders] o ON o.[customer_id] = c.[id] WHERE o.[id] = 20").Scan(&name, &payloadHex); err != nil {
		t.Fatalf("verify restored Unicode and binary data: %v", err)
	}
	if name != "Grüezi 東京" || payloadHex != "DEADBEEF" {
		t.Fatalf("restored data name=%q payload=%q", name, payloadHex)
	}
	var viewRows, procedureRows, foreignKeys int
	if err := restoredDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM [portable_it].[customer_totals]").Scan(&viewRows); err != nil {
		t.Fatalf("query restored view: %v", err)
	}
	if err := restoredDB.QueryRowContext(ctx, "EXEC [portable_it].[get_order_count]").Scan(&procedureRows); err != nil {
		t.Fatalf("execute restored procedure: %v", err)
	}
	if err := restoredDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sys.foreign_keys WHERE name = N'FK_portable_orders_customer'").Scan(&foreignKeys); err != nil {
		t.Fatalf("verify restored foreign key: %v", err)
	}
	if viewRows != 2 || procedureRows != 3 || foreignKeys != 1 {
		t.Fatalf("restored objects view-rows=%d procedure-rows=%d foreign-keys=%d", viewRows, procedureRows, foreignKeys)
	}

	restoreContainerID, err := portableComposeContainerID(ctx, runner, filepath.Join(bundleDir, "docker-compose.yml"), portableServiceName)
	if err != nil {
		t.Fatalf("find restored container: %v", err)
	}
	restoreInfo, err := inspectDockerContainer(ctx, runner, restoreContainerID)
	if err != nil {
		t.Fatalf("inspect restored container: %v", err)
	}
	restoredVolume, err := namedVolumeAt(restoreInfo, portableContainerPath)
	if err != nil {
		t.Fatal(err)
	}
	if restoredVolume == manifest.SourceVolume {
		t.Fatalf("restore reused source volume %q instead of creating an isolated destination volume", restoredVolume)
	}

	if _, err := restoredDB.ExecContext(ctx, "INSERT INTO [portable_it].[orders] ([id], [customer_id], [amount]) VALUES (999, 1, 1.00)"); err != nil {
		t.Fatalf("add destination-only sentinel row: %v", err)
	}
	if output, err := runBundledRestore(ctx, bundledExecutable, bundleDir); err == nil || !strings.Contains(output+err.Error(), "not empty") {
		t.Fatalf("second restore should reject non-empty volume: err=%v\n%s", err, output)
	}
	forceOutput, err := runBundledRestore(ctx, bundledExecutable, bundleDir, "--force")
	if err != nil {
		t.Fatalf("force restore generated bundle: %v\n%s", err, forceOutput)
	}
	restoredDB.Close()
	restoredDB, err = openTestDB(ctx, restoredDSN, 4)
	if err != nil {
		t.Fatalf("reopen database after force restore: %v", err)
	}
	defer restoredDB.Close()
	if err := restoredDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM [portable_it].[orders]").Scan(&orderCount); err != nil {
		t.Fatalf("count rows after force restore: %v", err)
	}
	if orderCount != 3 {
		t.Fatalf("force restore retained destination-only data; order count = %d", orderCount)
	}

	if err := executeConfig(cfg); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("repeat portable copy should refuse to overwrite its bundle: %v", err)
	}
	archive, err := os.OpenFile(archivePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open archive for checksum safety test: %v", err)
	}
	if _, err := archive.Write([]byte("tampered")); err != nil {
		archive.Close()
		t.Fatalf("tamper archive: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if output, err := runBundledRestore(ctx, bundledExecutable, bundleDir); err == nil || !strings.Contains(output+err.Error(), "checksum mismatch") {
		t.Fatalf("tampered bundle should fail checksum validation: err=%v\n%s", err, output)
	}
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release test port: %v", err)
	}
	return port
}

func runBundledRestore(ctx context.Context, executable, bundleDir string, extraArgs ...string) (string, error) {
	args := []string{"restore", "--bundle", bundleDir}
	args = append(args, extraArgs...)
	// #nosec G204 -- executable is a test-built mssql-copier path and arguments are passed without a shell.
	cmd := exec.CommandContext(ctx, executable, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func cleanupPortableIntegrationProject(t *testing.T, runner *portableRunner, composeFile string) {
	t.Helper()
	if _, err := os.Stat(composeFile); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runner.run(ctx, "docker", portableComposeArgs(composeFile, "down", "--volumes", "--remove-orphans")...); err != nil {
		t.Errorf("clean up portable integration project %q: %v", composeFile, err)
	}
}

func buildPortableTestExecutable(ctx context.Context, t *testing.T, outputDir string) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	executableName := "mssql-copier-portable-it"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	outputPath := filepath.Join(outputDir, executableName)
	// #nosec G204 -- the Go executable and package path are fixed; outputPath is test-owned.
	command := exec.CommandContext(ctx, "go", "build", "-o", outputPath, "./cmd/mssql-copier")
	command.Dir = repositoryRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build portable integration executable: %v\n%s", err, output)
	}
	return outputPath
}
