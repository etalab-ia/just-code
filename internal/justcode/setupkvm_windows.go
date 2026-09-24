//go:build windows

package justcode

// probeKVM is never reached on Windows (probeVirtualizationPrimitive's
// default branch runs instead); the stub keeps the file set building.
func probeKVM() (bool, string) { return false, "" }

func probeKVMFailure() string { return "/dev/kvm is not a Windows concept" }
