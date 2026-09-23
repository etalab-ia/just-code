package justcode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

type fakeMSBClient struct {
	status       string
	exists       bool
	lookupErr    error
	ensureErr    error
	doctorOutput string
	doctorErr    error
	mount        string
	mountErr     error
	startScript  string
	env          map[string]string
	envErr       error
	createErr    error
	startErr     error
	modifyErr    error
	stopErr      error
	removeErr    error
	logsErr      error
	shellErr     error
	attachErr    error

	calls       []string
	created     *msbSandboxSpec
	modifiedEnv map[string]string
	modifiedKey string
	execResults []fakeMSBExecResult
	// execDefault is returned once execResults is drained; nil means success,
	// which preserves the fake's historical default.
	execDefault *fakeMSBExecResult
	// listed/listedRunning drive List: the managed sandboxes the fake
	// runtime knows, and which of them are up.
	listed        []string
	listedRunning map[string]bool
}

type fakeMSBExecResult struct {
	code   int
	stderr string
	err    error
}

func (f *fakeMSBClient) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeMSBClient) EnsureInstalled(context.Context) error {
	f.record("ensure")
	return f.ensureErr
}

func (f *fakeMSBClient) Doctor(context.Context) (string, error) {
	f.record("doctor")
	return f.doctorOutput, f.doctorErr
}

func (f *fakeMSBClient) Lookup(_ context.Context, name string) (msbSandboxInfo, bool, error) {
	f.record("lookup " + name)
	return msbSandboxInfo{Name: name, Status: f.status}, f.exists, f.lookupErr
}

func (f *fakeMSBClient) Create(_ context.Context, spec msbSandboxSpec) error {
	f.record("create")
	copy := spec
	copy.Env = cloneStringMap(spec.Env)
	copy.AllowHosts = append([]string(nil), spec.AllowHosts...)
	f.created = &copy
	return f.createErr
}

func (f *fakeMSBClient) Start(_ context.Context, name string) error {
	f.record("start " + name)
	return f.startErr
}

func (f *fakeMSBClient) ModifyNextStart(_ context.Context, name string, env map[string]string, apiKey string) error {
	f.record("modify " + name)
	f.modifiedEnv = cloneStringMap(env)
	f.modifiedKey = apiKey
	return f.modifyErr
}

func (f *fakeMSBClient) Exec(_ context.Context, name, command string) (int, string, error) {
	f.record("exec " + name + " " + command)
	if len(f.execResults) == 0 {
		if f.execDefault != nil {
			return f.execDefault.code, f.execDefault.stderr, f.execDefault.err
		}
		return 0, "", nil
	}
	result := f.execResults[0]
	f.execResults = f.execResults[1:]
	return result.code, result.stderr, result.err
}

func (f *fakeMSBClient) Stop(_ context.Context, name string) error {
	f.record("stop " + name)
	return f.stopErr
}

func (f *fakeMSBClient) Remove(_ context.Context, name string) error {
	f.record("remove " + name)
	return f.removeErr
}

func (f *fakeMSBClient) WorkspaceMount(_ context.Context, name string) (string, error) {
	f.record("mount " + name)
	return f.mount, f.mountErr
}

func (f *fakeMSBClient) StartScript(_ context.Context, name string) (string, error) {
	// Recorded as "readconfig", not "start*": hasCall prefix-matches, so a
	// "start" prefix would be indistinguishable from a Start() call.
	f.record("readconfig " + name)
	return f.startScript, nil
}

func (f *fakeMSBClient) Env(_ context.Context, name string) (map[string]string, error) {
	f.record("readenv " + name)
	return cloneStringMap(f.env), f.envErr
}

func (f *fakeMSBClient) List(_ context.Context) ([]msbSandboxInfo, error) {
	f.record("list")
	var out []msbSandboxInfo
	for _, name := range f.listed {
		status := "stopped"
		if f.listedRunning[name] {
			status = "running"
		}
		out = append(out, msbSandboxInfo{Name: name, Status: status})
	}
	return out, nil
}

func (f *fakeMSBClient) Logs(name string) error {
	f.record("logs " + name)
	return f.logsErr
}

func (f *fakeMSBClient) Shell(name string) error {
	f.record("shell " + name)
	return f.shellErr
}

func (f *fakeMSBClient) AttachInteractive(_ context.Context, name, cmd, cwd string) (int, error) {
	f.record("attach " + name + " " + cmd + " " + cwd)
	return 0, f.attachErr
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func newTestMicrosandbox(t *testing.T, client *fakeMSBClient) *MicrosandboxRuntime {
	t.Helper()
	m := NewMicrosandboxRuntime(Config{
		APIKey:       "key",
		WorkspaceDir: t.TempDir(),
		Username:     "opencode",
		Password:     "pw",
	})
	m.Client = client
	m.launchRetryDelay = time.Millisecond
	// Default to "backend not healthy" so tests exercise the relaunch path.
	m.Probe = func(context.Context, string, string, string) HealthProbe { return HealthProbe{} }
	return m
}

func countCalls(client *fakeMSBClient, prefix string) int {
	n := 0
	for _, c := range client.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func hasCall(client *fakeMSBClient, prefix string) bool {
	for _, call := range client.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = original }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(read)
		done <- string(data)
	}()

	fn()
	write.Close()
	out := <-done
	read.Close()
	return out
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	defer func() { os.Stderr = original }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(read)
		done <- string(data)
	}()

	fn()
	write.Close()
	out := <-done
	read.Close()
	return out
}

func TestMicrosandboxStartRequiresAPIKey(t *testing.T) {
	client := &fakeMSBClient{}
	m := NewMicrosandboxRuntime(Config{WorkspaceDir: t.TempDir()})
	m.Client = client
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("SDK called before validation: %v", client.calls)
	}
}

func TestMicrosandboxStartNew(t *testing.T) {
	client := &fakeMSBClient{}
	m := newTestMicrosandbox(t, client)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if client.created == nil {
		t.Fatalf("sandbox was not created; calls: %v", client.calls)
	}
	spec := client.created
	if spec.Image != msbImage || spec.Workspace != m.cfg.WorkspaceDir {
		t.Fatalf("created spec = %+v", spec)
	}
	if spec.APIKey != "key" || !reflect.DeepEqual(spec.AllowHosts, []string{msbAllowHost}) {
		t.Fatalf("secret config = key %q, hosts %v", spec.APIKey, spec.AllowHosts)
	}
	if spec.Env["OPENCODE_SERVER_PASSWORD"] != "pw" || spec.Env["OPENCODE_SERVER_USERNAME"] != "opencode" {
		t.Fatalf("credentials missing from guest env: %v", spec.Env)
	}
	if _, exists := spec.Env["ALBERT_API_KEY"]; exists {
		t.Fatal("real API key must not be placed in the guest environment")
	}
	if !strings.Contains(spec.StartScript, "opencode serve") || !strings.Contains(spec.Env["OPENCODE_CONFIG_CONTENT"], "albert.api.etalab.gouv.fr") {
		t.Fatal("embedded guest assets are missing")
	}
	// Creation boots an idle VM (the Go SDK has no background launch intent),
	// so the start script must be launched explicitly right after Create.
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("start script was not launched after creation; calls: %v", client.calls)
	}
}

// TestMicrosandboxFullModeCreatePreparesGuest pins the full-mode half of the
// create fix: the same idle-VM problem leaves a freshly created full-mode
// sandbox without its toolchain, so the start script (which installs it and
// then keeps the VM alive) must run there too.
func TestMicrosandboxFullModeCreatePreparesGuest(t *testing.T) {
	client := &fakeMSBClient{}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if client.created == nil {
		t.Fatalf("sandbox was not created; calls: %v", client.calls)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("start script was not launched after full-mode creation; calls: %v", client.calls)
	}
}

func TestMicrosandboxStartStopsOnInstallFailure(t *testing.T) {
	client := &fakeMSBClient{ensureErr: errors.New("download failed")}
	m := newTestMicrosandbox(t, client)
	err := m.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("Start error = %v", err)
	}
	if hasCall(client, "lookup") || hasCall(client, "create") {
		t.Fatalf("continued after installation failure: %v", client.calls)
	}
}

func TestMicrosandboxStartRunningHealthy(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Status: 200, Healthy: true}
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, prefix := range []string{"create", "start", "modify", "exec"} {
		if hasCall(client, prefix) {
			t.Fatalf("%s must not run for a healthy backend; calls: %v", prefix, client.calls)
		}
	}
}

func TestMicrosandboxStartRunningDeadBackend(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("entrypoint was not relaunched: %v", client.calls)
	}
	if hasCall(client, "create") || hasCall(client, "start") {
		t.Fatalf("running VM was recreated or restarted: %v", client.calls)
	}
}

func TestMicrosandboxStartStoppedRefreshesSecretAndRelaunches(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "stopped"}
	m := newTestMicrosandbox(t, client)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if client.modifiedKey != "key" {
		t.Fatalf("API key was not refreshed for next start: %q", client.modifiedKey)
	}
	if client.modifiedEnv["OPENCODE_SERVER_PASSWORD"] != "pw" {
		t.Fatalf("credentials were not refreshed: %v", client.modifiedEnv)
	}
	for _, prefix := range []string{"modify " + msbSandbox, "start " + msbSandbox, "exec " + msbSandbox} {
		if !hasCall(client, prefix) {
			t.Fatalf("missing %q; calls: %v", prefix, client.calls)
		}
	}
	if hasCall(client, "create") {
		t.Fatalf("stopped sandbox was recreated: %v", client.calls)
	}
}

func TestMicrosandboxLaunchBackendRetries(t *testing.T) {
	client := &fakeMSBClient{execResults: []fakeMSBExecResult{
		{code: 1, stderr: "agent not ready"},
		{err: errors.New("agent unavailable")},
		{},
	}}
	m := newTestMicrosandbox(t, client)
	if err := m.launchBackend(context.Background()); err != nil {
		t.Fatalf("launchBackend: %v", err)
	}
	if got := len(client.calls); got != 3 {
		t.Fatalf("calls = %v, want three exec attempts", client.calls)
	}
}

// TestMSBRelaunchCommandRunsThroughShell pins the ENOEXEC half of the fix: the
// Go SDK stores script bodies verbatim, and sandboxes created before the
// shebang was added to guest-prep.sh persist a script that cannot be exec'd
// directly. The relaunch must therefore invoke the interpreter explicitly.
func TestMSBRelaunchCommandRunsThroughShell(t *testing.T) {
	if !strings.Contains(msbRelaunchCommand, "sh "+msbGuestEntrypoint) {
		t.Fatalf("relaunch must run the entrypoint through sh: %q", msbRelaunchCommand)
	}
	// A bare `nohup ... & sleep 1` always reports the sleep's exit status (0);
	// only the kill -0 on the launcher PID makes an immediate death visible.
	if !strings.Contains(msbRelaunchCommand, "pid=$!; sleep 1; kill -0 \"$pid\"") {
		t.Fatalf("relaunch must check the backgrounded launcher's survival: %q", msbRelaunchCommand)
	}
}

func TestMicrosandboxFullModeCreateWaitsForToolchain(t *testing.T) {
	// The relaunch succeeds, the marker check fails twice, then appears:
	// creation must block until the guest is actually prepared.
	client := &fakeMSBClient{execResults: []fakeMSBExecResult{
		{code: 0},            // relaunch
		{code: 1}, {code: 1}, // marker not yet present
		{code: 0}, // marker present
	}}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.toolchainPollDelay = time.Millisecond
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	markerChecks := 0
	for _, c := range client.calls {
		if c == "exec "+msbSandbox+" "+msbGuestPrepareProbe {
			markerChecks++
		}
	}
	if markerChecks != 3 {
		t.Fatalf("marker checked %d times, want 3; calls: %v", markerChecks, client.calls)
	}
}

func TestMicrosandboxFullModeCreateTimesOutWaitingForToolchain(t *testing.T) {
	// The installer never finishes: creation must fail with guidance, not
	// report success into an unprepared guest.
	client := &fakeMSBClient{
		execResults: []fakeMSBExecResult{{code: 0}},               // relaunch
		execDefault: &fakeMSBExecResult{code: msbPrepareProgress}, // never ready
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.cfg.StartTimeout = 20 * time.Millisecond
	m.toolchainPollDelay = time.Millisecond
	err := m.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("Start error = %v, want a toolchain readiness timeout", err)
	}
}

// A running full-mode sandbox can still be unprepared: a previous creation
// may have timed out while the background installer kept going. The running
// fast path must re-check readiness rather than attach into that guest.
func TestMicrosandboxFullModeRunningRechecksToolchain(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "running",
		startScript: msbStartScript(IsolationFull),
		execDefault: &fakeMSBExecResult{code: msbPrepareProgress}, // never ready
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.cfg.StartTimeout = 20 * time.Millisecond
	m.toolchainPollDelay = time.Millisecond
	err := m.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "toolchain") {
		t.Fatalf("Start error = %v, want a toolchain readiness timeout", err)
	}
	if hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("an installer that is alive must not be joined by a second launch: %v", client.calls)
	}
}

// A running full-mode sandbox where nothing is preparing (an idle VM left by
// the pre-#61 creation path, or a start script whose launch retries were
// exhausted) must be relaunched rather than polled: no process will ever
// write the marker on its own.
func TestMicrosandboxFullModeIdleGuestIsRelaunched(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "running",
		startScript: msbStartScript(IsolationFull),
		execResults: []fakeMSBExecResult{
			{code: msbPrepareIdle},  // nothing preparing
			{code: 0},               // relaunch
			{code: msbPrepareReady}, // prepared
		},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.toolchainPollDelay = time.Millisecond
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("an idle guest must be relaunched: %v", client.calls)
	}
}

// The relaunch loop is bounded: a guest that stays idle cannot spin.
func TestMicrosandboxFullModeIdleRelaunchIsBounded(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "running",
		startScript: msbStartScript(IsolationFull),
		execResults: []fakeMSBExecResult{
			{code: msbPrepareIdle}, {code: 0},
			{code: msbPrepareIdle}, {code: 0},
			{code: msbPrepareIdle}, {code: 0},
		},
		execDefault: &fakeMSBExecResult{code: msbPrepareIdle},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.cfg.StartTimeout = 60 * time.Millisecond
	m.toolchainPollDelay = time.Millisecond
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("Start must fail when the guest never becomes ready")
	}
	relaunches := 0
	for _, c := range client.calls {
		if c == "exec "+msbSandbox+" "+msbRelaunchCommand {
			relaunches++
		}
	}
	if relaunches != msbToolchainRelaunchLimit {
		t.Fatalf("relaunches = %d, want the limit %d; calls: %v", relaunches, msbToolchainRelaunchLimit, client.calls)
	}
}

// An invalid JUST_CODE_START_TIMEOUT must surface as a configuration error in
// full mode too: attach never reaches the validation the backend path does.
func TestMicrosandboxFullModeSurfacesInvalidStartTimeout(t *testing.T) {
	client := &fakeMSBClient{execDefault: &fakeMSBExecResult{code: msbPrepareProgress}}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.cfg.StartTimeoutErr = errors.New("JUST_CODE_START_TIMEOUT must be a whole number of seconds")
	err := m.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "JUST_CODE_START_TIMEOUT") {
		t.Fatalf("Start error = %v, want the configuration error", err)
	}
}

// A full-mode sandbox stopped mid-preparation never finishes on boot (the
// entrypoint only runs at creation): the stopped path relaunches the
// idempotent start script and gates on the marker.
func TestMicrosandboxFullModeStoppedRelaunchesAndWaits(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "stopped",
		startScript: msbStartScript(IsolationFull),
		execResults: []fakeMSBExecResult{
			{code: 0},                  // relaunch
			{code: msbPrepareProgress}, // installer alive
			{code: msbPrepareReady},    // prepared
		},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	m.toolchainPollDelay = time.Millisecond
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("stopped full-mode sandbox must relaunch the start script: %v", client.calls)
	}
}

func TestMicrosandboxDoctorReportsDiagnostics(t *testing.T) {
	client := &fakeMSBClient{doctorOutput: "KVM: available\n"}
	m := newTestMicrosandbox(t, client)
	out := captureStdout(t, func() {
		if err := m.Doctor(context.Background()); err != nil {
			t.Fatalf("Doctor: %v", err)
		}
	})
	if !strings.Contains(out, "KVM: available") || !strings.Contains(out, "is ready") {
		t.Fatalf("doctor output = %q", out)
	}
}

func TestMicrosandboxDoctorSurfacesFailure(t *testing.T) {
	client := &fakeMSBClient{doctorErr: errors.New("KVM device missing")}
	m := newTestMicrosandbox(t, client)
	err := m.Doctor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "KVM device missing") {
		t.Fatalf("Doctor error = %v", err)
	}
}

func TestMicrosandboxIsRunning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exists bool
		status string
		want   bool
	}{
		{"running", true, "running", true},
		{"stopped", true, "stopped", false},
		{"absent", false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMicrosandbox(t, &fakeMSBClient{exists: tc.exists, status: tc.status})
			got, err := m.IsRunning(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("IsRunning = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestMicrosandboxWarnsOnStaleWorkspaceMount(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "running", mount: "/somewhere/else"}
	m := newTestMicrosandbox(t, client)
	stderr := captureStderr(t, func() {
		if err := m.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})
	if !strings.Contains(stderr, "/somewhere/else") || !strings.Contains(stderr, "just-code restart --microsandbox") {
		t.Fatalf("stale-mount warning = %q", stderr)
	}
}

func TestMicrosandboxStopAndClean(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Clean(context.Background()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !hasCall(client, "stop "+msbSandbox) || !hasCall(client, "remove "+msbSandbox) {
		t.Fatalf("lifecycle calls = %v", client.calls)
	}
}

func TestMicrosandboxDelegatesInteractiveCommands(t *testing.T) {
	client := &fakeMSBClient{}
	m := newTestMicrosandbox(t, client)
	if err := m.Logs(); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if err := m.Shell(); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if !hasCall(client, "logs "+msbSandbox) || !hasCall(client, "shell "+msbSandbox) {
		t.Fatalf("interactive calls = %v", client.calls)
	}
}

func TestMSBCreateOptions(t *testing.T) {
	spec := msbSandboxSpec{
		Image:       msbImage,
		Env:         map[string]string{"OPENCODE_SERVER_USERNAME": "opencode"},
		Workspace:   "/workspace-on-host",
		APIKey:      "secret-value",
		AllowHosts:  []string{msbAllowHost},
		StartScript: "exec opencode serve",
	}
	var cfg msb.SandboxConfig
	for _, option := range msbCreateOptions(spec) {
		option(&cfg)
	}
	if cfg.Image != msbImage || cfg.CPUs != 2 || cfg.MemoryMiB != 4096 || !cfg.Detached {
		t.Fatalf("resource config = %+v", cfg)
	}
	if cfg.Workdir != "/workspace" || cfg.Shell != "/bin/sh" || !reflect.DeepEqual(cfg.Entrypoint, []string{"/bin/sh", "-c"}) {
		t.Fatalf("process config = %+v", cfg)
	}
	if cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != 8*1024 {
		t.Fatalf("root disk = %+v", cfg.RootDisk)
	}
	if cfg.Volumes["/workspace"].Bind != spec.Workspace {
		t.Fatalf("workspace mount = %+v", cfg.Volumes)
	}
	for port := uint16(3000); port <= 3010; port++ {
		if cfg.Ports[port] != port {
			t.Fatalf("preview port %d missing: %v", port, cfg.Ports)
		}
	}
	if cfg.Ports[DefaultPort] != DefaultPort || cfg.Network == nil {
		t.Fatalf("network config = ports %v, network %+v", cfg.Ports, cfg.Network)
	}
	if len(cfg.Secrets) != 1 || cfg.Secrets[0].Value != spec.APIKey || cfg.Secrets[0].EnvVar != "ALBERT_API_KEY" {
		t.Fatalf("secret config = %+v", cfg.Secrets)
	}
	if !reflect.DeepEqual(cfg.Secrets[0].Allow, spec.AllowHosts) {
		t.Fatalf("allowed hosts = %v", cfg.Secrets[0].Allow)
	}
	if cfg.Scripts["start"] != spec.StartScript {
		t.Fatalf("start script = %q", cfg.Scripts["start"])
	}
}

func TestMSBNextStartOptionsRefreshesSecret(t *testing.T) {
	env := map[string]string{"OPENCODE_SERVER_PASSWORD": "new-password"}
	options := msbNextStartOptions(env, "new-key")
	if options.Policy != msb.ModificationPolicyNextStart || !reflect.DeepEqual(options.Env, env) {
		t.Fatalf("modify options = %+v", options)
	}
	secret := options.Secrets["ALBERT_API_KEY"]
	if secret.Value != "new-key" || !reflect.DeepEqual(secret.AllowedHosts, []string{msbAllowHost}) {
		t.Fatalf("updated secret = %+v", secret)
	}
}

type fakeDetachable struct {
	called bool
	err    error
}

func (f *fakeDetachable) Detach(ctx context.Context) error {
	f.called = true
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("detach context is not bounded")
	}
	return f.err
}

func TestDetachMSBSandbox(t *testing.T) {
	sb := &fakeDetachable{}
	if err := detachMSBSandbox(sb); err != nil {
		t.Fatalf("detachMSBSandbox: %v", err)
	}
	if !sb.called {
		t.Fatal("Detach was not called")
	}
}

func TestMSBRuntimeBinaryUsesManagedHome(t *testing.T) {
	t.Setenv("MSB_HOME", t.TempDir())
	path, err := msbRuntimeBinary()
	if err != nil {
		t.Fatalf("msbRuntimeBinary: %v", err)
	}
	name := "msb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if !strings.HasSuffix(path, string(os.PathSeparator)+"bin"+string(os.PathSeparator)+name) {
		t.Fatalf("managed runtime path = %q", path)
	}
}

func TestMicrosandboxRestartPreflightsWorkspace(t *testing.T) {
	// A rejected workspace must abort restart before the instance is
	// touched, so the sandbox and its persistent state survive.
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	if err := os.WriteFile(filepath.Join(m.cfg.WorkspaceDir, ".env"), []byte("X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := m.Restart(context.Background())
	if err == nil {
		t.Fatal("Restart must refuse a workspace containing .env")
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "remove") {
			t.Fatalf("Remove ran before the workspace gate: %v", client.calls)
		}
	}
}

func TestMicrosandboxRestartCreatesMissingWorkspace(t *testing.T) {
	// Restart preflights the workspace, but a workspace that does not exist
	// yet (default ./workspace) must be created like Start does, not fail
	// the preflight with a stat error.
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	m.cfg.WorkspaceDir = filepath.Join(t.TempDir(), "missing", "workspace")
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart must create a missing workspace, got: %v", err)
	}
	if info, err := os.Stat(m.cfg.WorkspaceDir); err != nil || !info.IsDir() {
		t.Fatalf("workspace was not created: %v", err)
	}
}

func TestMicrosandboxRestartIsNonDestructive(t *testing.T) {
	// P07: restart stops and starts the existing instance. It must never
	// Remove it — the destructive rebuild is the explicit Recreate.
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	if err := m.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "remove") {
			t.Fatalf("Restart must not Remove the instance: %v", client.calls)
		}
	}
	if !hasCall(client, "stop ") {
		t.Fatalf("Restart must stop the instance: %v", client.calls)
	}
}

func TestMicrosandboxRecreateRebuildsAndDropsJournal(t *testing.T) {
	// Recreate is the explicit destructive path: it removes and re-creates,
	// and clears the reconcile journal so the new instance is a fresh
	// creation rather than a resumed apply.
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	if err := m.Recreate(context.Background()); err != nil {
		t.Fatalf("Recreate: %v", err)
	}
	if !hasCall(client, "remove") {
		t.Fatalf("Recreate must remove the instance: %v", client.calls)
	}
}

func TestMicrosandboxRestartRejectsBadConfigBeforeClean(t *testing.T) {
	// A configuration error (missing API key) must abort restart before
	// the destructive Clean: the sandbox and its persistent state survive,
	// instead of being removed and then failing to recreate in Start.
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	m.cfg.APIKey = ""
	if err := m.Restart(context.Background()); err == nil {
		t.Fatal("Restart must refuse a missing ALBERT_API_KEY")
	} else if !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("client call ran despite the configuration error: %v", client.calls)
	}
}

// TestMicrosandboxFullModeSpecUsesProxySecret pins the credential contract for
// isolation full: the TUI runs inside the microVM, but that must not move the
// real key into the guest. The secret travels through the runtime's network
// proxy, exactly as in backend mode, and the guest only ever sees the
// placeholder Microsandbox exposes under ALBERT_API_KEY.
func TestMicrosandboxFullModeSpecUsesProxySecret(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	m.cfg.Isolation = IsolationFull
	spec := m.sandboxSpec()
	if spec.APIKey != "key" {
		t.Fatalf("full mode must configure the proxy secret: %+v", spec)
	}
	if !reflect.DeepEqual(spec.AllowHosts, []string{msbAllowHost}) {
		t.Fatalf("full mode must restrict proxy hosts to Albert: %v", spec.AllowHosts)
	}
	if got := spec.Env[msbAPISecretEnv]; got != "" {
		t.Fatalf("full mode guest env must not carry the real key: %v", spec.Env)
	}
	if spec.Env["OPENCODE_CONFIG_CONTENT"] == "" {
		t.Fatal("full mode guest env must carry the OpenCode config")
	}
	if !strings.Contains(spec.StartScript, "sleep infinity") || strings.Contains(spec.StartScript, "opencode serve") {
		t.Fatalf("full mode start script must keep the VM alive, not serve: %q", spec.StartScript)
	}
	if !strings.Contains(spec.StartScript, "apk add") {
		t.Fatalf("full mode start script must keep the shared toolchain prep: %q", spec.StartScript)
	}
}

// TestMicrosandboxSpecNeverLeaksRealKey is the regression guard for the whole
// change: whatever the isolation level, no sandbox spec may place the host
// secret in the guest environment.
func TestMicrosandboxSpecNeverLeaksRealKey(t *testing.T) {
	for _, isolation := range []Isolation{IsolationBackend, IsolationFull, ""} {
		m := newTestMicrosandbox(t, &fakeMSBClient{})
		m.cfg.Isolation = isolation
		spec := m.sandboxSpec()
		for key, value := range spec.Env {
			if value == m.cfg.APIKey {
				t.Fatalf("isolation %q leaks the real key into guest env %q", isolation, key)
			}
		}
		for key, value := range m.nextStartEnv() {
			if value == m.cfg.APIKey {
				t.Fatalf("isolation %q leaks the real key into next-start env %q", isolation, key)
			}
		}
	}
}

func TestMicrosandboxBackendModeSpecUnchanged(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	spec := m.sandboxSpec()
	if spec.APIKey != "key" || len(spec.AllowHosts) != 1 {
		t.Fatalf("backend mode must keep the proxy secret: %+v", spec)
	}
	if spec.Env[msbAPISecretEnv] != "" {
		t.Fatalf("backend mode must not leak the real key into guest env: %v", spec.Env)
	}
	if !strings.Contains(spec.StartScript, "exec opencode serve") {
		t.Fatalf("backend mode start script must serve: %q", spec.StartScript)
	}
}

// TestMicrosandboxFullModeNextStartUsesProxySecretAndScrubsRawEnv covers the
// stopped-sandbox path. The proxy secret must be refreshed for the next boot
// just as in backend mode, and any plaintext ALBERT_API_KEY persisted by an
// earlier version must be removed from the guest environment in the same
// modification.
func TestMicrosandboxFullModeNextStartUsesProxySecretAndScrubsRawEnv(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{exists: true, status: "stopped"})
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.cfg.Isolation != IsolationFull {
		t.Fatal("config lost the isolation level")
	}
	client := m.Client.(*fakeMSBClient)
	if client.modifiedKey != "key" {
		t.Fatalf("full mode must refresh the proxy secret on restart: %q", client.modifiedKey)
	}
	if client.modifiedEnv[msbAPISecretEnv] != "" {
		t.Fatalf("full mode next-start env must not carry the real key: %v", client.modifiedEnv)
	}
	options := msbNextStartOptions(m.nextStartEnv(), m.cfg.APIKey)
	if !containsString(options.EnvRemove, msbAPISecretEnv) {
		t.Fatalf("next-start must remove a persisted plaintext key: %v", options.EnvRemove)
	}
	secret := options.Secrets[msbAPISecretEnv]
	if secret.Value != "key" || !reflect.DeepEqual(secret.AllowedHosts, []string{msbAllowHost}) {
		t.Fatalf("proxy secret not refreshed: %+v", secret)
	}
	for _, prefix := range []string{"modify " + msbSandbox, "start " + msbSandbox} {
		if !hasCall(client, prefix) {
			t.Fatalf("missing %q; calls: %v", prefix, client.calls)
		}
	}
	// Booting a stopped VM does not re-run the entrypoint, so the idempotent
	// start script is relaunched (a sandbox stopped mid-preparation would
	// otherwise never finish installing) and readiness is gated on the
	// toolchain marker.
	if !hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("full mode restart must relaunch the start script: %v", client.calls)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbGuestPrepareProbe) {
		t.Fatalf("readiness marker was not checked: %v", client.calls)
	}
}

// TestMicrosandboxFullModeRejectsLegacyRawKey pins the migration guard: a
// sandbox whose persisted guest environment holds a real key cannot be fixed
// in place, so the start must fail with the recreate command instead of
// booting a VM that keeps serving a plaintext credential.
func TestMicrosandboxFullModeRejectsLegacyRawKey(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "stopped",
		startScript: msbStartScript(IsolationFull),
		env:         map[string]string{"ALBERT_API_KEY": "real-persisted-key"},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	err := m.Start(context.Background())
	if err == nil {
		t.Fatal("a sandbox persisting a plaintext key must not boot")
	}
	if !strings.Contains(err.Error(), "restart --microsandbox") {
		t.Fatalf("error must point at the recreate command: %v", err)
	}
	if hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a legacy sandbox must not be started: %v", client.calls)
	}
}

// TestMicrosandboxFullModeAcceptsPlaceholderEnv records that the guard targets
// the plaintext value, not the variable name: a sandbox already using the
// proxy stores the placeholder there and starts normally.
func TestMicrosandboxFullModeAcceptsPlaceholderEnv(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "stopped",
		startScript: msbStartScript(IsolationFull),
		env:         map[string]string{"ALBERT_API_KEY": "$MSB_ALBERT_API_KEY"},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a proxied sandbox must start: %v", client.calls)
	}
}

// TestMicrosandboxStartFailsClosedOnUnreadableEnv pins the refusal when the
// guest environment cannot be inspected. Scrub-on-next-start never runs for a
// running sandbox, and a stopped one cannot be verified either, so an
// unreadable config is not evidence of a protected sandbox: booting on that
// assumption could keep serving a plaintext key.
func TestMicrosandboxStartFailsClosedOnUnreadableEnv(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "stopped",
		startScript: msbStartScript(IsolationFull),
		envErr:      errors.New("config unavailable"),
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	err := m.Start(context.Background())
	if err == nil {
		t.Fatal("an unreadable guest environment must not boot")
	}
	if !strings.Contains(err.Error(), "restart --microsandbox") {
		t.Fatalf("error must point at the recreate command: %v", err)
	}
	if !errors.Is(err, client.envErr) {
		t.Fatalf("error must carry the inspection failure: %v", err)
	}
	if hasCall(client, "start "+msbSandbox) {
		t.Fatalf("an unverifiable sandbox must not be started: %v", client.calls)
	}
}

// persistedConfigWithGuestEnv renders the JSON the runtime stores for a
// sandbox whose spec environment holds the given entries.
//
// The env field is an array of {key,value} objects, not a JSON object: the
// runtime flattens its spec into the sandbox config (serde flatten,
// sdk/rust/lib/sandbox/config.rs) and the spec's env is a Vec<EnvVar> where
// EnvVar is {key, value} (packages/microsandbox-types/rust/lib/domain.rs).
// Reproducing that shape here is the point of these tests: a fixture shaped
// like the map the SDK would need would pass while production saw nothing.
func persistedConfigWithGuestEnv(entries ...string) string {
	return `{"name":"` + msbSandbox + `",` +
		`"runtime":{"workdir":"/workspace","shell":"/bin/sh","scripts":{"start":"exec sleep infinity"}},` +
		`"env":[` + strings.Join(entries, ",") + `]}`
}

// persistedGuestEnvFromConfig runs the production reader over the persisted
// fixture, so the guard tests below start from the stored JSON rather than from
// a hand-built map the runtime could never produce.
func persistedGuestEnvFromConfig(t *testing.T, entries ...string) map[string]string {
	t.Helper()
	env, err := parseGuestEnv(persistedConfigWithGuestEnv(entries...))
	if err != nil {
		t.Fatalf("parseGuestEnv: %v", err)
	}
	return env
}

// TestMicrosandboxLegacyGuardReadsThePersistedConfig is the end-to-end check of
// the guard against the stored shape, and the test that fails when the reader
// looks at a field the SDK never fills: a legacy sandbox persisting the
// plaintext key must be refused, and one persisting the placeholder must boot.
func TestMicrosandboxLegacyGuardReadsThePersistedConfig(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		wantErr bool
	}{
		{
			name:    "pre-proxy sandbox holds the plaintext key",
			entries: []string{`{"key":"OPENCODE_SERVER_USERNAME","value":"opencode"}`, `{"key":"` + msbAPISecretEnv + `","value":"albert-real-secret"}`},
			wantErr: true,
		},
		{
			name:    "proxied sandbox holds the placeholder",
			entries: []string{`{"key":"` + msbAPISecretEnv + `","value":"` + msbAPISecretPlaceholder + `"}`},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeMSBClient{
				exists:      true,
				status:      "stopped",
				startScript: msbStartScript(IsolationFull),
				env:         persistedGuestEnvFromConfig(t, tt.entries...),
			}
			m := newTestMicrosandbox(t, client)
			m.cfg.Isolation = IsolationFull
			err := m.Start(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("a sandbox persisting a plaintext key must not boot")
				}
				if !strings.Contains(err.Error(), "restart --microsandbox") {
					t.Fatalf("error must point at the recreate command: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if !hasCall(client, "start "+msbSandbox) {
				t.Fatalf("the sandbox must start: %v", client.calls)
			}
		})
	}
}

// TestParseGuestEnvReadsThePersistedEnvShape pins the reader against the shape
// the runtime actually persists. The legacy guard is only worth anything if it
// sees the stored value, so this asserts the array form directly.
func TestParseGuestEnvReadsThePersistedEnvShape(t *testing.T) {
	config := persistedConfigWithGuestEnv(
		`{"key":"OPENCODE_SERVER_USERNAME","value":"opencode"}`,
		`{"key":"`+msbAPISecretEnv+`","value":"albert-real-secret"}`,
	)
	env, err := parseGuestEnv(config)
	if err != nil {
		t.Fatalf("parseGuestEnv: %v", err)
	}
	if got := env[msbAPISecretEnv]; got != "albert-real-secret" {
		t.Fatalf("%s = %q, want the persisted value", msbAPISecretEnv, got)
	}
	if got := env["OPENCODE_SERVER_USERNAME"]; got != "opencode" {
		t.Fatalf("OPENCODE_SERVER_USERNAME = %q, want %q", got, "opencode")
	}
}

// TestParseGuestEnvIgnoresAbsentAndSecretsSections keeps the reader narrow: a
// sandbox with no spec env yields an empty environment rather than an error, so
// callers can tell "nothing persisted" from "could not read".
func TestParseGuestEnvIgnoresAbsentAndSecretsSections(t *testing.T) {
	env, err := parseGuestEnv(`{"name":"` + msbSandbox + `"}`)
	if err != nil {
		t.Fatalf("parseGuestEnv without an env section: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("env = %v, want empty", env)
	}
	if _, err := parseGuestEnv(""); err == nil {
		t.Fatal("an unreadable config must surface an error, not an empty environment")
	}
}

// TestSDKSandboxConfigCannotSeeThePersistedGuestEnv is the regression guard for
// the reason parseGuestEnv exists at all. The SDK's typed SandboxConfig decodes
// the stored config into an internal struct with no top-level env field, and
// fills the public Env map only from the init section, so cfg.Env stays nil for
// anything created from a spec env. A guard reading cfg.Env therefore never
// fires. If a future SDK version starts populating it, this fails and the raw
// parse can be revisited.
func TestSDKSandboxConfigCannotSeeThePersistedGuestEnv(t *testing.T) {
	config := persistedConfigWithGuestEnv(
		`{"key":"` + msbAPISecretEnv + `","value":"albert-real-secret"}`,
	)
	var cfg msb.SandboxConfig
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		t.Fatalf("unmarshal SandboxConfig: %v", err)
	}
	if _, ok := cfg.Env[msbAPISecretEnv]; ok {
		t.Fatalf("SandboxConfig.Env now exposes the spec env (%v); the raw parse may be unnecessary", cfg.Env)
	}
}

// persistedConfigWithMounts reproduces the mounts section of a real persisted
// config document (captured from a live albert-opencode-sandbox created by
// just-code 0.4.1, runtime v0.7.0). Keeping the full entry shape — options,
// stat_virtualization, follow_root_symlinks — matters: a fixture trimmed to
// what parseWorkspaceMount reads would not prove the parser accepts what the
// runtime actually writes.
func persistedConfigWithMounts(mounts ...string) string {
	return `{"name":"` + msbSandbox + `",` +
		`"runtime":{"workdir":"/workspace","shell":"/bin/sh"},` +
		`"mounts":[` + strings.Join(mounts, ",") + `]}`
}

const persistedWorkspaceBindMount = `{"type":"Bind","host":"/Users/tester/project","guest":"/workspace",` +
	`"options":{"readonly":false,"noexec":false,"nosuid":false,"nodev":false},` +
	`"stat_virtualization":"strict","host_permissions":"private","follow_root_symlinks":false,"quota_mib":null}`

func TestParseWorkspaceMount(t *testing.T) {
	host, err := parseWorkspaceMount(persistedConfigWithMounts(persistedWorkspaceBindMount), "/workspace")
	if err != nil {
		t.Fatalf("parseWorkspaceMount: %v", err)
	}
	if host != "/Users/tester/project" {
		t.Fatalf("host = %q, want /Users/tester/project", host)
	}

	otherGuest := `{"type":"Bind","host":"/elsewhere","guest":"/data"}`
	host, err = parseWorkspaceMount(persistedConfigWithMounts(otherGuest), "/workspace")
	if err != nil {
		t.Fatalf("parseWorkspaceMount without a /workspace mount: %v", err)
	}
	if host != "" {
		t.Fatalf("host = %q, want empty when no /workspace mount exists", host)
	}

	if _, err := parseWorkspaceMount("", "/workspace"); err == nil {
		t.Fatal("an unreadable config must surface an error, not an empty mount")
	}
}

// TestSDKSandboxConfigCannotSeeThePersistedMounts is the regression guard for
// the reason parseWorkspaceMount exists at all — the mounts twin of
// TestSDKSandboxConfigCannotSeeThePersistedGuestEnv. The SDK's typed
// SandboxConfig decodes Volumes as nil for a sandbox created from spec mounts,
// so a stale-mount check reading cfg.Volumes never fires. If a future SDK
// version starts populating it, this fails and the raw parse can be revisited.
func TestSDKSandboxConfigCannotSeeThePersistedMounts(t *testing.T) {
	config := persistedConfigWithMounts(persistedWorkspaceBindMount)
	var cfg msb.SandboxConfig
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		t.Fatalf("unmarshal SandboxConfig: %v", err)
	}
	if len(cfg.Volumes) != 0 {
		t.Fatalf("SandboxConfig.Volumes now exposes the spec mounts (%v); the raw parse may be unnecessary", cfg.Volumes)
	}
}

// TestMicrosandboxFullModeRejectsEnvThatMerelyLooksLikeAPlaceholder covers the
// prefix trap: only the exact documented placeholder is protected, so a value
// that starts with the runtime's placeholder prefix is still treated as a
// credential the guest can read.
func TestMicrosandboxFullModeRejectsEnvThatMerelyLooksLikeAPlaceholder(t *testing.T) {
	for _, value := range []string{"$MSB_OTHER_VAR", "$MSB_", "$MSB_ALBERT_API_KEYx"} {
		client := &fakeMSBClient{
			exists:      true,
			status:      "stopped",
			startScript: msbStartScript(IsolationFull),
			env:         map[string]string{msbAPISecretEnv: value},
		}
		m := newTestMicrosandbox(t, client)
		m.cfg.Isolation = IsolationFull
		if err := m.Start(context.Background()); err == nil {
			t.Fatalf("%q must not pass as the protected placeholder", value)
		}
		if hasCall(client, "start "+msbSandbox) {
			t.Fatalf("%q must not boot: %v", value, client.calls)
		}
	}
}

// TestMicrosandboxFullModeStartsWithNoPersistedKey records that a sandbox whose
// spec env carries no Albert entry at all is not a legacy sandbox: the guard
// looks for a persisted value, not for the variable name.
func TestMicrosandboxFullModeStartsWithNoPersistedKey(t *testing.T) {
	client := &fakeMSBClient{
		exists:      true,
		status:      "stopped",
		startScript: msbStartScript(IsolationFull),
		env:         map[string]string{"OPENCODE_SERVER_USERNAME": "opencode"},
	}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a sandbox without a persisted key must start: %v", client.calls)
	}
}

// handledConfig is a persisted document exposed the way a real SDK handle
// exposes it. The fake carries no typed config, which is the faithful shape:
// the real handle's typed SandboxConfig cannot see the spec env either, so a
// reader that reaches for it observes nothing.
type handledConfig struct{ configJSON string }

func (h handledConfig) ConfigJSON() string { return h.configJSON }

// TestGuestEnvFromHandleReadsThePersistedDocument covers the wiring, not just
// the parser. The guard is only as good as the source it reads, and the source
// that looks obvious, the handle's typed sandbox config, is blind to the spec
// env. A reader wired to that field no-ops in production while every
// fakeMSBClient test still passes, so this pins the document the reader uses.
func TestGuestEnvFromHandleReadsThePersistedDocument(t *testing.T) {
	h := handledConfig{configJSON: persistedConfigWithGuestEnv(
		`{"key":"` + msbAPISecretEnv + `","value":"albert-real-secret"}`,
	)}
	env, err := guestEnvFromHandle(h)
	if err != nil {
		t.Fatalf("guestEnvFromHandle: %v", err)
	}
	if got := env[msbAPISecretEnv]; got != "albert-real-secret" {
		t.Fatalf("%s = %q, want the value stored in the persisted document", msbAPISecretEnv, got)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// TestWarnUnprotectedRuntimeNamesTheRuntimeAndTheRisk pins the user-facing
// warning for runtimes with no secret proxy: it must name the runtime, say the
// key is readable in the guest, and make clear that isolation backend is not
// an escape hatch.
func TestWarnUnprotectedRuntimeNamesTheRuntimeAndTheRisk(t *testing.T) {
	for _, runtimeName := range []string{"Tart", "agent-vm"} {
		out := captureStderr(t, func() { warnUnprotectedRuntime(runtimeName) })
		for _, want := range []string{runtimeName, "no secret injection", "ALBERT_API_KEY", "isolation backend and full"} {
			if !strings.Contains(out, want) {
				t.Fatalf("warning for %s missing %q: %s", runtimeName, want, out)
			}
		}
	}
}

// TestMicrosandboxStartRejectsIsolationSwitch pins the mode-switch guard: the
// start script is persisted at creation and cannot be rewritten, so switching
// isolation on an existing sandbox must fail with guidance instead of booting
// the wrong process.
func TestMicrosandboxStartRejectsIsolationSwitch(t *testing.T) {
	// A sandbox created in full mode, now requested in backend mode.
	client := &fakeMSBClient{exists: true, status: "stopped", startScript: msbStartScript(IsolationFull)}
	m := newTestMicrosandbox(t, client)
	err := m.Start(context.Background())
	if err == nil {
		t.Fatal("switching an existing full-mode sandbox to backend must fail")
	}
	if !strings.Contains(err.Error(), "restart") {
		t.Errorf("error must point at the recovery command: %v", err)
	}
	if hasCall(client, "start "+msbSandbox) || hasCall(client, "exec "+msbSandbox) {
		t.Fatalf("a mismatched sandbox must not be booted: %v", client.calls)
	}

	// The reverse direction: a backend-mode sandbox requested in full mode.
	client = &fakeMSBClient{exists: true, status: "stopped", startScript: msbStartScript(IsolationBackend)}
	m = newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("switching an existing backend-mode sandbox to full must fail")
	}
	if hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a mismatched sandbox must not be booted: %v", client.calls)
	}
}

// TestMicrosandboxFullModeRunningSandboxIsReady records that a running
// full-mode sandbox is not health-probed (no backend endpoint exists in that
// mode, so probing would always fail and relaunch into the wrong process) —
// but readiness is still re-checked via the toolchain marker, because a
// previous creation may have timed out with the installer still running.
func TestMicrosandboxFullModeRunningSandboxIsReady(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "running", startScript: msbStartScript(IsolationFull)}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if hasCall(client, "exec "+msbSandbox+" "+msbRelaunchCommand) {
		t.Fatalf("a running full-mode sandbox must not be relaunched: %v", client.calls)
	}
	if !hasCall(client, "exec "+msbSandbox+" "+msbGuestPrepareProbe) {
		t.Fatalf("readiness marker was not re-checked: %v", client.calls)
	}
	if hasCall(client, "create") || hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a running full-mode sandbox must be left as-is: %v", client.calls)
	}
}

// TestMicrosandboxStartMatchingIsolationProceeds records that the guard is
// mode-equality, not "always refuse": an existing sandbox whose script matches
// the requested mode still starts normally.
func TestMicrosandboxStartMatchingIsolationProceeds(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "stopped", startScript: msbStartScript(IsolationFull)}
	m := newTestMicrosandbox(t, client)
	m.cfg.Isolation = IsolationFull
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !hasCall(client, "start "+msbSandbox) {
		t.Fatalf("a matching sandbox must start; calls: %v", client.calls)
	}

	// An unreadable script must not block the start either.
	client = &fakeMSBClient{exists: true, status: "stopped"}
	m = newTestMicrosandbox(t, client)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start with an unknown script: %v", err)
	}
	if !hasCall(client, "start "+msbSandbox) {
		t.Fatalf("an undetectable mode must not block the start; calls: %v", client.calls)
	}
}

func TestMicrosandboxRunAgentAttachesTUI(t *testing.T) {
	client := &fakeMSBClient{}
	m := newTestMicrosandbox(t, client)
	if err := m.RunAgent(context.Background()); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if !hasCall(client, "attach "+msbSandbox+" opencode /workspace") {
		t.Fatalf("RunAgent must attach opencode at /workspace; calls: %v", client.calls)
	}
}

func TestMicrosandboxRunAgentSurfacesExitCode(t *testing.T) {
	client := &fakeMSBClient{attachErr: errors.New("attach stream closed")}
	m := newTestMicrosandbox(t, client)
	if err := m.RunAgent(context.Background()); err == nil {
		t.Fatal("RunAgent must surface attach failures")
	}
}

func TestMicrosandboxStatus(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{exists: true, status: "running"})
	state, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(state, "running") {
		t.Fatalf("Status = %q", state)
	}
}
