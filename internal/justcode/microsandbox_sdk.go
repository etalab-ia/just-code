package justcode

import (
	"context"
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

// Kept separate from Create so tests can verify the actual SDK options without
// loading native code or creating a VM.
func msbCreateOptions(spec msbSandboxSpec) []msb.SandboxOption {
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
		msb.WithSecrets(msb.Secret.Env("ALBERT_API_KEY", spec.APIKey, msb.SecretEnvOptions{
			Allow: spec.AllowHosts,
		})),
		msb.WithScripts(map[string]string{"start": spec.StartScript}),
	}
}

func (sdkMSBClient) Create(ctx context.Context, spec msbSandboxSpec) error {
	sb, err := msb.CreateSandbox(ctx, msbSandbox, msbCreateOptions(spec)...)
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

func msbNextStartOptions(env map[string]string, apiKey string) msb.ModifyOptions {
	return msb.ModifyOptions{
		Env: env,
		Secrets: map[string]msb.SecretModifySpec{
			"ALBERT_API_KEY": {Value: apiKey, AllowedHosts: []string{msbAllowHost}},
		},
		Policy: msb.ModificationPolicyNextStart,
	}
}

func (sdkMSBClient) ModifyNextStart(ctx context.Context, name string, env map[string]string, apiKey string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return err
	}
	_, err = h.Modify(ctx, msbNextStartOptions(env, apiKey))
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

func (sdkMSBClient) WorkspaceMount(ctx context.Context, name string) (string, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return "", err
	}
	cfg, err := h.Config()
	if err != nil {
		return "", err
	}
	return cfg.Volumes["/workspace"].Bind, nil
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

func (sdkMSBClient) Logs() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	h, err := msb.GetSandbox(ctx, msbSandbox)
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

func (sdkMSBClient) Shell() (err error) {
	ctx := context.Background()
	sb, err := connectMSBSandbox(ctx, msbSandbox)
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
func (sdkMSBClient) AttachInteractive(ctx context.Context, cmd, cwd string) (int, error) {
	sb, err := connectMSBSandbox(ctx, msbSandbox)
	if err != nil {
		return 0, err
	}
	defer func() { _ = sb.Close() }()
	return sb.AttachWith(ctx, cmd, nil, msb.WithAttachCwd(cwd))
}
