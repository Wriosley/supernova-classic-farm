package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/testcatalog"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testrunner"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7199", "loopback listen address")
	uiDir := flag.String("ui", "", "optional directory of built platform UI (tests/platform/web/dist)")
	flag.Parse()

	host, port, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("invalid addr: %v", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		log.Fatalf("testrunner refuses non-loopback bind address %q", *addr)
	}
	_ = port

	startDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getcwd: %v", err)
	}
	repoRoot, err := testcatalog.ResolveRepoRoot(startDir)
	if err != nil {
		log.Fatalf("resolve repo root: %v", err)
	}

	catalog, err := testcatalog.Load(testcatalog.DefaultCatalogPath(repoRoot))
	if err != nil {
		log.Fatalf("load catalog: %v", err)
	}
	if err := catalog.Validate(repoRoot); err != nil {
		log.Fatalf("catalog validation failed: %v", err)
	}

	mysql, err := testrunner.LoadMySQLConfig(repoRoot)
	if err != nil {
		log.Fatalf("load mysql config: %v", err)
	}

	resolvedUI := *uiDir
	if resolvedUI == "" {
		candidate := filepath.Join(repoRoot, "tests", "platform", "web", "dist")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			resolvedUI = candidate
		}
	} else if !filepath.IsAbs(resolvedUI) {
		resolvedUI = filepath.Join(repoRoot, resolvedUI)
	}

	platform := testrunner.NewPlatform(repoRoot, catalog, mysql)
	server := &testrunner.Server{Platform: platform, UIDir: resolvedUI}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("local test runner listening on http://%s\n", *addr)
	fmt.Printf("catalog entries: %d\n", len(catalog.Tests))
	fmt.Printf("mysql configured: %v\n", platform.MySQLConfigured())
	if resolvedUI != "" {
		fmt.Printf("ui dir: %s\n", resolvedUI)
	} else {
		fmt.Printf("ui dir: (none; use tests/platform/web Vite dev proxy)\n")
	}
	fmt.Printf("note: platform history does not replace docs/evidence/\n")
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
