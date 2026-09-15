package justcode

import (
	"context"
	"errors"
	"io"
	"os"
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
	createErr    error
	startErr     error
	modifyErr    error
	stopErr      error
	removeErr    error
	logsErr      error
	shellErr     error

	calls       []string
	created     *msbSandboxSpec
	modifiedEnv map[string]string
	modifiedKey string
	execResults []fakeMSBExecResult
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

func (f *fakeMSBClient) Logs() error {
	f.record("logs")
	return f.logsErr
}

func (f *fakeMSBClient) Shell() error {
	f.record("shell")
	return f.shellErr
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
	if !hasCall(client, "logs") || !hasCall(client, "shell") {
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
	if !reflect.DeepEqual(cfg.Secrets[0].AllowHosts, spec.AllowHosts) {
		t.Fatalf("allowed hosts = %v", cfg.Secrets[0].AllowHosts)
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
