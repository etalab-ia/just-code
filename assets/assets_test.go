package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeWritesEveryAsset(t *testing.T) {
	dir := t.TempDir()
	got, err := Materialize(dir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got != dir {
		t.Fatalf("Materialize returned %q, want %q", got, dir)
	}
	for _, name := range Files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("asset %s not written: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("asset %s is empty", name)
		}
	}
}

func TestMaterializeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	// A second call must refresh the files without leaving temp files behind.
	if _, err := Materialize(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(Files) {
		t.Fatalf("expected %d files, got %d (%v)", len(Files), len(entries), entries)
	}
}

func TestEmbeddedBootstrapLooksReal(t *testing.T) {
	data, err := Read("tart-bootstrap.sh")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"OPENCODE_SERVER_PASSWORD", "TART_MTU", "opencode serve"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("bootstrap missing %q", want)
		}
	}
}

func TestEmbeddedComposePreservesEmptyPassword(t *testing.T) {
	data, err := Read("docker-compose.yml")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(data), "${OPENCODE_SERVER_PASSWORD-albert-dev-pass}") {
		t.Fatalf("compose should use the `-` default so an empty password is preserved:\n%s", data)
	}
	if strings.Contains(string(data), "${OPENCODE_SERVER_PASSWORD:-albert-dev-pass}") {
		t.Fatalf("compose still uses `:-`, which overrides an explicitly empty password")
	}
}
