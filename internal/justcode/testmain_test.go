package justcode

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	stateDir, err := os.MkdirTemp("", "just-code-test-state-")
	if err != nil {
		panic(err)
	}
	projectUpdateStateDirFn = func() string { return stateDir }
	code := m.Run()
	_ = os.RemoveAll(stateDir)
	os.Exit(code)
}
