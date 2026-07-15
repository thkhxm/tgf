package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

type Verification struct {
	Dir        string `json:"dir"`
	Module     string `json:"module"`
	TGFVersion string `json:"tgfVersion"`
	GoWork     string `json:"goWork,omitempty"`
}

type listedModule struct {
	Path    string
	Version string
	Main    bool
	Replace *listedModule
}

var managedModules = map[string]struct{}{
	TGFModule:                          {},
	"github.com/thkhxm/rpcx/v2":        {},
	"github.com/thkhxm/rpcx-consul/v2": {},
}

func Verify(ctx context.Context, dir string) (Verification, error) {
	return verifyWithRunner(ctx, dir, execRunner{})
}

func verifyWithRunner(ctx context.Context, dir string, runner commandRunner) (Verification, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Verification{}, err
	}
	modulePath, required, err := inspectGoMod(abs)
	if err != nil {
		return Verification{}, err
	}
	goWork, err := inspectWorkspace(ctx, runner, abs)
	if err != nil {
		return Verification{}, err
	}
	out, err := runner.Run(ctx, abs, "go", "list", "-m", "-json", TGFModule)
	if err != nil {
		return Verification{}, fmt.Errorf("resolve tgf module provenance: %w", err)
	}
	listed, err := parseListedModule(out)
	if err != nil {
		return Verification{}, err
	}
	if listed.Version != required {
		return Verification{}, fmt.Errorf("go.mod pins %s but go list resolves %s", required, listed.Version)
	}
	return Verification{Dir: abs, Module: modulePath, TGFVersion: listed.Version, GoWork: goWork}, nil
}

func inspectGoMod(dir string) (string, string, error) {
	path := filepath.Join(dir, "go.mod")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read go.mod: %w", err)
	}
	mf, err := modfile.Parse(path, b, nil)
	if err != nil {
		return "", "", fmt.Errorf("parse go.mod: %w", err)
	}
	if mf.Module == nil || mf.Module.Mod.Path == "" {
		return "", "", fmt.Errorf("go.mod has no module directive")
	}
	for _, replace := range mf.Replace {
		if _, managed := managedModules[replace.Old.Path]; managed {
			return "", "", fmt.Errorf("go.mod must not replace framework module %s", replace.Old.Path)
		}
	}
	for _, require := range mf.Require {
		if require.Mod.Path == TGFModule {
			if require.Indirect {
				return "", "", fmt.Errorf("go.mod must directly require %s", TGFModule)
			}
			if require.Mod.Version == "" {
				return "", "", fmt.Errorf("go.mod has an empty tgf version")
			}
			if !isV2Version(require.Mod.Version) {
				return "", "", fmt.Errorf("go.mod has invalid tgf v2 version %q", require.Mod.Version)
			}
			return mf.Module.Mod.Path, require.Mod.Version, nil
		}
	}
	return "", "", fmt.Errorf("go.mod must directly require %s", TGFModule)
}

func isV2Version(version string) bool {
	return semver.IsValid(version) && semver.Major(version) == "v2"
}

func parseListedModule(data []byte) (listedModule, error) {
	var module listedModule
	if err := json.Unmarshal(data, &module); err != nil {
		return listedModule{}, fmt.Errorf("parse go list module JSON: %w", err)
	}
	if module.Path != TGFModule {
		return listedModule{}, fmt.Errorf("go list resolved unexpected module %q", module.Path)
	}
	if module.Main {
		return listedModule{}, fmt.Errorf("tgf resolved as a workspace main module instead of a dependency")
	}
	if module.Replace != nil {
		return listedModule{}, fmt.Errorf("tgf resolved through replace to %q", module.Replace.Path)
	}
	if module.Version == "" {
		return listedModule{}, fmt.Errorf("tgf resolved without a fixed version")
	}
	return module, nil
}

func inspectWorkspace(ctx context.Context, runner commandRunner, dir string) (string, error) {
	out, err := runner.Run(ctx, dir, "go", "env", "GOWORK")
	if err != nil {
		return "", fmt.Errorf("inspect active Go workspace: %w", err)
	}
	workPath := strings.TrimSpace(string(out))
	if workPath == "" || strings.EqualFold(workPath, "off") {
		return "", nil
	}
	b, err := os.ReadFile(workPath)
	if err != nil {
		return "", fmt.Errorf("read active go.work %s: %w", workPath, err)
	}
	wf, err := modfile.ParseWork(workPath, b, nil)
	if err != nil {
		return "", fmt.Errorf("parse active go.work %s: %w", workPath, err)
	}
	for _, replace := range wf.Replace {
		if _, managed := managedModules[replace.Old.Path]; managed {
			return "", fmt.Errorf("active go.work replaces framework module %s", replace.Old.Path)
		}
	}
	base := filepath.Dir(workPath)
	for _, use := range wf.Use {
		useDir := use.Path
		if !filepath.IsAbs(useDir) {
			useDir = filepath.Join(base, useDir)
		}
		modBytes, readErr := os.ReadFile(filepath.Join(useDir, "go.mod"))
		if readErr != nil {
			return "", fmt.Errorf("read workspace module %s: %w", useDir, readErr)
		}
		mf, parseErr := modfile.Parse(filepath.Join(useDir, "go.mod"), modBytes, nil)
		if parseErr != nil {
			return "", fmt.Errorf("parse workspace module %s: %w", useDir, parseErr)
		}
		if mf.Module != nil {
			if _, managed := managedModules[mf.Module.Mod.Path]; managed {
				return "", fmt.Errorf("active go.work uses local framework module %s at %s", mf.Module.Mod.Path, useDir)
			}
		}
	}
	return workPath, nil
}
