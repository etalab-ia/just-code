package justcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/etalab-ia/just-code/assets"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Guest assets are read directly from the binary, without writing host files.
var (
	guestPrepScript       = mustAsset("guest-prep.sh")
	opencodeConfigContent = mustAsset("opencode-config.json")
)

// msbStartScript composes the guest start script: the shared prep (toolchain,
// git identity) followed by a mode-specific tail. In backend mode the tail
// execs `opencode serve`; in full mode the sandbox is only kept alive — the
// TUI is launched interactively by RunAgent, not by the entrypoint.
func msbStartScript(iso Isolation) string {
	tail := "exec opencode serve --hostname 0.0.0.0 --port " + strconv.Itoa(DefaultPort)
	if iso == IsolationFull {
		tail = "exec sleep infinity"
	}
	return guestPrepScript + tail + "\n"
}

func mustAsset(name string) string {
	data, err := assets.Read(name)
	if err != nil {
		panic(fmt.Sprintf("justcode: embedded asset %s missing: %v", name, err))
	}
	return string(data)
}

func msbPortMappings() map[uint16]uint16 {
	ports := map[uint16]uint16{DefaultPort: DefaultPort}
	for p := uint16(3000); p <= 3010; p++ {
		ports[p] = p
	}
	return ports
}

// msbManagedLabel is the ownership label attached to every sandbox just-code
// creates (P06). The lifecycle sweeps enumerate managed sandboxes by this
// label; the legacy singleton (created before labels) is recognized by name.
const msbManagedLabel = "justcode.managed"

type sdkMSBClient struct{}

var _ msbClient = sdkMSBClient{}

func (sdkMSBClient) EnsureInstalled(ctx context.Context) error {
	return ensureMSBRuntime(ctx, nil)
}

func (sdkMSBClient) Doctor(ctx context.Context) (string, error) {
	path, err := msbRuntimeBinary()
	if err != nil {
		return "", err
	}
	output, err := exec.CommandContext(ctx, path, "doctor").CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return "", fmt.Errorf("microsandbox doctor: %w", err)
	}
	return string(output), fmt.Errorf("microsandbox doctor: %w: %s", err, detail)
}

// Managed installs use the SDK's documented MSB_HOME layout; manual installs
// keep the SDK's MSB_PATH override. The CLI is invoked by absolute path solely
// because the SDK does not expose the host diagnostics API.
func msbRuntimeBinary() (string, error) {
	if path := os.Getenv("MSB_PATH"); path != "" {
		return path, nil
	}
	home, err := msbRuntimeHome()
	if err != nil {
		return "", err
	}
	name := "msb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, "bin", name), nil
}

func (sdkMSBClient) Lookup(ctx context.Context, name string) (msbSandboxInfo, bool, error) {
	h, err := msb.GetSandbox(ctx, name)
	if msb.IsKind(err, msb.ErrSandboxNotFound) {
		return msbSandboxInfo{}, false, nil
	}
	if err != nil {
		return msbSandboxInfo{}, false, err
	}
	return msbSandboxInfo{Name: h.Name(), Status: string(h.Status())}, true, nil
}

// List enumerates managed sandboxes by the ownership label, following the
// SDK's cursor pagination.
func (sdkMSBClient) List(ctx context.Context) ([]msbSandboxInfo, error) {
	var out []msbSandboxInfo
	cursor := ""
	for {
		opts := []msb.SandboxListOption{msb.WithListLabels(map[string]string{msbManagedLabel: "true"})}
		if cursor != "" {
			opts = append(opts, msb.WithListCursor(cursor))
		}
		page, err := msb.ListSandboxesWith(ctx, opts...)
		if err != nil {
			return nil, err
		}
		for _, h := range page.Sandboxes {
			out = append(out, msbSandboxInfo{Name: h.Name(), Status: string(h.Status())})
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
	}
}

// Kept separate from Create so tests can verify the actual SDK options without
// loading native code or creating a VM.
//
// Secret handling (P09): the SDK's create surface only accepts inline values,
// so each binding is registered with the inert bootstrap sentinel and rotated
// to its host env reference immediately after creation (RotateSecretsLive),
// before the start script runs. The raw value never reaches the persisted
// sandbox config.
func msbCreateOptions(spec msbSandboxSpec) []msb.SandboxOption {
	secrets := make([]msb.SecretEntry, 0, len(spec.Bindings))
	for _, b := range spec.Bindings {
		secrets = append(secrets, msb.Secret.Env(b.GuestEnv, msbSecretBootstrapValue, msb.SecretEnvOptions{
			Allow: b.AllowHosts,
		}))
	}
	return []msb.SandboxOption{
		msb.WithImage(spec.Image),
		msb.WithCPUs(2),
		msb.WithMemory(4096),
		msb.WithWorkdir("/workspace"),
		msb.WithShell("/bin/sh"),
		msb.WithEntrypoint("/bin/sh", "-c"),
		msb.WithCmd("exec " + msbGuestEntrypoint),
		msb.WithDetached(),
		msb.WithRootDisk(msb.RootDisk.Managed(8 * 1024)),
		msb.WithEnv(spec.Env),
		msb.WithMounts(map[string]msb.MountConfig{
			"/workspace": msb.Mount.Bind(spec.Workspace, msb.MountOptions{}),
		}),
		msb.WithNetwork(msb.NetworkPolicy.FromProfiles(msb.NetworkProfilePublic)),
		msb.WithPorts(msbPortMappings()),
		msb.WithSecrets(secrets...),
		msb.WithScripts(map[string]string{"start": spec.StartScript}),
		// Ownership label (P06): the lifecycle sweeps list managed sandboxes
		// by this label rather than by name guessing, and the legacy singleton
		// is recognized separately.
		msb.WithLabel(msbManagedLabel, "true"),
	}
}

func (sdkMSBClient) Create(ctx context.Context, spec msbSandboxSpec) error {
	sb, err := msb.CreateSandbox(ctx, spec.Name, msbCreateOptions(spec)...)
	if err != nil {
		return err
	}
	return detachMSBSandbox(sb)
}

// Close stops detached VMs too. Detach releases ownership without stopping
// them; use a fresh bounded context so caller cancellation cannot skip cleanup.
type msbDetachable interface {
	Detach(context.Context) error
}

func detachMSBSandbox(sb msbDetachable) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return sb.Detach(ctx)
}

func (sdkMSBClient) Start(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	sb, err := h.StartDetached(ctx)
	if err != nil {
		return err
	}
	return detachMSBSandbox(sb)
}

// msbNextStartOptions builds the next-boot refresh for an existing sandbox.
// Every binding is re-registered as a host env reference ({"kind":"env",
// "var":...}): the raw value is never persisted, and the reference is
// re-resolved from the just-code process environment at apply/boot time, so a
// rotated credential is picked up on the next boot without any value crossing
// the persisted config. EnvRemove scrubs a raw value an earlier version may
// have persisted under the guest name.
func msbNextStartOptions(env map[string]string, bindings []msbSecretBinding) msb.ModifyOptions {
	opts := msb.ModifyOptions{
		Env:     env,
		Secrets: make(map[string]msb.SecretModifySpec, len(bindings)),
		Policy:  msb.ModificationPolicyNextStart,
	}
	for _, b := range bindings {
		opts.EnvRemove = append(opts.EnvRemove, b.GuestEnv)
		opts.Secrets[b.GuestEnv] = msb.SecretModifySpec{Env: b.HostEnv, AllowedHosts: b.AllowHosts}
	}
	return opts
}

func (sdkMSBClient) ModifyNextStart(ctx context.Context, name string, env map[string]string, bindings []msbSecretBinding) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	_, err = h.Modify(ctx, msbNextStartOptions(env, bindings))
	return err
}

// msbRotateLiveOptions re-registers each binding as a host env reference with
// the NoRestart policy: the only secret change the runtime applies to a
// running sandbox. The create path uses it to replace the bootstrap sentinel
// before the start script runs.
func msbRotateLiveOptions(bindings []msbSecretBinding) msb.ModifyOptions {
	opts := msb.ModifyOptions{
		Secrets: make(map[string]msb.SecretModifySpec, len(bindings)),
		Policy:  msb.ModificationPolicyNoRestart,
	}
	for _, b := range bindings {
		opts.Secrets[b.GuestEnv] = msb.SecretModifySpec{Env: b.HostEnv, AllowedHosts: b.AllowHosts}
	}
	return opts
}

func (sdkMSBClient) RotateSecretsLive(ctx context.Context, name string, bindings []msbSecretBinding) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	_, err = h.Modify(ctx, msbRotateLiveOptions(bindings))
	return err
}

// msbRemoveSecretsOptions drops the proxy registrations for guestEnvs. Live
// (running sandbox, NoRestart) the registration goes away immediately but the
// guest process keeps its environment — the placeholder variable dangles
// until restart, which the caller must warn about. On a stopped sandbox the
// removal is persisted for the next boot and the placeholder's variable is
// scrubbed too.
func msbRemoveSecretsOptions(guestEnvs []string, live bool) msb.ModifyOptions {
	opts := msb.ModifyOptions{SecretsRemove: guestEnvs}
	if live {
		opts.Policy = msb.ModificationPolicyNoRestart
	} else {
		opts.Policy = msb.ModificationPolicyNextStart
		opts.EnvRemove = guestEnvs
	}
	return opts
}

func (sdkMSBClient) RemoveSecrets(ctx context.Context, name string, guestEnvs []string, live bool) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	_, err = h.Modify(ctx, msbRemoveSecretsOptions(guestEnvs, live))
	return err
}

func connectMSBSandbox(ctx context.Context, name string) (*msb.Sandbox, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return h.Connect(ctx)
}

func (sdkMSBClient) Exec(ctx context.Context, name, command string) (code int, stderr string, err error) {
	sb, err := connectMSBSandbox(ctx, name)
	if err != nil {
		return 0, "", err
	}
	// Connect returns a non-owning handle, so Close only releases it and does
	// not stop the VM.
	defer func() { err = errors.Join(err, sb.Close()) }()
	out, err := sb.Shell(ctx, command)
	if err != nil {
		return 0, "", err
	}
	return out.ExitCode(), out.Stderr(), nil
}

func (sdkMSBClient) Stop(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	return h.Stop(ctx, msb.WithStopTimeout(3*time.Second))
}

func (sdkMSBClient) Remove(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	// Destroy handles running and stopped sandboxes and binds removal to the
	// identity just retrieved, refusing to remove a same-name replacement.
	return h.Destroy(ctx, msb.WithDestroyForce())
}

// persistedMounts mirrors how the runtime stores a sandbox's mounts in the
// persisted config document. The SDK's typed SandboxConfig cannot see them:
// its Volumes map decodes as nil for a sandbox created with spec mounts
// (verified against a live handle — same blind spot as the spec env, see
// persistedGuestEnv), so the raw document is the only readable source.
type persistedMounts struct {
	Mounts []struct {
		Type  string `json:"type"`
		Host  string `json:"host"`
		Guest string `json:"guest"`
	} `json:"mounts"`
}

// parseWorkspaceMount returns the host path bound at the guest path, or ""
// when the document carries no such mount.
func parseWorkspaceMount(configJSON, guestPath string) (string, error) {
	var raw persistedMounts
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return "", fmt.Errorf("decode persisted sandbox config: %w", err)
	}
	for _, m := range raw.Mounts {
		if m.Guest == guestPath && (m.Type == "" || m.Type == "Bind") {
			return m.Host, nil
		}
	}
	return "", nil
}

func (sdkMSBClient) WorkspaceMount(ctx context.Context, name string) (string, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return "", err
	}
	return parseWorkspaceMount(h.ConfigJSON(), "/workspace")
}

func (sdkMSBClient) StartScript(ctx context.Context, name string) (string, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return "", err
	}
	cfg, err := h.Config()
	if err != nil {
		return "", err
	}
	return cfg.Scripts["start"], nil
}

// persistedGuestEnv mirrors how the runtime stores a sandbox's guest
// environment. The runtime flattens its spec into the sandbox config
// (serde flatten) and the spec's env field is a list of {key, value} entries,
// so the stored JSON carries "env" as an array of objects.
//
// The SDK's own SandboxConfig.Env map cannot see it. SandboxConfig.UnmarshalJSON
// decodes into an internal struct with no top-level env field, and fills the
// public Env map only from the init section; reading it therefore always
// yields nil for a sandbox created with a spec env. Parsing the raw config is
// the only way to observe what the guest actually holds.
//
// The representation is stable across releases: the runtime's catalog encoder
// lists config.env among the collections that "have the same representation in
// every supported release" (sdk/rust/lib/db/encoding.rs), so this holds for
// sandboxes written by older builds as well. A document whose env is shaped
// differently does not decode into the slice below and surfaces as an error,
// which the caller treats as a refusal.
type persistedGuestEnv struct {
	Env []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"env"`
}

func parseGuestEnv(configJSON string) (map[string]string, error) {
	var raw persistedGuestEnv
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return nil, fmt.Errorf("decode persisted sandbox config: %w", err)
	}
	env := make(map[string]string, len(raw.Env))
	for _, entry := range raw.Env {
		env[entry.Key] = entry.Value
	}
	return env, nil
}

// persistedConfigReader is the part of an SDK sandbox handle the guest-env
// reader needs. Naming it keeps the reader verifiable without a live sandbox:
// the real handle's ConfigJSON carries the persisted document, whereas its
// typed SandboxConfig is blind to the spec env (see persistedGuestEnv).
type persistedConfigReader interface {
	ConfigJSON() string
}

func guestEnvFromHandle(h persistedConfigReader) (map[string]string, error) {
	return parseGuestEnv(h.ConfigJSON())
}

// Env returns the persisted guest environment. Earlier versions stored the
// real ALBERT_API_KEY here in isolation full, so it is read back to detect
// sandboxes that must be recreated rather than booted with a plaintext key.
func (sdkMSBClient) Env(ctx context.Context, name string) (map[string]string, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return nil, err
	}
	return guestEnvFromHandle(h)
}

func (sdkMSBClient) Logs(name string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	stream, err := h.LogStream(ctx, msb.LogStreamOptions{Follow: true})
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		entry, err := stream.Recv(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if entry == nil {
			return nil
		}
		if _, err := os.Stdout.Write(entry.Data); err != nil {
			return err
		}
	}
}

func (sdkMSBClient) Shell(name string) (err error) {
	ctx := context.Background()
	sb, err := connectMSBSandbox(ctx, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sb.Close()) }()
	code, err := sb.Attach(ctx, "/bin/bash")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("sandbox shell exited %d", code)
	}
	return nil
}

// AttachInteractive runs cmd interactively in the sandbox with cwd as the
// working directory, blocking until it exits. It is the isolation-full TUI
// channel; the host terminal is passed through by the SDK attach stream.
func (sdkMSBClient) AttachInteractive(ctx context.Context, name, cmd, cwd string) (int, error) {
	sb, err := connectMSBSandbox(ctx, name)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sb.Close() }()
	return sb.AttachWith(ctx, cmd, nil, msb.WithAttachCwd(cwd))
}
