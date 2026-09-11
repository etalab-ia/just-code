package justcode

import (
	"path"
	"strings"
)

// managedVMPrefix marks VMs owned by just-code. `stop` removes every local
// Tart VM carrying this prefix, so it must not be reused for VMs created
// outside just-code.
const managedVMPrefix = "opencode-"

// VMName derives the stable Tart VM name from an image reference. It mirrors
// the justfile expression:
//
//	"opencode-" + replace(replace(trim_start_matches(file_name(image), "macos-"), ":", "-"), "@sha256", "-sha256")
func VMName(imageRef string) string {
	base := path.Base(imageRef)
	base = strings.TrimPrefix(base, "macos-")
	base = strings.ReplaceAll(base, ":", "-")
	base = strings.ReplaceAll(base, "@sha256", "-sha256")
	return managedVMPrefix + base
}
