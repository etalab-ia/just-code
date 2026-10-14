package justcode

import (
	"context"
	"strings"
	"testing"
)

// guestFSClient is the fake sandbox client with a small in-memory guest
// filesystem, so the publish path can be exercised for real: writes land in
// the map, the atomic rename actually moves the temporary file, and reads see
// what was written. The base fake records calls and provides every other
// method unchanged.
type guestFSClient struct {
	*fakeMSBClient
	files map[string]string
}

func newGuestFSClient() *guestFSClient {
	return &guestFSClient{fakeMSBClient: &fakeMSBClient{}, files: map[string]string{}}
}

func (c *guestFSClient) WriteFile(_ context.Context, name, guestPath string, data []byte) error {
	c.record("writefile " + name + " " + guestPath)
	c.files[guestPath] = string(data)
	return nil
}

// Exec understands only the three shapes this package issues: mkdir -p, the
// atomic mv, and rm -f. Anything else is a test bug, not a silent success.
func (c *guestFSClient) Exec(_ context.Context, name, command string) (int, string, error) {
	c.record("exec " + name + " " + command)
	switch {
	case strings.HasPrefix(command, "mkdir -p "):
		return 0, "", nil
	case strings.HasPrefix(command, "mv -f "):
		parts := strings.Fields(command)
		if len(parts) != 4 {
			return 1, "unexpected mv shape", nil
		}
		src, dst := unquoteGuest(parts[2]), unquoteGuest(parts[3])
		content, ok := c.files[src]
		if !ok {
			return 1, "no such file", nil
		}
		delete(c.files, src)
		c.files[dst] = content
		return 0, "", nil
	case strings.HasPrefix(command, "rm -f "):
		delete(c.files, unquoteGuest(strings.TrimPrefix(command, "rm -f ")))
		return 0, "", nil
	default:
		return 1, "unsupported command", nil
	}
}

func (c *guestFSClient) ExecCapture(_ context.Context, name, command string) (string, string, int, error) {
	c.record("execcapture " + name + " " + command)
	if !strings.HasPrefix(command, "cat ") {
		return "", "unsupported command", 1, nil
	}
	path := unquoteGuest(strings.TrimSuffix(strings.TrimPrefix(command, "cat "), " 2>/dev/null"))
	content, ok := c.files[path]
	if !ok {
		return "", "", 1, nil
	}
	return content, "", 0, nil
}

func unquoteGuest(value string) string {
	return strings.Trim(value, "'\"")
}

func newGuestFSRuntime(t *testing.T, files map[string]string) (*MicrosandboxRuntime, *guestFSClient) {
	t.Helper()
	client := newGuestFSClient()
	for path, content := range files {
		client.files[path] = content
	}
	// newTestMicrosandbox wires the shared defaults (state dir, probe, the
	// "guest already provisioned" exec default); the client is replaced
	// afterwards with this wrapper, which keeps the file semantics.
	m := newTestMicrosandbox(t, client.fakeMSBClient)
	m.Client = client
	return m, client
}

func testDelivery(component, name, guestEnv, marker string, hosts ...string) ProjectSecretDelivery {
	return ProjectSecretDelivery{
		ProjectSecretBinding: ProjectSecretBinding{
			Profile: "development", Component: component, Name: name,
			GuestEnv: guestEnv, Destinations: hosts,
		},
		Reference: "project-env-ns-" + name,
		Marker:    marker,
	}
}

func TestBuildGuestSecretFilesRendersMarkersOnly(t *testing.T) {
	files, err := BuildGuestSecretFiles([]ProjectSecretDelivery{
		testDelivery("apps/api/.env", "API_KEY", "JC_APPS_API_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
		testDelivery(".env", "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_bbb__", "api.example.com"),
		testDelivery("apps/api/.env", "DB_URL", "JC_APPS_API_DB_URL", "__JUST_CODE_SECRET_ccc__", "db.example.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Component != ".env" || files[1].Component != "apps/api/.env" {
		t.Fatalf("one file per component, sorted: %+v", files)
	}
	for _, f := range files {
		if !isManagedGuestSecretFile(f.Content) {
			t.Fatalf("%s must carry the managed header: %q", f.Component, f.Content)
		}
		if strings.Contains(f.Content, "project-env-") {
			t.Fatalf("%s must not leak the credential reference", f.Component)
		}
	}
	api := files[1].Content
	if !strings.Contains(api, "API_KEY=__JUST_CODE_SECRET_aaa__\n") || !strings.Contains(api, "DB_URL=__JUST_CODE_SECRET_ccc__\n") {
		t.Fatalf("api component content: %q", api)
	}
	root := files[0].Content
	if !strings.Contains(root, "API_KEY=__JUST_CODE_SECRET_bbb__\n") {
		t.Fatalf("root component content: %q", root)
	}
	// Two components may share a variable name with different markers.
	if strings.Contains(root, "__JUST_CODE_SECRET_aaa__") || strings.Contains(api, "__JUST_CODE_SECRET_bbb__") {
		t.Fatal("component values must not bleed into each other")
	}
	// Variables are ordered so the file is stable across runs.
	if strings.Index(api, "API_KEY=") > strings.Index(api, "DB_URL=") {
		t.Fatal("variables must be rendered in sorted order")
	}
}

func TestBuildGuestSecretFilesRejectsUnsafeComponents(t *testing.T) {
	for _, component := range []string{"../.env", "/tmp/.env", "apps/../../.env", "apps\\api\\.env", "notes.txt", ""} {
		if _, err := BuildGuestSecretFiles([]ProjectSecretDelivery{
			testDelivery(component, "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
		}); err == nil {
			t.Fatalf("component %q must be refused", component)
		}
	}
	if _, err := BuildGuestSecretFiles([]ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "", "api.example.com"),
	}); err == nil {
		t.Fatal("a delivery with no marker must be refused rather than rendered empty")
	}
	if _, err := BuildGuestSecretFiles([]ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "m", "api.example.com"),
		testDelivery(".env", "API_KEY", "JC_OTHER", "m2", "api.example.com"),
	}); err == nil {
		t.Fatal("two bindings for one variable in one component must be refused")
	}
}

func TestGuestWorkspaceDotenvPathStaysInsideWorkspace(t *testing.T) {
	got, err := guestWorkspaceDotenvPath("apps/api/.env")
	if err != nil || got != msbGuestWorkspace+"/apps/api/.env" {
		t.Fatalf("resolved path: %q, %v", got, err)
	}
	for _, component := range []string{"../escape.env", "/etc/passwd", "a/../../b.env"} {
		if _, err := guestWorkspaceDotenvPath(component); err == nil {
			t.Fatalf("component %q must not resolve", component)
		}
	}
}

func TestPlanGuestSecretPublish(t *testing.T) {
	desired, err := BuildGuestSecretFiles([]ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
	})
	if err != nil {
		t.Fatal(err)
	}
	apiPath := msbGuestWorkspace + "/.env"
	otherPath := msbGuestWorkspace + "/old/.env"

	// Nothing in the guest yet: a fresh write.
	plan, err := PlanGuestSecretPublish(desired, map[string]string{}, nil)
	if err != nil || len(plan.Writes) != 1 || len(plan.Conflicts) != 0 || len(plan.Removes) != 0 {
		t.Fatalf("fresh publish: %+v, %v", plan, err)
	}

	// Identical managed content: no write, so a restart is a true no-op.
	plan, err = PlanGuestSecretPublish(desired, map[string]string{apiPath: desired[0].Content}, []string{apiPath})
	if err != nil || len(plan.Writes) != 0 || len(plan.Unchanged) != 1 {
		t.Fatalf("idempotent publish: %+v, %v", plan, err)
	}

	// Changed managed content: rewrite.
	plan, err = PlanGuestSecretPublish(desired, map[string]string{apiPath: guestSecretHeaderPrefix + "\nOLD=1\n"}, []string{apiPath})
	if err != nil || len(plan.Writes) != 1 {
		t.Fatalf("managed update: %+v, %v", plan, err)
	}

	// A file the project ships is never overwritten.
	plan, err = PlanGuestSecretPublish(desired, map[string]string{apiPath: "API_KEY=the-projects-own-value\n"}, nil)
	if err != nil || len(plan.Writes) != 0 || len(plan.Conflicts) != 1 {
		t.Fatalf("conflict must be reported, not resolved: %+v, %v", plan, err)
	}
	// And the refusal is enforced at apply time too.
	m, _ := newGuestFSRuntime(t, map[string]string{apiPath: "API_KEY=the-projects-own-value\n"})
	if err := m.publishGuestSecretFiles(t.Context(), plan); err == nil {
		t.Fatal("applying a plan with a conflict must be refused")
	}

	// A previously managed file that is no longer wanted is removed.
	plan, err = PlanGuestSecretPublish(desired, map[string]string{apiPath: desired[0].Content, otherPath: guestSecretHeaderPrefix + "\nOLD=1\n"}, []string{apiPath, otherPath})
	if err != nil || len(plan.Removes) != 1 || plan.Removes[0] != otherPath {
		t.Fatalf("obsolete managed file: %+v, %v", plan, err)
	}

	// A file the user replaced with their own is theirs: never removed.
	plan, err = PlanGuestSecretPublish(desired, map[string]string{otherPath: "MINE=1\n"}, []string{otherPath})
	if err != nil || len(plan.Removes) != 0 || len(plan.Writes) != 1 {
		t.Fatalf("a user-replaced file must survive: %+v, %v", plan, err)
	}

	// No desired files at all: everything previously managed goes away.
	plan, err = PlanGuestSecretPublish(nil, map[string]string{apiPath: desired[0].Content}, []string{apiPath})
	if err != nil || len(plan.Removes) != 1 || len(plan.Writes) != 0 {
		t.Fatalf("revoking every approval must clean up: %+v, %v", plan, err)
	}
}

func TestSyncGuestSecretFilesWritesOnceAndCleansUp(t *testing.T) {
	m, client := newGuestFSRuntime(t, nil)
	deliveries := []ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
	}
	managed, err := m.syncGuestSecretFiles(t.Context(), deliveries, nil)
	if err != nil {
		t.Fatal(err)
	}
	guestPath := msbGuestWorkspace + "/.env"
	if len(managed) != 1 || managed[0] != guestPath {
		t.Fatalf("managed paths: %v", managed)
	}
	written, ok := client.files[guestPath]
	if !ok {
		t.Fatalf("the file was not written: %v", client.files)
	}
	if !strings.Contains(written, "__JUST_CODE_SECRET_aaa__") || strings.Contains(written, "project-env-") {
		t.Fatalf("written content: %q", written)
	}
	// The temporary file must not survive the atomic rename.
	for path := range client.files {
		if strings.HasSuffix(path, ".jc-tmp") {
			t.Fatalf("temporary file left behind: %s", path)
		}
	}

	// A second pass with identical content is a no-op.
	before := len(client.calls)
	if _, err := m.syncGuestSecretFiles(t.Context(), deliveries, managed); err != nil {
		t.Fatal(err)
	}
	for _, call := range client.calls[before:] {
		if strings.Contains(call, "mv -f") {
			t.Fatalf("an unchanged file was rewritten: %v", client.calls[before:])
		}
	}

	// Withdrawing every approval removes the managed file.
	if _, err := m.syncGuestSecretFiles(t.Context(), nil, managed); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.files[guestPath]; ok {
		t.Fatal("a withdrawn approval must remove the generated file")
	}
}

func TestSyncGuestSecretFilesRefusesToClobberProjectFile(t *testing.T) {
	guestPath := msbGuestWorkspace + "/.env"
	own := "API_KEY=the-projects-own-value\n"
	m, client := newGuestFSRuntime(t, map[string]string{guestPath: own})
	_, err := m.syncGuestSecretFiles(t.Context(), []ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "were not written by just-code") {
		t.Fatalf("the publish must be refused: %v", err)
	}
	if client.files[guestPath] != own {
		t.Fatal("the project's own file was modified")
	}
}

func TestSyncGuestSecretFilesWritesEachComponentSeparately(t *testing.T) {
	m, client := newGuestFSRuntime(t, nil)
	managed, err := m.syncGuestSecretFiles(t.Context(), []ProjectSecretDelivery{
		testDelivery(".env", "API_KEY", "JC_API_KEY", "__JUST_CODE_SECRET_bbb__", "api.example.com"),
		testDelivery("apps/api/.env", "API_KEY", "JC_APPS_API_API_KEY", "__JUST_CODE_SECRET_aaa__", "api.example.com"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 2 {
		t.Fatalf("two components must produce two managed paths: %v", managed)
	}
	// Same variable name, different components, different markers: the
	// framework's own load order is preserved because the files are separate.
	root := client.files[msbGuestWorkspace+"/.env"]
	nested := client.files[msbGuestWorkspace+"/apps/api/.env"]
	if !strings.Contains(root, "__JUST_CODE_SECRET_bbb__") || !strings.Contains(nested, "__JUST_CODE_SECRET_aaa__") {
		t.Fatalf("components were mixed up: root=%q nested=%q", root, nested)
	}
}

func TestGeneratedMarkerMatchesTheEnvironmentBinding(t *testing.T) {
	// The environment variable and the generated dotenv must expose the same
	// placeholder, or an application reading either source would send a value
	// the proxy does not recognize.
	store := newProjectSecretFakeStore()
	root, state := t.TempDir(), t.TempDir()
	s := projectSecretFixture(t, root, state, store)
	importProjectSecrets(t, s, "development", ".env", map[string]string{"API_KEY": "project-value"})
	if err := SetProjectSecretActiveProfile(DefaultFS, state, s.Namespace(), "development"); err != nil {
		t.Fatal(err)
	}
	if err := ApproveProjectSecretBinding(DefaultFS, state, s.Namespace(), ProjectSecretBinding{
		Profile: "development", Component: ".env", Name: "API_KEY",
		GuestEnv: "JC_API_KEY", Destinations: []string{"api.example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := s.ApprovedDeliveries()
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries: %+v, %v", deliveries, err)
	}
	if deliveries[0].Marker != ProjectSecretPlaceholder(s.Namespace(), "development", ".env", "API_KEY") {
		t.Fatalf("marker: %q", deliveries[0].Marker)
	}
	files, err := BuildGuestSecretFiles(deliveries)
	if err != nil || len(files) != 1 {
		t.Fatalf("files: %+v, %v", files, err)
	}
	if !strings.Contains(files[0].Content, "API_KEY="+deliveries[0].Marker+"\n") {
		t.Fatalf("the generated file must carry the binding's marker: %q", files[0].Content)
	}
}

var _ msbClient = (*guestFSClient)(nil)
