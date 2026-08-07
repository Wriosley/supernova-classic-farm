package testcatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DestructiveConfirmToken = "I_UNDERSTAND_DESTRUCTIVE"

type Catalog struct {
	Version int        `json:"version"`
	Tests   []TestItem `json:"tests"`
}

type TestItem struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	Purpose              string   `json:"purpose"`
	Tiers                []string `json:"tiers"`
	Preconditions        []string `json:"preconditions"`
	EstimatedDurationSec int      `json:"estimatedDurationSec"`
	Impact               string   `json:"impact"`
	Files                []string `json:"files"`
	Command              Command  `json:"command"`
	Ports                []int    `json:"ports"`
	NeedsMysql           bool     `json:"needsMysql"`
	ClearMysqlDsn        bool     `json:"clearMysqlDsn"`
	Destructive          bool     `json:"destructive"`
	Repeatable           bool     `json:"repeatable"`
	Runnable             bool     `json:"runnable"`
	PostRunWarning       string   `json:"postRunWarning"`
}

type Command struct {
	Kind    string   `json:"kind"`
	Workdir string   `json:"workdir"`
	Script  string   `json:"script,omitempty"`
	Args    []string `json:"args"`
}

func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return &catalog, nil
}

func (c *Catalog) ByID(id string) (*TestItem, bool) {
	for i := range c.Tests {
		if c.Tests[i].ID == id {
			return &c.Tests[i], true
		}
	}
	return nil, false
}

func (c *Catalog) Validate(repoRoot string) error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if len(c.Tests) == 0 {
		return fmt.Errorf("catalog has no tests")
	}

	seen := make(map[string]struct{}, len(c.Tests))
	coveredScripts := make(map[string]struct{})

	for i := range c.Tests {
		item := &c.Tests[i]
		if err := validateItem(item); err != nil {
			return fmt.Errorf("test[%d] %q: %w", i, item.ID, err)
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("duplicate test id %q", item.ID)
		}
		seen[item.ID] = struct{}{}

		for _, rel := range item.Files {
			abs := filepath.Join(repoRoot, filepath.FromSlash(rel))
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("test %q missing file %q: %w", item.ID, rel, err)
			}
			if strings.HasSuffix(strings.ToLower(rel), ".ps1") {
				coveredScripts[filepath.ToSlash(rel)] = struct{}{}
			}
		}
		if item.Command.Script != "" {
			script := filepath.ToSlash(item.Command.Script)
			abs := filepath.Join(repoRoot, filepath.FromSlash(script))
			if _, err := os.Stat(abs); err != nil {
				return fmt.Errorf("test %q missing script %q: %w", item.ID, script, err)
			}
			coveredScripts[script] = struct{}{}
		}
	}

	e2eDir := filepath.Join(repoRoot, "tests", "e2e")
	entries, err := os.ReadDir(e2eDir)
	if err != nil {
		return fmt.Errorf("read tests/e2e: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".ps1") {
			continue
		}
		if strings.HasPrefix(name, "_") {
			// Shared helpers must still appear in files[] of at least one entry.
		}
		rel := filepath.ToSlash(filepath.Join("tests", "e2e", name))
		if _, ok := coveredScripts[rel]; !ok {
			return fmt.Errorf("e2e script %q is not referenced by catalog files or command.script", rel)
		}
	}
	return nil
}

func validateItem(item *TestItem) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(item.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(item.Type) == "" {
		return fmt.Errorf("type is required")
	}
	if strings.TrimSpace(item.Purpose) == "" {
		return fmt.Errorf("purpose is required")
	}
	if len(item.Tiers) == 0 {
		return fmt.Errorf("tiers are required")
	}
	for _, tier := range item.Tiers {
		switch tier {
		case "safe", "service", "mysql", "destructive":
		default:
			return fmt.Errorf("unknown tier %q", tier)
		}
	}
	if item.EstimatedDurationSec <= 0 {
		return fmt.Errorf("estimatedDurationSec must be positive")
	}
	if strings.TrimSpace(item.Impact) == "" {
		return fmt.Errorf("impact is required")
	}
	if len(item.Files) == 0 {
		return fmt.Errorf("files are required")
	}
	if err := validateCommand(item); err != nil {
		return err
	}
	if item.Destructive {
		if !hasTier(item.Tiers, "destructive") {
			return fmt.Errorf("destructive=true requires destructive tier")
		}
		if !hasTier(item.Tiers, "mysql") || !hasTier(item.Tiers, "service") {
			return fmt.Errorf("destructive tests must include mysql and service tiers")
		}
		if item.Repeatable {
			return fmt.Errorf("destructive tests must set repeatable=false")
		}
		if strings.TrimSpace(item.PostRunWarning) == "" {
			return fmt.Errorf("destructive tests require postRunWarning")
		}
	}
	if item.NeedsMysql && !hasTier(item.Tiers, "mysql") && item.Runnable {
		return fmt.Errorf("needsMysql requires mysql tier for runnable tests")
	}
	if !item.Runnable && item.Type != "manual" {
		return fmt.Errorf("non-runnable tests must be type manual in v1")
	}
	return nil
}

func validateCommand(item *TestItem) error {
	cmd := item.Command
	if strings.TrimSpace(cmd.Kind) == "" {
		return fmt.Errorf("command.kind is required")
	}
	if strings.TrimSpace(cmd.Workdir) == "" {
		return fmt.Errorf("command.workdir is required")
	}
	switch cmd.Kind {
	case "go-test", "go-vet":
		if len(cmd.Args) == 0 {
			return fmt.Errorf("%s requires args", cmd.Kind)
		}
	case "npm":
		if len(cmd.Args) < 2 || cmd.Args[0] != "run" {
			return fmt.Errorf("npm command must be [run, <script>]")
		}
	case "powershell":
		if strings.TrimSpace(cmd.Script) == "" {
			return fmt.Errorf("powershell command requires script")
		}
		if filepath.Ext(strings.ToLower(cmd.Script)) != ".ps1" {
			return fmt.Errorf("powershell script must end with .ps1")
		}
	case "bash":
		if strings.TrimSpace(cmd.Script) == "" {
			return fmt.Errorf("bash command requires script")
		}
		if filepath.Ext(strings.ToLower(cmd.Script)) != ".sh" {
			return fmt.Errorf("bash script must end with .sh")
		}
	case "manual":
		if item.Runnable {
			return fmt.Errorf("manual command cannot be runnable")
		}
	default:
		return fmt.Errorf("unsupported command.kind %q", cmd.Kind)
	}
	return nil
}

func hasTier(tiers []string, want string) bool {
	for _, tier := range tiers {
		if tier == want {
			return true
		}
	}
	return false
}

func ResolveRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		catalogPath := filepath.Join(dir, "tests", "catalog.json")
		if _, err := os.Stat(catalogPath); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root with tests/catalog.json not found from %s", start)
		}
		dir = parent
	}
}

func DefaultCatalogPath(repoRoot string) string {
	return filepath.Join(repoRoot, "tests", "catalog.json")
}
