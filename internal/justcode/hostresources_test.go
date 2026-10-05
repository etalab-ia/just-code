package justcode

import (
	"errors"
	"strings"
	"testing"
)

var errTestHostMemory = errors.New("memory unavailable")

func TestBrowserResourceGuidanceUsesHostCapacityWithoutInventingPeak(t *testing.T) {
	got := BrowserResourceGuidance(HostResources{CPUs: 8, MemoryMB: 16384}, 2, 4096, nil)
	for _, want := range []string{"2 vCPU", "4096 MiB RAM", "8192 MiB root disk", "8 logical CPUs", "16384 MiB memory ceiling", "2.6 GiB", "662 MiB", "RAM peak was not measured", "does not account for other host workloads"} {
		if !strings.Contains(got, want) {
			t.Errorf("resource guidance %q lacks %q", got, want)
		}
	}
}

func TestBrowserResourceGuidanceWarnsWhenGuestExceedsHost(t *testing.T) {
	got := BrowserResourceGuidance(HostResources{CPUs: 1, MemoryMB: 2048}, 2, 4096, nil)
	if !strings.Contains(got, "equals or exceeds the detected host memory ceiling") {
		t.Fatalf("memory pressure warning missing: %q", got)
	}
	if strings.Contains(got, "lower JUST_CODE_CPUS") {
		t.Fatalf("memory warning should not claim CPU overcommit was handled: %q", got)
	}
}

func TestBrowserResourceGuidanceReportsUnknownMemoryDetection(t *testing.T) {
	got := BrowserResourceGuidance(HostResources{CPUs: 4}, 2, 4096, errTestHostMemory)
	if !strings.Contains(got, "Memory detection failed; treat host capacity as unknown") || !strings.Contains(got, "4 logical CPUs") {
		t.Fatalf("partial host detection guidance = %q", got)
	}
}
