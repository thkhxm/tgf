package example_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scenario struct {
	id     string
	dir    string
	marker string
}

var scenarioMatrix = []scenario{
	{id: "single-process-game", dir: "single_process", marker: "WithSingleProcess()"},
	{id: "distributed-game", dir: "distributed_game", marker: "WithService(service.NewUserService())"},
	{id: "http-rest", dir: "http_rest", marker: "WithHTTPService(web.Options"},
	{id: "http-rpc", dir: "http_rpc", marker: "BackendFromRequest"},
	{id: "gateway-tcp", dir: "distributed_game", marker: `TCPPort: "8082"`},
	{id: "gateway-ws", dir: "distributed_game", marker: `WSPath:  "/ws"`},
	{id: "gateway-kcp", dir: "distributed_game", marker: "NewKCPBuilder"},
	{id: "robot-self-test", dir: "robot_test", marker: "probeRobot"},
	{id: "redis-mysql-write-behind", dir: "db_cache", marker: "WithLongevityCache"},
	{id: "config-reload", dir: "config_reload", marker: "config.Reload()"},
	{id: "game-config", dir: "game_config", marker: "component.ReloadGameConf()"},
	{id: "logging", dir: "log_usage", marker: "log.InfoTagW"},
	{id: "metrics-trace", dir: "metrics_trace", marker: "trace.StartSpan"},
	{id: "rpc-policy", dir: "rpc_policy", marker: "WithMethodPolicy"},
	{id: "util", dir: "util_tools", marker: "util.Go"},
}

func TestDeclaredScenarioMatrixHasRunnableSourceAndDocumentation(t *testing.T) {
	doc, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read example README: %v", err)
	}
	seen := make(map[string]struct{}, len(scenarioMatrix))
	for _, item := range scenarioMatrix {
		if _, ok := seen[item.id]; ok {
			t.Fatalf("duplicate scenario id %q", item.id)
		}
		seen[item.id] = struct{}{}
		if !strings.Contains(string(doc), "`"+item.id+"`") {
			t.Errorf("scenario %q is missing from example/README.md", item.id)
		}
		source := readRunnableGoSource(t, item.dir)
		if !strings.Contains(source, item.marker) {
			t.Errorf("scenario %q source under %s lacks marker %q", item.id, item.dir, item.marker)
		}
	}
}

func TestExampleLifecycleAndArtifactGovernance(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".exe") {
			t.Errorf("tracked/generated executable must not live under example: %s", path)
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, forbidden := range []string{`"os/signal"`, "signal.Notify(", "os.Exit(0)"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s owns process lifecycle via forbidden %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
}

func TestDistributedGatewayConfigurationFailsClosed(t *testing.T) {
	source := readRunnableGoSource(t, "distributed_game/cmd/gateway")
	for _, required := range []string{"loadKCPAEADKey()", "os.Exit(1)"} {
		if !strings.Contains(source, required) {
			t.Errorf("distributed gateway must fail closed with %q", required)
		}
	}
}

func TestPublicExampleDocsUseV2NamingAndDistributedMapping(t *testing.T) {
	for _, path := range []string{"README.md", filepath.Join("..", "README.md")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		legacyMajor := "v" + "3"
		if strings.Contains(strings.ToLower(text), legacyMajor) {
			t.Errorf("%s still contains legacy major-version naming drift", path)
		}
		if !strings.Contains(text, "distributed_game") {
			t.Errorf("%s does not map distributed game to distributed_game", path)
		}
	}
}

func readRunnableGoSource(t *testing.T, dir string) string {
	t.Helper()
	var source strings.Builder
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("read source under %s: %v", dir, err)
	}
	if source.Len() == 0 {
		t.Fatalf("scenario directory %s has no runnable Go source", dir)
	}
	return source.String()
}
