package testcatalog

import (
	"path/filepath"
	"testing"
)

func TestCatalogIntegrity(t *testing.T) {
	repoRoot, err := ResolveRepoRoot(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	catalog, err := Load(DefaultCatalogPath(repoRoot))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if err := catalog.Validate(repoRoot); err != nil {
		t.Fatalf("catalog invalid: %v", err)
	}

	item, ok := catalog.ByID("e2e-dual-zone-mysql")
	if !ok {
		t.Fatal("missing e2e-dual-zone-mysql")
	}
	if !item.Destructive || item.Repeatable || !item.Runnable {
		t.Fatalf("active shard migration flags mismatch: destructive=%v repeatable=%v runnable=%v",
			item.Destructive, item.Repeatable, item.Runnable)
	}
	if item.PostRunWarning == "" {
		t.Fatal("active shard migration requires postRunWarning")
	}
}
