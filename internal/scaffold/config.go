package scaffold

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
)

const TGFModule = "github.com/thkhxm/tgf/v2"

var GeneratorVersion = detectGeneratorVersion()

type Config struct {
	Module     string
	Dir        string
	Profile    string
	Data       string
	Protocol   string
	Deploy     string
	Security   string
	TGFVersion string
	Force      bool
	GitInit    bool
	SkipChecks bool
}

func (c *Config) normalize() {
	c.Module = strings.TrimSpace(c.Module)
	c.Dir = strings.TrimSpace(c.Dir)
	c.Profile = strings.ToLower(strings.TrimSpace(c.Profile))
	c.Data = strings.ToLower(strings.TrimSpace(c.Data))
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	c.Deploy = strings.ToLower(strings.TrimSpace(c.Deploy))
	c.Security = strings.ToLower(strings.TrimSpace(c.Security))
	c.TGFVersion = strings.TrimSpace(c.TGFVersion)
	if c.Profile == "" {
		c.Profile = "single"
	}
	if c.Data == "" {
		if c.Profile == "distributed" {
			c.Data = "redis"
		} else {
			c.Data = "none"
		}
	}
	if c.Protocol == "" {
		if c.Profile == "http-rest" || c.Profile == "http-rpc" {
			c.Protocol = "none"
		} else {
			c.Protocol = "tcp"
		}
	}
	if c.Deploy == "" {
		c.Deploy = "local"
	}
	if c.Security == "" {
		c.Security = "dev-generated"
	}
	if c.TGFVersion == "" {
		c.TGFVersion = "latest"
	}
}

func (c *Config) Validate() error {
	c.normalize()
	if c.Module == "" {
		return fmt.Errorf("--module is required")
	}
	if err := module.CheckPath(c.Module); err != nil {
		return fmt.Errorf("invalid Go module path %q: %w", c.Module, err)
	}
	if c.Dir == "" {
		return fmt.Errorf("--dir is required")
	}
	if filepath.Clean(c.Dir) == "." {
		return fmt.Errorf("--dir must name a new project directory, not the current directory")
	}
	if !oneOf(c.Profile, "single", "distributed", "http-rest", "http-rpc") {
		return fmt.Errorf("invalid --profile %q", c.Profile)
	}
	if !oneOf(c.Data, "none", "redis", "redis-mysql") {
		return fmt.Errorf("invalid --data %q", c.Data)
	}
	if !oneOf(c.Protocol, "none", "tcp", "websocket", "kcp", "all") {
		return fmt.Errorf("invalid --protocol %q", c.Protocol)
	}
	if !oneOf(c.Deploy, "local", "compose", "k8s") {
		return fmt.Errorf("invalid --deploy %q", c.Deploy)
	}
	if !oneOf(c.Security, "dev-generated", "external") {
		return fmt.Errorf("invalid --security %q", c.Security)
	}
	if c.TGFVersion == "" {
		return fmt.Errorf("--tgf-version must be latest, a tag, or a commit")
	}
	if c.Profile == "distributed" && c.Data == "none" {
		return fmt.Errorf("distributed profile requires --data redis or redis-mysql")
	}
	if (c.Profile == "http-rest" || c.Profile == "http-rpc") && c.Protocol != "none" {
		return fmt.Errorf("%s profile requires --protocol none", c.Profile)
	}
	if (c.Profile == "single" || c.Profile == "distributed") && c.Protocol == "none" {
		return fmt.Errorf("%s profile requires tcp, websocket, kcp, or all protocol", c.Profile)
	}
	return nil
}

func detectGeneratorVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
