package justcode

import (
	"fmt"
	"runtime"
)

// HostResources describes the CPU and effective memory ceiling visible to
// just-code. On Linux, MemoryMB respects a smaller cgroup limit.
type HostResources struct {
	CPUs     int
	MemoryMB int
}

// DetectHostResources reports the execution host's logical CPUs and memory
// ceiling. A memory error still returns the CPU count for partial guidance.
func DetectHostResources() (HostResources, error) {
	resources := HostResources{CPUs: runtime.NumCPU()}
	bytes, err := hostMemoryBytes()
	if err != nil {
		return resources, err
	}
	resources.MemoryMB = int(bytes / (1024 * 1024))
	return resources, nil
}

// BrowserResourceGuidance reports the requested guest sizing in host context
// without inventing a browser workload peak that has not been measured.
func BrowserResourceGuidance(host HostResources, guestCPUs, guestMemoryMB int, detectionErr error) string {
	if guestCPUs <= 0 {
		guestCPUs = DefaultSandboxCPUs
	}
	if guestMemoryMB <= 0 {
		guestMemoryMB = DefaultSandboxMemoryMB
	}
	message := fmt.Sprintf("Browser guest: %d vCPU, %d MiB RAM, 8192 MiB root disk. P03 measured about 2.6 GiB disk use; 662 MiB was an unused Playwright browser-download cache, which this setup does not populate. Browser-load RAM peak was not measured, so no larger RAM recommendation is justified.", guestCPUs, guestMemoryMB)
	if host.CPUs > 0 {
		message += fmt.Sprintf(" Host: %d logical CPUs", host.CPUs)
	}
	if host.MemoryMB > 0 {
		message += fmt.Sprintf(" / %d MiB memory ceiling", host.MemoryMB)
	}
	if detectionErr != nil {
		message += ". Memory detection failed; treat host capacity as unknown"
	} else if host.MemoryMB > 0 && guestMemoryMB >= host.MemoryMB {
		message += ". The guest request equals or exceeds the detected host memory ceiling; lower JUST_CODE_MEMORY_MB or provide a larger host"
	} else if host.CPUs > 0 && guestCPUs > host.CPUs {
		message += ". The guest vCPU request overcommits the detected host logical CPU count; lower JUST_CODE_CPUS if guest latency is unacceptable"
	} else if host.CPUs > 0 && host.MemoryMB > 0 {
		message += ". The request fits the detected CPU and memory ceilings; this does not account for other host workloads"
	}
	return message
}
