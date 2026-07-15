package scaffold

import (
	"os"
	"testing"
)

func TestCommandPathFindsGoToolchain(t *testing.T) {
	path := commandPath("go")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("commandPath(go) = %q: %v", path, err)
	}
}
