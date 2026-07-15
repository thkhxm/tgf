package scaffold

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates/base templates/profiles templates/data templates/deploy/base templates/deploy/compose templates/deploy/k8s
var templateFS embed.FS

type Result struct {
	Dir            string `json:"dir"`
	Module         string `json:"module"`
	Profile        string `json:"profile"`
	TGFVersion     string `json:"tgfVersion"`
	TemplateDigest string `json:"templateDigest"`
	ChecksRun      bool   `json:"checksRun"`
}

type manifest struct {
	Schema         int    `json:"schema"`
	Generator      string `json:"generator"`
	TemplateDigest string `json:"templateDigest"`
	Module         string `json:"module"`
	Profile        string `json:"profile"`
	Data           string `json:"data"`
	Protocol       string `json:"protocol"`
	Deploy         string `json:"deploy"`
	Security       string `json:"security"`
	TGFVersion     string `json:"tgfVersion"`
}

type templateData struct {
	Module      string
	Profile     string
	Data        string
	Protocol    string
	Deploy      string
	Security    string
	TGFVersion  string
	CacheExpr   string
	MainPackage string
	LoginSecret string
	AdminToken  string
	KCPKeyHex   string
	UseRedis    bool
	UseMySQL    bool
	UseGateway  bool
	UseTCP      bool
	UseWS       bool
	UseKCP      bool
	Distributed bool
	HTTP        bool
}

type generator struct {
	runner commandRunner
	random io.Reader
}

func Generate(ctx context.Context, cfg Config) (Result, error) {
	return generator{runner: execRunner{}, random: rand.Reader}.generate(ctx, cfg)
}

func (g generator) generate(ctx context.Context, cfg Config) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	if g.runner == nil {
		g.runner = execRunner{}
	}
	if g.random == nil {
		g.random = rand.Reader
	}

	dest, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("resolve destination: %w", err)
	}
	existedEmpty, err := preflightDestination(dest, cfg.Force)
	if err != nil {
		return Result{}, err
	}
	parent := filepath.Dir(dest)
	if mkdirErr := os.MkdirAll(parent, 0o755); mkdirErr != nil {
		return Result{}, fmt.Errorf("create destination parent: %w", mkdirErr)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dest)+".tmp-")
	if err != nil {
		return Result{}, fmt.Errorf("create staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()

	resolved, err := g.resolveVersion(ctx, stage, cfg.TGFVersion)
	if err != nil {
		return Result{}, err
	}
	data, err := g.makeTemplateData(cfg, resolved)
	if err != nil {
		return Result{}, err
	}
	digest, err := renderProject(stage, cfg, data)
	if err != nil {
		return Result{}, err
	}
	if err := formatRenderedGo(stage); err != nil {
		return Result{}, err
	}
	if err := writeManifest(stage, cfg, resolved, digest); err != nil {
		return Result{}, err
	}

	if !cfg.SkipChecks {
		if _, err := inspectWorkspace(ctx, g.runner, stage); err != nil {
			return Result{}, err
		}
		for _, check := range [][]string{{"mod", "tidy"}, {"build", "./..."}, {"vet", "./..."}, {"test", "./..."}} {
			if _, err := g.runner.Run(ctx, stage, "go", check...); err != nil {
				return Result{}, fmt.Errorf("generated project check failed: %w", err)
			}
		}
		if _, err := verifyWithRunner(ctx, stage, g.runner); err != nil {
			return Result{}, fmt.Errorf("generated project provenance check failed: %w", err)
		}
	}
	if cfg.GitInit {
		if _, err := g.runner.Run(ctx, stage, "git", "init"); err != nil {
			return Result{}, fmt.Errorf("git init failed: %w", err)
		}
	}

	if existedEmpty {
		if err := os.Remove(dest); err != nil {
			return Result{}, fmt.Errorf("remove empty destination before commit: %w", err)
		}
	}
	if err := os.Rename(stage, dest); err != nil {
		if existedEmpty {
			_ = os.Mkdir(dest, 0o755)
		}
		return Result{}, fmt.Errorf("commit generated project: %w", err)
	}
	committed = true
	return Result{Dir: dest, Module: cfg.Module, Profile: cfg.Profile, TGFVersion: resolved, TemplateDigest: digest, ChecksRun: !cfg.SkipChecks}, nil
}

func preflightDestination(dest string, force bool) (bool, error) {
	info, err := os.Stat(dest)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect destination: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("destination exists and is not a directory: %s", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return false, fmt.Errorf("inspect destination contents: %w", err)
	}
	if !force {
		return false, fmt.Errorf("destination already exists; --force only permits an existing empty directory: %s", dest)
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("destination is not empty; refusing to merge or overwrite: %s", dest)
	}
	return true, nil
}

func (g generator) resolveVersion(ctx context.Context, dir, query string) (string, error) {
	out, err := g.runner.Run(ctx, dir, "go", "mod", "download", "-json", TGFModule+"@"+query)
	if err != nil {
		return "", fmt.Errorf("resolve tgf version %q: %w", query, err)
	}
	var response struct {
		Path    string
		Version string
		Error   string
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return "", fmt.Errorf("parse resolved tgf version: %w", err)
	}
	if response.Error != "" {
		return "", fmt.Errorf("resolve tgf version %q: %s", query, response.Error)
	}
	if response.Path != TGFModule || !isV2Version(response.Version) {
		return "", fmt.Errorf("resolver returned invalid tgf module/version: path=%q version=%q", response.Path, response.Version)
	}
	return response.Version, nil
}

func (g generator) makeTemplateData(cfg Config, version string) (templateData, error) {
	data := templateData{
		Module: cfg.Module, Profile: cfg.Profile, Data: cfg.Data, Protocol: cfg.Protocol,
		Deploy: cfg.Deploy, Security: cfg.Security, TGFVersion: version,
		CacheExpr: "tgf.CacheModuleClose", MainPackage: ".",
		UseRedis:    cfg.Data == "redis" || cfg.Data == "redis-mysql" || cfg.Profile == "distributed",
		UseMySQL:    cfg.Data == "redis-mysql",
		UseGateway:  cfg.Profile == "single" || cfg.Profile == "distributed",
		UseTCP:      cfg.Profile == "single" || cfg.Profile == "distributed",
		UseWS:       cfg.Protocol == "websocket" || cfg.Protocol == "all",
		UseKCP:      cfg.Protocol == "kcp" || cfg.Protocol == "all",
		Distributed: cfg.Profile == "distributed",
		HTTP:        cfg.Profile == "http-rest" || cfg.Profile == "http-rpc",
	}
	if data.UseRedis {
		data.CacheExpr = "tgf.CacheModuleRedis"
	}
	if data.Distributed {
		data.MainPackage = "./cmd/gateway"
	}
	if cfg.Security == "dev-generated" {
		var err error
		if data.UseGateway {
			data.LoginSecret, err = randomHex(g.random, 32)
			if err != nil {
				return templateData{}, fmt.Errorf("generate login secret: %w", err)
			}
		}
		if data.HTTP {
			data.AdminToken, err = randomHex(g.random, 32)
			if err != nil {
				return templateData{}, fmt.Errorf("generate admin token: %w", err)
			}
		}
		if data.UseKCP {
			data.KCPKeyHex, err = randomHex(g.random, 32)
			if err != nil {
				return templateData{}, fmt.Errorf("generate KCP AEAD key: %w", err)
			}
		}
	}
	return data, nil
}

func randomHex(reader io.Reader, size int) (string, error) {
	b := make([]byte, size)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func renderProject(stage string, cfg Config, data templateData) (string, error) {
	roots := []string{"templates/base", "templates/profiles/" + cfg.Profile}
	if cfg.Data == "redis" || cfg.Data == "redis-mysql" {
		roots = append(roots, "templates/data/redis")
	}
	if cfg.Data == "redis-mysql" {
		roots = append(roots, "templates/data/redis-mysql")
	}
	if cfg.Deploy != "local" {
		roots = append(roots, "templates/deploy/base", "templates/deploy/"+cfg.Deploy)
	}
	type source struct {
		path, output string
		content      []byte
	}
	var sources []source
	for _, root := range roots {
		err := fs.WalkDir(templateFS, root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			content, err := templateFS.ReadFile(path)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(path, root+"/")
			if root == "templates/base" || root == "templates/deploy/base" {
				rel = filepath.Base(rel)
			}
			rel = strings.TrimSuffix(rel, ".tmpl")
			switch rel {
			case "gitignore":
				rel = ".gitignore"
			case "env.example":
				rel = ".env.example"
			case "env.dev":
				rel = ".env.dev"
			}
			sources = append(sources, source{path: path, output: filepath.FromSlash(rel), content: content})
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("walk embedded templates %s: %w", root, err)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	h := sha256.New()
	for _, src := range sources {
		h.Write([]byte(src.path))
		h.Write([]byte{0})
		h.Write(src.content)
		h.Write([]byte{0})
		parsed, err := template.New(src.path).Option("missingkey=error").Parse(string(src.content))
		if err != nil {
			return "", fmt.Errorf("parse embedded template %s: %w", src.path, err)
		}
		var rendered strings.Builder
		if err := parsed.Execute(&rendered, data); err != nil {
			return "", fmt.Errorf("render embedded template %s: %w", src.path, err)
		}
		body := strings.ReplaceAll(rendered.String(), "\r\n", "\n")
		if containsTemplateMarker(body) {
			return "", fmt.Errorf("unresolved template marker in %s", src.path)
		}
		target := filepath.Join(stage, src.output)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		mode := fs.FileMode(0o644)
		if src.output == ".env.dev" {
			mode = 0o600
		}
		if err := os.WriteFile(target, []byte(body), mode); err != nil {
			return "", fmt.Errorf("write %s: %w", src.output, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func containsTemplateMarker(body string) bool {
	for _, marker := range []string{"{{.", "{{if", "{{else", "{{end", "{{range", "{{with"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func formatRenderedGo(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read generated Go file %s: %w", path, err)
		}
		formatted, err := format.Source(body)
		if err != nil {
			return fmt.Errorf("format generated Go file %s: %w", path, err)
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return fmt.Errorf("write formatted Go file %s: %w", path, err)
		}
		return nil
	})
}

func writeManifest(stage string, cfg Config, version, digest string) error {
	m := manifest{Schema: 1, Generator: GeneratorVersion, TemplateDigest: digest, Module: cfg.Module, Profile: cfg.Profile, Data: cfg.Data, Protocol: cfg.Protocol, Deploy: cfg.Deploy, Security: cfg.Security, TGFVersion: version}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(stage, ".tgf-scaffold.json"), b, 0o644); err != nil {
		return fmt.Errorf("write scaffold manifest: %w", err)
	}
	return nil
}
