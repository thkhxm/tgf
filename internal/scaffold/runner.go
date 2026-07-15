package scaffold

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type commandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	command := commandPath(name)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w\n%s", name, args, err, out)
	}
	return out, nil
}

func commandPath(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	if name == "go" {
		roots := []string{os.Getenv("GOROOT"), build.Default.GOROOT}
		if runtime.GOOS == "windows" {
			roots = append(roots, `C:\Program Files\Go`)
		} else {
			roots = append(roots, "/usr/local/go", "/usr/lib/go")
		}
		for _, root := range roots {
			if root == "" {
				continue
			}
			candidate := filepath.Join(root, "bin", "go"+executableSuffix())
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return name
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
