package assets

import (
	"testing"
)

func TestReadEveryAsset(t *testing.T) {
	for _, name := range Files {
		data, err := Read(name)
		if err != nil {
			t.Fatalf("Read(%q): %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("asset %s is empty", name)
		}
	}
}

func TestReadRejectsUnknownAsset(t *testing.T) {
	if _, err := Read("unknown"); err == nil {
		t.Fatal("expected an error for an unknown asset")
	}
}
