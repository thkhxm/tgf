package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectGoModRequiresPinnedDirectDependency(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\ngo 1.26.0\n\nrequire "+TGFModule+" v2.1.0\n")
	module, version, err := inspectGoMod(dir)
	if err != nil || module != "example.com/app" || version != "v2.1.0" {
		t.Fatalf("inspectGoMod() = %q, %q, %v", module, version, err)
	}
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\nrequire "+TGFModule+" v2.1.0\nreplace "+TGFModule+" => ../tgf\n")
	if _, _, err := inspectGoMod(dir); err == nil {
		t.Fatal("expected local replace rejection")
	}
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/app\n\nrequire "+TGFModule+" v2.1.0\nreplace github.com/thkhxm/rpcx/v2 => ../rpcx\n")
	if _, _, err := inspectGoMod(dir); err == nil {
		t.Fatal("expected local rpcx replace rejection")
	}
}

func TestParseListedModuleProvenance(t *testing.T) {
	t.Parallel()
	valid := `{"Path":"github.com/thkhxm/tgf/v2","Version":"v2.1.0"}`
	if got, err := parseListedModule([]byte(valid)); err != nil || got.Version != "v2.1.0" {
		t.Fatalf("valid provenance rejected: %+v %v", got, err)
	}
	invalid := []string{
		`{"Path":"github.com/thkhxm/tgf/v2","Main":true}`,
		`{"Path":"github.com/thkhxm/tgf/v2","Version":"v2.1.0","Replace":{"Path":"../tgf"}}`,
		`{"Path":"github.com/thkhxm/tgf/v2"}`,
		`{"Path":"example.com/not-tgf","Version":"v2.1.0"}`,
	}
	for _, input := range invalid {
		if _, err := parseListedModule([]byte(input)); err == nil {
			t.Fatalf("invalid provenance accepted: %s", input)
		}
	}
}

func TestInspectWorkspaceAllowsProjectWorkspaceAndRejectsLocalTGF(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.26.0\n")
	work := filepath.Join(root, "go.work")
	writeTestFile(t, work, "go 1.26.0\n\nuse ./app\n")
	runner := runnerFunc(func(context.Context, string, string, ...string) ([]byte, error) { return []byte(work + "\n"), nil })
	if got, err := inspectWorkspace(context.Background(), runner, app); err != nil || got != work {
		t.Fatalf("project workspace rejected: got=%q err=%v", got, err)
	}

	localTGF := filepath.Join(root, "tgf")
	if err := os.Mkdir(localTGF, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(localTGF, "go.mod"), "module "+TGFModule+"\n\ngo 1.26.0\n")
	writeTestFile(t, work, "go 1.26.0\n\nuse (\n\t./app\n\t./tgf\n)\n")
	if _, err := inspectWorkspace(context.Background(), runner, app); err == nil || !strings.Contains(err.Error(), "uses local") {
		t.Fatalf("local tgf workspace was not rejected: %v", err)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
