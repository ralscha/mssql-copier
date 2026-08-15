package copier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	portableManifestName  = "manifest.json"
	portableArchiveName   = "mssql_data.tar.gz"
	portableServiceName   = "mssql"
	portableContainerPath = "/var/opt/mssql"
	portableHelperImage   = "alpine:3.22"
)

type portableManifest struct {
	FormatVersion  int       `json:"format_version"`
	CreatedAt      time.Time `json:"created_at"`
	Service        string    `json:"service"`
	Database       string    `json:"database,omitempty"`
	ContainerPath  string    `json:"container_path"`
	ComposeFile    string    `json:"compose_file"`
	ArchiveFile    string    `json:"archive_file"`
	ArchiveSHA256  string    `json:"archive_sha256"`
	SourceVolume   string    `json:"source_volume"`
	HelperImage    string    `json:"helper_image"`
	BundledCLIFile string    `json:"bundled_cli_file,omitempty"`
}

type dockerContainerInfo struct {
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

type portableRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func validatePortableBundleDestination(cfg dockerTargetConfig) error {
	composeDir, err := filepath.Abs(filepath.Dir(dockerComposePath(cfg.ComposeDir)))
	if err != nil {
		return fmt.Errorf("resolve Compose directory: %w", err)
	}
	bundleDir, err := filepath.Abs(portableBundleDir(cfg.BundleDir))
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	if pathsOverlap(composeDir, bundleDir) {
		return errors.New("bundle directory and Compose directory must not overlap")
	}
	return requireEmptyOrMissingDirectory(bundleDir)
}

func portableBundleDir(configured string) string {
	dir := strings.TrimSpace(configured)
	if dir == "" {
		return defaultDockerBundleDir
	}
	return dir
}

func sameLocalPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathsOverlap(left, right string) bool {
	return pathWithin(left, right) || pathWithin(right, left)
}

func pathWithin(parent, child string) bool {
	if sameLocalPath(parent, child) {
		return true
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func createPortableDockerBundle(ctx context.Context, cfg dockerTargetConfig, database string) error {
	if err := validatePortableBundleDestination(cfg); err != nil {
		return err
	}
	if err := requireCommand("docker"); err != nil {
		return err
	}

	composeFile, err := filepath.Abs(dockerComposePath(cfg.ComposeDir))
	if err != nil {
		return fmt.Errorf("resolve Compose file: %w", err)
	}
	outputDir, err := filepath.Abs(portableBundleDir(cfg.BundleDir))
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	runner := &portableRunner{stdout: os.Stdout, stderr: os.Stderr}

	containerID, err := portableComposeContainerID(ctx, runner, composeFile, portableServiceName)
	if err != nil {
		return err
	}
	info, err := inspectDockerContainer(ctx, runner, containerID)
	if err != nil {
		return err
	}
	if !info.State.Running {
		return fmt.Errorf("Compose service %q must be running before it can be bundled", portableServiceName)
	}
	volumeName, err := namedVolumeAt(info, portableContainerPath)
	if err != nil {
		return err
	}
	if err := ensureHelperImage(ctx, runner, portableHelperImage); err != nil {
		return err
	}

	parent := filepath.Dir(outputDir)
	// #nosec G301 -- the bundle parent is a user-selected local output path.
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create bundle parent directory: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".mssql-copier-bundle-*")
	if err != nil {
		return fmt.Errorf("create bundle staging directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()

	composeBase := filepath.Base(composeFile)
	if err := copyPortableFile(composeFile, filepath.Join(staging, composeBase), 0); err != nil {
		return fmt.Errorf("copy Compose file: %w", err)
	}

	executable, err := executablePath()
	if err != nil {
		return fmt.Errorf("find current executable: %w", err)
	}
	bundledCLI := "mssql-copier"
	if runtime.GOOS == "windows" {
		bundledCLI += ".exe"
	}
	if err := copyPortableFile(executable, filepath.Join(staging, bundledCLI), 0o755); err != nil {
		return fmt.Errorf("copy mssql-copier into bundle: %w", err)
	}

	log.Printf("docker: stopping service %q for a consistent portable snapshot...", portableServiceName)
	if err := runner.run(ctx, "docker", portableComposeArgs(composeFile, "stop", portableServiceName)...); err != nil {
		return err
	}
	stopped := true
	defer func() {
		if !stopped {
			return
		}
		restartCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		log.Printf("docker: restarting service %q after portable snapshot...", portableServiceName)
		if err := runner.run(restartCtx, "docker", portableComposeArgs(composeFile, "start", portableServiceName)...); err != nil {
			log.Printf("docker: warning: could not restart service %q: %v", portableServiceName, err)
		}
	}()

	log.Printf("docker: archiving volume %q into portable bundle...", volumeName)
	archiveArgs := []string{
		"run", "--rm",
		"--mount", "type=volume,source=" + volumeName + ",target=/volume,readonly",
		"--mount", "type=bind,source=" + staging + ",target=/backup",
		portableHelperImage, "sh", "-c", "cd /volume && tar czf /backup/" + portableArchiveName + " .",
	}
	if err := runner.run(ctx, "docker", archiveArgs...); err != nil {
		return fmt.Errorf("archive Docker volume: %w", err)
	}

	checksum, err := portableFileSHA256(filepath.Join(staging, portableArchiveName))
	if err != nil {
		return err
	}
	manifest := portableManifest{
		FormatVersion:  1,
		CreatedAt:      time.Now().UTC(),
		Service:        portableServiceName,
		Database:       database,
		ContainerPath:  portableContainerPath,
		ComposeFile:    composeBase,
		ArchiveFile:    portableArchiveName,
		ArchiveSHA256:  checksum,
		SourceVolume:   volumeName,
		HelperImage:    portableHelperImage,
		BundledCLIFile: bundledCLI,
	}
	if err := writePortableJSON(filepath.Join(staging, portableManifestName), manifest); err != nil {
		return err
	}
	if err := writePortableReadme(filepath.Join(staging, "README.txt"), bundledCLI); err != nil {
		return err
	}

	if _, err := os.Stat(outputDir); err == nil {
		if err := os.Remove(outputDir); err != nil {
			return fmt.Errorf("remove empty bundle directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect bundle directory: %w", err)
	}
	if err := os.Rename(staging, outputDir); err != nil {
		return fmt.Errorf("publish portable bundle: %w", err)
	}
	published = true
	log.Printf("docker: portable bundle created at %s", outputDir)
	log.Printf("docker: archive SHA-256: %s", checksum)
	return nil
}

func runPortableRestoreCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bundleDir := fs.String("bundle", ".", "directory containing the portable bundle")
	serviceOverride := fs.String("service", "", "override the service recorded in the manifest")
	force := fs.Bool("force", false, "erase a non-empty destination volume before restoring")
	noStart := fs.Bool("no-start", false, "leave the restored service stopped")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: mssql-copier restore [options]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return restorePortableDockerBundle(ctx, *bundleDir, *serviceOverride, *force, *noStart, &portableRunner{stdout: stdout, stderr: stderr})
}

func restorePortableDockerBundle(ctx context.Context, bundleDir, serviceOverride string, force, noStart bool, runner *portableRunner) error {
	bundleDir, err := filepath.Abs(strings.TrimSpace(bundleDir))
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	manifest, err := readPortableManifest(filepath.Join(bundleDir, portableManifestName))
	if err != nil {
		return err
	}
	if manifest.FormatVersion != 1 {
		return fmt.Errorf("unsupported portable bundle format version %d", manifest.FormatVersion)
	}
	if manifest.ArchiveFile != portableArchiveName {
		return fmt.Errorf("unsupported archive name %q", manifest.ArchiveFile)
	}
	composeFile, err := safePortableBundlePath(bundleDir, manifest.ComposeFile)
	if err != nil {
		return fmt.Errorf("invalid Compose path in manifest: %w", err)
	}
	archiveFile, err := safePortableBundlePath(bundleDir, manifest.ArchiveFile)
	if err != nil {
		return fmt.Errorf("invalid archive path in manifest: %w", err)
	}
	if manifest.ArchiveSHA256 == "" {
		return errors.New("manifest does not contain an archive checksum")
	}

	fmt.Fprintln(runner.stdout, "Verifying portable bundle checksum...")
	checksum, err := portableFileSHA256(archiveFile)
	if err != nil {
		return err
	}
	if !strings.EqualFold(checksum, manifest.ArchiveSHA256) {
		return fmt.Errorf("archive checksum mismatch: expected %s, got %s", manifest.ArchiveSHA256, checksum)
	}

	service := strings.TrimSpace(serviceOverride)
	if service == "" {
		service = manifest.Service
	}
	if service == "" || manifest.ContainerPath == "" {
		return errors.New("manifest has an empty service or container path")
	}
	helperImage := strings.TrimSpace(manifest.HelperImage)
	if helperImage == "" {
		helperImage = portableHelperImage
	}
	if err := requireCommand("docker"); err != nil {
		return err
	}
	if err := ensureHelperImage(ctx, runner, helperImage); err != nil {
		return err
	}

	fmt.Fprintf(runner.stdout, "Creating destination service %q without starting it...\n", service)
	if err := runner.run(ctx, "docker", portableComposeArgs(composeFile, "create", service)...); err != nil {
		return err
	}
	containerID, err := portableComposeContainerID(ctx, runner, composeFile, service)
	if err != nil {
		return err
	}
	info, err := inspectDockerContainer(ctx, runner, containerID)
	if err != nil {
		return err
	}
	volumeName, err := namedVolumeAt(info, manifest.ContainerPath)
	if err != nil {
		return err
	}

	wasRunning := info.State.Running
	if wasRunning {
		fmt.Fprintf(runner.stdout, "Stopping Compose service %q...\n", service)
		if err := runner.run(ctx, "docker", portableComposeArgs(composeFile, "stop", service)...); err != nil {
			return err
		}
	}
	restoreFinished := false
	defer func() {
		if !wasRunning || restoreFinished {
			return
		}
		restartCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fmt.Fprintf(runner.stdout, "Restore failed; restarting service %q...\n", service)
		if err := runner.run(restartCtx, "docker", portableComposeArgs(composeFile, "start", service)...); err != nil {
			fmt.Fprintf(runner.stderr, "Warning: could not restart service %q: %v\n", service, err)
		}
	}()

	empty, err := portableVolumeIsEmpty(ctx, runner, helperImage, volumeName)
	if err != nil {
		return err
	}
	if !empty && !force {
		return fmt.Errorf("destination volume %q is not empty; use --force to erase and replace it", volumeName)
	}

	fmt.Fprintf(runner.stdout, "Restoring portable database into Docker volume %q...\n", volumeName)
	restoreScript := "cd /volume && tar xzf /backup/" + portableArchiveName
	if force {
		restoreScript = "find /volume -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + && " + restoreScript
	}
	restoreArgs := []string{
		"run", "--rm",
		"--mount", "type=volume,source=" + volumeName + ",target=/volume",
		"--mount", "type=bind,source=" + bundleDir + ",target=/backup,readonly",
		helperImage, "sh", "-c", restoreScript,
	}
	if err := runner.run(ctx, "docker", restoreArgs...); err != nil {
		return fmt.Errorf("restore Docker volume: %w", err)
	}

	if noStart {
		restoreFinished = true
		fmt.Fprintf(runner.stdout, "Restore complete. Service %q was left stopped.\n", service)
		return nil
	}
	fmt.Fprintf(runner.stdout, "Starting restored service %q...\n", service)
	if err := runner.run(ctx, "docker", portableComposeArgs(composeFile, "up", "-d", service)...); err != nil {
		return err
	}
	restoreFinished = true
	fmt.Fprintln(runner.stdout, "Restore complete.")
	return nil
}

func portableComposeArgs(composeFile string, args ...string) []string {
	base := []string{"compose", "--project-directory", filepath.Dir(composeFile), "-f", composeFile}
	return append(base, args...)
}

func portableComposeContainerID(ctx context.Context, runner *portableRunner, composeFile, service string) (string, error) {
	output, err := runner.capture(ctx, "docker", portableComposeArgs(composeFile, "ps", "-aq", service)...)
	if err != nil {
		return "", err
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return "", fmt.Errorf("Compose service %q has no container", service)
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("Compose service %q has %d containers; expected exactly one", service, len(ids))
	}
	return ids[0], nil
}

func inspectDockerContainer(ctx context.Context, runner *portableRunner, containerID string) (dockerContainerInfo, error) {
	output, err := runner.capture(ctx, "docker", "inspect", containerID)
	if err != nil {
		return dockerContainerInfo{}, err
	}
	var result []dockerContainerInfo
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return dockerContainerInfo{}, fmt.Errorf("decode Docker inspect output: %w", err)
	}
	if len(result) != 1 {
		return dockerContainerInfo{}, fmt.Errorf("Docker inspect returned %d containers; expected one", len(result))
	}
	return result[0], nil
}

func namedVolumeAt(info dockerContainerInfo, containerPath string) (string, error) {
	wanted := path.Clean(containerPath)
	for _, mount := range info.Mounts {
		if path.Clean(mount.Destination) != wanted {
			continue
		}
		if mount.Type != "volume" || mount.Name == "" {
			return "", fmt.Errorf("mount at %q is %q, not a named Docker volume", wanted, mount.Type)
		}
		return mount.Name, nil
	}
	return "", fmt.Errorf("container has no mount at %q", wanted)
}

func ensureHelperImage(ctx context.Context, runner *portableRunner, image string) error {
	if _, err := runner.capture(ctx, "docker", "image", "inspect", image); err == nil {
		return nil
	}
	fmt.Fprintf(runner.stdout, "Pulling portable bundle helper image %q...\n", image)
	if err := runner.run(ctx, "docker", "pull", image); err != nil {
		return fmt.Errorf("prepare helper image: %w", err)
	}
	return nil
}

func portableVolumeIsEmpty(ctx context.Context, runner *portableRunner, image, volumeName string) (bool, error) {
	args := []string{
		"run", "--rm",
		"--mount", "type=volume,source=" + volumeName + ",target=/volume,readonly",
		image, "sh", "-c", `if [ -n "$(ls -A /volume 2>/dev/null)" ]; then exit 3; fi`,
	}
	err := runner.run(ctx, "docker", args...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
		return false, nil
	}
	return false, fmt.Errorf("inspect destination volume: %w", err)
}

func requireEmptyOrMissingDirectory(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("output %q already exists and is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %q is not empty; choose another bundle directory or move the old bundle", dir)
	}
	return nil
}

func safePortableBundlePath(bundleDir, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("path must be a non-empty relative path")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path leaves the bundle directory")
	}
	absolute := filepath.Join(bundleDir, clean)
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory", relative)
	}
	return absolute, nil
}

func requireCommand(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not available on PATH", name)
	}
	return nil
}

func portableFileSHA256(name string) (string, error) {
	// #nosec G304 -- name is an app-generated bundle path or a validated manifest path.
	file, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", name, err)
	}
	defer closeAndLog(file, "portable bundle file")
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %q: %w", name, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readPortableManifest(name string) (portableManifest, error) {
	// #nosec G304 -- name is resolved beneath the user-selected bundle directory.
	data, err := os.ReadFile(name)
	if err != nil {
		return portableManifest{}, fmt.Errorf("read portable bundle manifest: %w", err)
	}
	var manifest portableManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return portableManifest{}, fmt.Errorf("decode portable bundle manifest: %w", err)
	}
	return manifest, nil
}

func writePortableJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode portable bundle manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("write portable bundle manifest: %w", err)
	}
	return nil
}

func writePortableReadme(name, executable string) error {
	command := "./" + executable + " restore"
	content := "Portable SQL Server database bundle\n\n" +
		"Copy this entire directory to the destination computer. With Docker Desktop running, open a terminal in this directory and run:\n\n  " + command + "\n\n" +
		"The restore verifies the archive checksum, creates the Docker volume, restores the database files, and starts SQL Server.\n" +
		"If the destination volume already contains data, choose a different folder name or intentionally replace it with:\n\n  " + command + " --force\n\n" +
		"Warning: docker-compose.yml contains the SQL Server administrator password. Store and transfer this folder securely.\n"
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write portable bundle README: %w", err)
	}
	return nil
}

func copyPortableFile(source, destination string, forcedMode os.FileMode) error {
	// #nosec G304 -- source and destination are app-selected bundle paths.
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer closeAndLog(in, "portable bundle source")
	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if forcedMode != 0 {
		mode = forcedMode
	}
	// #nosec G304 -- destination is inside the app-created staging directory.
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	succeeded := false
	defer func() {
		_ = out.Close()
		if !succeeded {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func (runner *portableRunner) run(ctx context.Context, name string, args ...string) error {
	fmt.Fprintln(runner.stdout, "+ "+formatPortableCommand(name, args))
	// #nosec G204 -- command name is fixed by callers and arguments are passed without a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = runner.stdout
	cmd.Stderr = runner.stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", formatPortableCommand(name, args), err)
	}
	return nil
}

func (runner *portableRunner) capture(ctx context.Context, name string, args ...string) (string, error) {
	// #nosec G204 -- command name is fixed by callers and arguments are passed without a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("%s: %w: %s", formatPortableCommand(name, args), err, detail)
		}
		return "", fmt.Errorf("%s: %w", formatPortableCommand(name, args), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func formatPortableCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			parts = append(parts, strconv.Quote(arg))
		} else {
			parts = append(parts, arg)
		}
	}
	return strings.Join(parts, " ")
}
