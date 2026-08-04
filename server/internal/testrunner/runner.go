package testrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/testcatalog"
)

type Status string

const (
	StatusIdle      Status = "idle"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type LogLine struct {
	Time    time.Time `json:"time"`
	Stream  string    `json:"stream"`
	Message string    `json:"message"`
}

type RunSnapshot struct {
	RunID          string     `json:"runId,omitempty"`
	TestID         string     `json:"testId,omitempty"`
	Name           string     `json:"name,omitempty"`
	Status         Status     `json:"status"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	ElapsedMs      int64      `json:"elapsedMs"`
	ExitCode       *int       `json:"exitCode,omitempty"`
	PostRunWarning string     `json:"postRunWarning,omitempty"`
	Logs           []LogLine  `json:"logs,omitempty"`
	Destructive    bool       `json:"destructive,omitempty"`
	Repeatable     bool       `json:"repeatable,omitempty"`
}

type StreamEvent struct {
	Type string       `json:"type"`
	Run  RunSnapshot  `json:"run"`
	Line *LogLine     `json:"line,omitempty"`
}

type HistoryEntry struct {
	RunID      string    `json:"runId"`
	TestID     string    `json:"testId"`
	Name       string    `json:"name"`
	Status     Status    `json:"status"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	ElapsedMs  int64     `json:"elapsedMs"`
	ExitCode   *int      `json:"exitCode,omitempty"`
}

type Platform struct {
	RepoRoot string
	Catalog  *testcatalog.Catalog
	MySQL    MySQLConfig

	mu       sync.Mutex
	current  *activeRun
	history  []HistoryEntry
	subs     map[chan StreamEvent]struct{}
	maxLogs  int
	histPath string
}

type activeRun struct {
	snapshot RunSnapshot
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	logs     []LogLine
}

func NewPlatform(repoRoot string, catalog *testcatalog.Catalog, mysql MySQLConfig) *Platform {
	return &Platform{
		RepoRoot: repoRoot,
		Catalog:  catalog,
		MySQL:    mysql,
		subs:     make(map[chan StreamEvent]struct{}),
		maxLogs:  4000,
		histPath: filepath.Join(repoRoot, "tests", "platform", ".history", "runs.jsonl"),
	}
}

func (p *Platform) MySQLConfigured() bool {
	return p.MySQL.Configured()
}

func (p *Platform) Status() RunSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil {
		return RunSnapshot{Status: StatusIdle}
	}
	return p.cloneLocked()
}

func (p *Platform) History() []HistoryEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]HistoryEntry, len(p.history))
	copy(out, p.history)
	return out
}

func (p *Platform) Subscribe() (<-chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 32)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	snap := p.cloneLocked()
	p.mu.Unlock()
	ch <- StreamEvent{Type: "snapshot", Run: snap}
	return ch, func() {
		p.mu.Lock()
		delete(p.subs, ch)
		p.mu.Unlock()
		close(ch)
	}
}

func (p *Platform) Start(testID, confirmToken string) (RunSnapshot, error) {
	item, ok := p.Catalog.ByID(testID)
	if !ok {
		return RunSnapshot{}, fmt.Errorf("unknown test id")
	}
	if !item.Runnable {
		return RunSnapshot{}, fmt.Errorf("test is not runnable")
	}
	if item.Destructive && confirmToken != testcatalog.DestructiveConfirmToken {
		return RunSnapshot{}, fmt.Errorf("destructive confirmation required")
	}
	if item.NeedsMysql {
		if !p.MySQL.Configured() {
			return RunSnapshot{}, fmt.Errorf("mysql is not configured in repo-root .env")
		}
		if err := PingMySQL(p.MySQL); err != nil {
			return RunSnapshot{}, fmt.Errorf("mysql ping failed: %w", err)
		}
	}
	for _, port := range item.Ports {
		if !PortFree(port) {
			return RunSnapshot{}, fmt.Errorf("port %d is already in use", port)
		}
	}

	p.mu.Lock()
	if p.current != nil && p.current.snapshot.Status == StatusRunning {
		p.mu.Unlock()
		return RunSnapshot{}, fmt.Errorf("another test is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	runID := fmt.Sprintf("%d", now.UnixNano())
	p.current = &activeRun{
		snapshot: RunSnapshot{
			RunID:          runID,
			TestID:         item.ID,
			Name:           item.Name,
			Status:         StatusRunning,
			StartedAt:      &now,
			PostRunWarning: item.PostRunWarning,
			Destructive:    item.Destructive,
			Repeatable:     item.Repeatable,
		},
		cancel: cancel,
		logs:   nil,
	}
	snap := p.cloneLocked()
	p.mu.Unlock()
	p.broadcast(StreamEvent{Type: "started", Run: snap})

	go p.execute(ctx, item)
	return snap, nil
}

func (p *Platform) Cancel() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current == nil || p.current.snapshot.Status != StatusRunning {
		return fmt.Errorf("no running test")
	}
	p.current.cancel()
	if p.current.cmd != nil && p.current.cmd.Process != nil {
		_ = p.current.cmd.Process.Kill()
	}
	return nil
}

func (p *Platform) execute(ctx context.Context, item *testcatalog.TestItem) {
	cmd, err := p.buildCommand(ctx, item)
	if err != nil {
		p.finish(StatusFailed, nil, err.Error())
		return
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.finish(StatusFailed, nil, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.finish(StatusFailed, nil, err.Error())
		return
	}

	p.mu.Lock()
	if p.current != nil {
		p.current.cmd = cmd
	}
	p.mu.Unlock()

	if err := cmd.Start(); err != nil {
		p.finish(StatusFailed, nil, err.Error())
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.pump(stdout, "stdout")
	}()
	go func() {
		defer wg.Done()
		p.pump(stderr, "stderr")
	}()
	wg.Wait()

	waitErr := cmd.Wait()
	exitCode := 0
	status := StatusSucceeded
	message := ""
	if ctx.Err() != nil {
		status = StatusCancelled
		exitCode = -1
		message = "cancelled"
	} else if waitErr != nil {
		status = StatusFailed
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
			message = waitErr.Error()
		}
	}
	if message != "" {
		p.appendLog("system", message)
	}
	p.finish(status, &exitCode, "")
}

func (p *Platform) buildCommand(ctx context.Context, item *testcatalog.TestItem) (*exec.Cmd, error) {
	workdir := filepath.Join(p.RepoRoot, filepath.FromSlash(item.Command.Workdir))
	var cmd *exec.Cmd
	switch item.Command.Kind {
	case "go-test":
		args := append([]string{"test"}, item.Command.Args...)
		cmd = exec.CommandContext(ctx, "go", args...)
	case "go-vet":
		args := append([]string{"vet"}, item.Command.Args...)
		cmd = exec.CommandContext(ctx, "go", args...)
	case "npm":
		npm := "npm"
		if runtime.GOOS == "windows" {
			npm = "npm.cmd"
		}
		cmd = exec.CommandContext(ctx, npm, item.Command.Args...)
	case "powershell":
		shell, shellArgs := powershellInvocation(filepath.Join(p.RepoRoot, filepath.FromSlash(item.Command.Script)), item.Command.Args)
		cmd = exec.CommandContext(ctx, shell, shellArgs...)
	default:
		return nil, fmt.Errorf("unsupported command kind")
	}
	cmd.Dir = workdir
	env, err := p.childEnv(item)
	if err != nil {
		return nil, err
	}
	cmd.Env = env
	return cmd, nil
}

func powershellInvocation(script string, args []string) (string, []string) {
	shell := "powershell"
	if path, err := exec.LookPath("pwsh"); err == nil {
		shell = path
	} else if path, err := exec.LookPath("powershell"); err == nil {
		shell = path
	}
	out := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}
	out = append(out, args...)
	return shell, out
}

func (p *Platform) childEnv(item *testcatalog.TestItem) ([]string, error) {
	base := os.Environ()
	filtered := make([]string, 0, len(base)+8)
	for _, entry := range base {
		key, _, _ := cutEnv(entry)
		switch key {
		case "MYSQL_DSN", "MYSQL_PASSWORD", "MYSQL_PWD":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	if item.ClearMysqlDsn {
		// intentionally omit MYSQL_DSN
		return filtered, nil
	}
	if item.NeedsMysql {
		dsn, err := p.MySQL.BuildDSN()
		if err != nil {
			return nil, err
		}
		filtered = append(filtered,
			"MYSQL_DSN="+dsn,
			"MYSQL_HOST="+p.MySQL.Host,
			fmt.Sprintf("MYSQL_PORT=%d", p.MySQL.Port),
			"MYSQL_DATABASE="+p.MySQL.Database,
			"MYSQL_USER="+p.MySQL.User,
			"MYSQL_PASSWORD="+p.MySQL.Password,
		)
	}
	return filtered, nil
}

func cutEnv(entry string) (string, string, bool) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return entry, "", false
}

func (p *Platform) pump(r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		p.appendLog(stream, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		p.appendLog("system", RedactSecrets(err.Error(), p.MySQL))
	}
}

func (p *Platform) appendLog(stream, message string) {
	line := LogLine{
		Time:    time.Now(),
		Stream:  stream,
		Message: RedactSecrets(message, p.MySQL),
	}
	p.mu.Lock()
	if p.current == nil {
		p.mu.Unlock()
		return
	}
	p.current.logs = append(p.current.logs, line)
	if len(p.current.logs) > p.maxLogs {
		p.current.logs = p.current.logs[len(p.current.logs)-p.maxLogs:]
	}
	snap := p.cloneLocked()
	p.mu.Unlock()
	p.broadcast(StreamEvent{Type: "log", Run: snap, Line: &line})
}

func (p *Platform) finish(status Status, exitCode *int, systemMessage string) {
	if systemMessage != "" {
		p.appendLog("system", systemMessage)
	}
	p.mu.Lock()
	if p.current == nil {
		p.mu.Unlock()
		return
	}
	now := time.Now()
	p.current.snapshot.Status = status
	p.current.snapshot.FinishedAt = &now
	p.current.snapshot.ExitCode = exitCode
	if p.current.snapshot.StartedAt != nil {
		p.current.snapshot.ElapsedMs = now.Sub(*p.current.snapshot.StartedAt).Milliseconds()
	}
	entry := HistoryEntry{
		RunID:      p.current.snapshot.RunID,
		TestID:     p.current.snapshot.TestID,
		Name:       p.current.snapshot.Name,
		Status:     status,
		StartedAt:  *p.current.snapshot.StartedAt,
		FinishedAt: now,
		ElapsedMs:  p.current.snapshot.ElapsedMs,
		ExitCode:   exitCode,
	}
	p.history = append([]HistoryEntry{entry}, p.history...)
	if len(p.history) > 50 {
		p.history = p.history[:50]
	}
	snap := p.cloneLocked()
	p.mu.Unlock()
	_ = p.appendHistory(entry)
	p.broadcast(StreamEvent{Type: "finished", Run: snap})
}

func (p *Platform) appendHistory(entry HistoryEntry) error {
	if err := os.MkdirAll(filepath.Dir(p.histPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(p.histPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	return enc.Encode(entry)
}

func (p *Platform) cloneLocked() RunSnapshot {
	if p.current == nil {
		return RunSnapshot{Status: StatusIdle}
	}
	snap := p.current.snapshot
	if snap.StartedAt != nil && snap.FinishedAt == nil {
		snap.ElapsedMs = time.Since(*snap.StartedAt).Milliseconds()
	}
	snap.Logs = append([]LogLine(nil), p.current.logs...)
	return snap
}

func (p *Platform) broadcast(event StreamEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- event:
		default:
		}
	}
}
