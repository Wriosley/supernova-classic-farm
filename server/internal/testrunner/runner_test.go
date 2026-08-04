package testrunner

import (
	"path/filepath"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/testcatalog"
)

func TestStartRejectsUnknownAndDestructiveWithoutConfirm(t *testing.T) {
	repoRoot, err := testcatalog.ResolveRepoRoot(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := testcatalog.Load(testcatalog.DefaultCatalogPath(repoRoot))
	if err != nil {
		t.Fatal(err)
	}
	platform := NewPlatform(repoRoot, catalog, MySQLConfig{})

	if _, err := platform.Start("does-not-exist", ""); err == nil {
		t.Fatal("expected unknown id to fail")
	}
	if _, err := platform.Start("manual-browser-h5", ""); err == nil {
		t.Fatal("expected non-runnable test to fail")
	}
	if _, err := platform.Start("e2e-dual-zone-mysql", ""); err == nil {
		t.Fatal("expected destructive test without confirm to fail")
	}
	if _, err := platform.Start("e2e-dual-zone-mysql", "wrong"); err == nil {
		t.Fatal("expected wrong confirm token to fail")
	}
}

func TestChildEnvClearsMysqlForMemorySuite(t *testing.T) {
	repoRoot, err := testcatalog.ResolveRepoRoot(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := testcatalog.Load(testcatalog.DefaultCatalogPath(repoRoot))
	if err != nil {
		t.Fatal(err)
	}
	item, ok := catalog.ByID("e2e-dual-zone-memory")
	if !ok {
		t.Fatal("missing memory dual-zone entry")
	}
	platform := NewPlatform(repoRoot, catalog, MySQLConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "classicfarm",
		User:     "classicfarm",
		Password: "secret",
	})
	env, err := platform.childEnv(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range env {
		key, _, _ := cutEnv(entry)
		if key == "MYSQL_DSN" || key == "MYSQL_PASSWORD" {
			t.Fatalf("memory suite leaked %s", key)
		}
	}
}
