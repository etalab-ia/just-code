package justcode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readArchiveEntries decodes a transfer payload into name -> header. The
// payload is a gzip stream, so assertions on its raw bytes prove nothing:
// every property under test has to be read out of the decoded entries.
func readArchiveEntries(t *testing.T, payload []byte) map[string]*tar.Header {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = h
	}
	return out
}

// TestTransferArchivePreservesModificationTimes pins #194: every entry was
// stamped with the Unix epoch, so a synced file looked older than any source
// and date-comparing tools (make, bundlers, build caches) rebuilt everything
// on each sync.
func TestTransferArchivePreservesModificationTimes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b/c.txt", "c\n")
	gitInitForTransfer(t, dir)

	// A distinctive, non-epoch time: a file dated 1970 for any reason must
	// fail the assertion below, whatever the mechanism.
	stamp := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, rel := range []string{"a.txt", filepath.Join("b", "c.txt")} {
		if err := os.Chtimes(filepath.Join(dir, rel), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	entries := readArchiveEntries(t, payload)

	for _, rel := range []string{"a.txt", "b/c.txt"} {
		h, ok := entries[rel]
		if !ok {
			t.Fatalf("%s missing from the archive: %v", rel, len(entries))
		}
		if h.ModTime.UTC().IsZero() || h.ModTime.UTC().Unix() == 0 {
			t.Fatalf("%s carries the epoch timestamp: %v", rel, h.ModTime)
		}
		if !h.ModTime.UTC().Equal(stamp) {
			t.Fatalf("%s mtime = %v, want %v", rel, h.ModTime.UTC(), stamp)
		}
	}
}

// TestTransferArchiveIsReproducibleForTheSameInput guards the property the
// fixed epoch was originally there to provide. Preserving real mtimes keeps it,
// because the same files carry the same mtimes — but only if nothing else in
// the payload varies, so it is worth asserting rather than assuming.
func TestTransferArchiveIsReproducibleForTheSameInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b/c.txt", "c\n")
	gitInitForTransfer(t, dir)
	stamp := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, rel := range []string{"a.txt", filepath.Join("b", "c.txt")} {
		if err := os.Chtimes(filepath.Join(dir, rel), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two builds of the same unchanged manifest differ")
	}
}
