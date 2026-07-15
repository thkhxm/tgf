package scaffold

import (
	"bytes"
	"context"
	"errors"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runnerFunc func(context.Context, string, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	return f(ctx, dir, name, args...)
}

func TestGenerateAllProfilesAtFixedVersion(t *testing.T) {
	t.Parallel()
	profiles := []Config{
		{Module: "example.com/single", Profile: "single", Data: "redis-mysql", Protocol: "all", Deploy: "compose"},
		{Module: "example.com/distributed", Profile: "distributed", Data: "redis", Protocol: "tcp", Deploy: "k8s"},
		{Module: "example.com/rest", Profile: "http-rest", Data: "none", Protocol: "none", Deploy: "local"},
		{Module: "example.com/http-rpc", Profile: "http-rpc", Data: "redis", Protocol: "none", Deploy: "compose"},
	}
	for _, cfg := range profiles {
		cfg := cfg
		t.Run(cfg.Profile, func(t *testing.T) {
			t.Parallel()
			cfg.Dir = filepath.Join(t.TempDir(), "project")
			cfg.Security = "dev-generated"
			cfg.TGFVersion = "v2.1.0"
			cfg.SkipChecks = true
			g := generator{runner: fixedVersionRunner("v2.1.0"), random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 256))}
			result, err := g.generate(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if result.TGFVersion != "v2.1.0" || result.ChecksRun {
				t.Fatalf("unexpected result: %+v", result)
			}
			assertNoTemplateMarkers(t, cfg.Dir)
			if _, err := os.Stat(filepath.Join(cfg.Dir, ".env.example")); err != nil {
				t.Fatalf("missing .env.example: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.Dir, ".env.dev")); err != nil {
				t.Fatalf("missing .env.dev: %v", err)
			}
			if cfg.Deploy == "local" {
				if _, err := os.Stat(filepath.Join(cfg.Dir, "Dockerfile")); !os.IsNotExist(err) {
					t.Fatalf("local deployment unexpectedly emitted Dockerfile")
				}
			} else if _, err := os.Stat(filepath.Join(cfg.Dir, "Dockerfile")); err != nil {
				t.Fatalf("missing Dockerfile: %v", err)
			}
		})
	}
}

func TestForwardGenerateAllProfilesAtFixedVersion(t *testing.T) {
	if os.Getenv("TGFCTL_FORWARD_TEST") == "" {
		t.Skip("set TGFCTL_FORWARD_TEST=1 to run GitHub-module compile/vet/test/provenance checks")
	}
	root := t.TempDir()
	profiles := []Config{
		{Module: "example.com/forward-single", Profile: "single", Data: "none", Protocol: "tcp"},
		{Module: "example.com/forward-distributed", Profile: "distributed", Data: "redis", Protocol: "websocket"},
		{Module: "example.com/forward-rest", Profile: "http-rest", Data: "none", Protocol: "none"},
		{Module: "example.com/forward-http-rpc", Profile: "http-rpc", Data: "redis-mysql", Protocol: "none"},
	}
	for _, cfg := range profiles {
		cfg.Dir = filepath.Join(root, cfg.Profile)
		cfg.Security = "dev-generated"
		cfg.TGFVersion = "v2.1.0"
		if _, err := Generate(context.Background(), cfg); err != nil {
			t.Fatalf("%s forward generation failed: %v", cfg.Profile, err)
		}
	}
}

func TestLatestResolutionWhenEnabled(t *testing.T) {
	if os.Getenv("TGFCTL_LATEST_TEST") == "" {
		t.Skip("set TGFCTL_LATEST_TEST=1 when network access is available")
	}
	cfg := Config{Module: "example.com/latest", Dir: filepath.Join(t.TempDir(), "latest"), Profile: "http-rest", Protocol: "none", Security: "external", TGFVersion: "latest"}
	result, err := Generate(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !isV2Version(result.TGFVersion) || result.TGFVersion == "latest" {
		t.Fatalf("latest did not resolve to a fixed v2 version: %+v", result)
	}
}

func TestGeneratedSecretsAreFixedLengthAndNotInManifest(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "project")
	cfg := Config{Module: "example.com/secure", Dir: dir, Profile: "single", Data: "none", Protocol: "kcp", Security: "dev-generated", TGFVersion: "v2.1.0", SkipChecks: true}
	g := generator{runner: fixedVersionRunner("v2.1.0"), random: bytes.NewReader(bytes.Repeat([]byte{0xab}, 128))}
	if _, err := g.generate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	envBytes, err := os.ReadFile(filepath.Join(dir, ".env.dev"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(envBytes)
	for _, key := range []string{"LoginTokenSecret", "KCP_AEAD_KEY_HEX"} {
		value := envValue(env, key)
		if len(value) != 64 {
			t.Fatalf("%s length = %d, want 64", key, len(value))
		}
	}
	manifestBytes, err := os.ReadFile(filepath.Join(dir, ".tgf-scaffold.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestBytes, []byte(strings.Repeat("ab", 32))) {
		t.Fatal("manifest contains generated secret")
	}
}

func TestDestinationConflictAndFailureLeaveNoPartialProject(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	existing := filepath.Join(parent, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	base := Config{Module: "example.com/project", Dir: existing, Profile: "single", Protocol: "tcp", TGFVersion: "v2.1.0", SkipChecks: true}
	if _, err := (generator{runner: fixedVersionRunner("v2.1.0"), random: bytes.NewReader(make([]byte, 128))}).generate(context.Background(), base); err == nil {
		t.Fatal("expected existing destination to fail without --force")
	}
	base.Force = true
	if _, err := (generator{runner: fixedVersionRunner("v2.1.0"), random: bytes.NewReader(make([]byte, 128))}).generate(context.Background(), base); err != nil {
		t.Fatalf("force on empty destination failed: %v", err)
	}

	failed := filepath.Join(parent, "failed")
	failing := Config{Module: "example.com/failed", Dir: failed, Profile: "single", Protocol: "tcp", TGFVersion: "v2.1.0"}
	fake := runnerFunc(func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name == "go" && len(args) == 4 && args[0] == "mod" && args[1] == "download" {
			return []byte(`{"Path":"github.com/thkhxm/tgf/v2","Version":"v2.1.0"}`), nil
		}
		if name == "go" && strings.Join(args, " ") == "env GOWORK" {
			return []byte("off\n"), nil
		}
		return nil, errors.New("injected check failure")
	})
	if _, err := (generator{runner: fake, random: bytes.NewReader(make([]byte, 128))}).generate(context.Background(), failing); err == nil {
		t.Fatal("expected injected check failure")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatalf("failed generation left destination behind: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".failed.tmp-") {
			t.Fatalf("failed generation left staging directory %s", entry.Name())
		}
	}
}

func fixedVersionRunner(version string) runnerFunc {
	return func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name == "go" && len(args) == 4 && args[0] == "mod" && args[1] == "download" && args[2] == "-json" {
			return []byte(`{"Path":"github.com/thkhxm/tgf/v2","Version":"` + version + `"}`), nil
		}
		return nil, errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
	}
}

func assertNoTemplateMarkers(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if containsTemplateMarker(string(body)) {
			t.Errorf("unresolved template marker in %s", path)
		}
		if filepath.Ext(path) == ".go" {
			formatted, formatErr := format.Source(body)
			if formatErr != nil {
				return formatErr
			}
			if !bytes.Equal(body, formatted) {
				t.Errorf("generated Go file is not gofmt-clean: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func envValue(contents, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(contents, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
