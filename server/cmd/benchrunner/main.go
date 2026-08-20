// benchrunner is a local-only protocol load generator for reproducible
// single-instance baselines. It intentionally exercises the public HTTP and
// Protobuf WebSocket paths instead of calling Zone code in-process.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	httpv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/http"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	protobufMediaType = "application/x-protobuf"
	defaultLoginURL   = "http://127.0.0.1:8080"
	defaultOrigin     = "http://localhost:5173"
	benchmarkPassword = "benchmark-password-2026"
)

// Production cluster defaults (overridable via flags).
// CLB IP 21.214.142.172 maps to Login:8080 and Gate:8081.
const (
	clusterLoginURL = "http://21.214.142.172:8080"
	clusterGateURL  = "ws://21.214.142.172:8081/ws"
	clusterOrigin   = "http://21.130.223.195:1616"
)

type options struct {
	scenario        string
	authMode        string // login | gate-skip
	loginURL        string
	loginURLs       []string
	gateURL         string
	gateURLs        []string
	origin          string
	mode            string // closed | open
	concurrencies   []int
	targetQPSs      []float64
	connectWorkers  int
	warmup          time.Duration
	duration        time.Duration
	timeout         time.Duration
	pingInterval    time.Duration
	runID           string
	outputDirectory string
	maxSamples      int
	accountFile     string
	identityOutput  string
	playerIDs       map[string]uint64
}

type sample struct {
	Concurrency int
	LatencyUS   int64
}

type runSummary struct {
	RunID       string            `json:"run_id"`
	Scenario    string            `json:"scenario"`
	GeneratedAt string            `json:"generated_at"`
	Parameters  map[string]string `json:"parameters"`
	Results     []result          `json:"results"`
}

type result struct {
	Concurrency        int                     `json:"concurrency"`
	AttemptCount       int64                   `json:"attempt_count,omitempty"`
	TargetQPS          float64                 `json:"target_qps,omitempty"`
	OfferedCount       int64                   `json:"offered_count,omitempty"`
	ShedCount          int64                   `json:"shed_count,omitempty"`
	DurationMS         int64                   `json:"duration_ms"`
	SuccessCount       int64                   `json:"success_count"`
	RejectedCount      int64                   `json:"rejected_count,omitempty"`
	ErrorCount         int64                   `json:"error_count"`
	ErrorKinds         map[string]int64        `json:"error_kinds"`
	DroppedSampleCount int64                   `json:"dropped_sample_count"`
	QPS                float64                 `json:"qps"`
	P50US              int64                   `json:"p50_us"`
	P95US              int64                   `json:"p95_us"`
	P99US              int64                   `json:"p99_us"`
	MaxUS              int64                   `json:"max_us"`
	Actions            map[string]actionResult `json:"actions,omitempty"`
}

type actionResult struct {
	SuccessCount int64 `json:"success_count"`
	ErrorCount   int64 `json:"error_count"`
	P50US        int64 `json:"p50_us"`
	P95US        int64 `json:"p95_us"`
	P99US        int64 `json:"p99_us"`
	MaxUS        int64 `json:"max_us"`
}

type environment struct {
	RunID       string            `json:"run_id"`
	GeneratedAt string            `json:"generated_at"`
	Hostname    string            `json:"hostname"`
	GoVersion   string            `json:"go_version"`
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	CPUs        int               `json:"cpus"`
	Parameters  map[string]string `json:"parameters"`
}

type benchClient struct {
	accountName string
	playerID    uint64
	conn        *websocket.Conn
	timeout     time.Duration
}

type httpStatusError struct {
	method    string
	endpoint  string
	requestID string
	status    int
	code      string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s %s: unexpected HTTP status=%d error=%s request_id=%s",
		e.method, e.endpoint, e.status, e.code, e.requestID)
}

func main() {
	opts := parseOptions()
	if opts.gateURL != "" {
		fmt.Printf("override gate url: %s\n", opts.gateURL)
	}
	if !validScenario(opts.scenario) {
		exitf("unsupported scenario %q; valid: snapshot, player_loop, connect_hold, friend_interaction, friend_steal, mail_operations, mixed", opts.scenario)
	}
	if err := os.MkdirAll(opts.outputDirectory, 0o755); err != nil {
		exitf("create output directory: %v", err)
	}
	hostname, _ := os.Hostname()
	if err := writeJSON(filepath.Join(opts.outputDirectory, "environment.json"), environment{
		RunID: opts.runID, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Hostname: hostname, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUs:       runtime.NumCPU(),
		Parameters: parameterMap(opts),
	}); err != nil {
		exitf("write environment: %v", err)
	}

	summary := runSummary{
		RunID: opts.runID, Scenario: opts.scenario,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339), Parameters: parameterMap(opts),
	}
	allSamples := make([]sample, 0)
	if opts.mode == "open" {
		pool := opts.concurrencies[0]
		for _, targetQPS := range opts.targetQPSs {
			fmt.Printf("run=%s scenario=%s mode=open pool=%d target_qps=%.0f warmup=%s duration=%s\n",
				opts.runID, opts.scenario, pool, targetQPS, opts.warmup, opts.duration)
			current, samples, err := runSnapshotOpen(opts, pool, targetQPS)
			if err != nil {
				exitf("target_qps %.0f: %v", targetQPS, err)
			}
			summary.Results = append(summary.Results, current)
			allSamples = append(allSamples, samples...)
			if err := writeArtifacts(opts.outputDirectory, summary, allSamples); err != nil {
				exitf("write partial results: %v", err)
			}
			fmt.Printf("pool=%d target_qps=%.0f achieved_qps=%.2f shed=%d p50=%dus p99=%dus errors=%d\n",
				current.Concurrency, current.TargetQPS, current.QPS, current.ShedCount,
				current.P50US, current.P99US, current.ErrorCount)
		}
	} else {
		for _, concurrency := range opts.concurrencies {
			fmt.Printf("run=%s scenario=%s mode=closed concurrency=%d warmup=%s duration=%s\n",
				opts.runID, opts.scenario, concurrency, opts.warmup, opts.duration)
			current, samples, err := runScenario(opts, concurrency)
			if err != nil {
				exitf("concurrency %d: %v", concurrency, err)
			}
			summary.Results = append(summary.Results, current)
			allSamples = append(allSamples, samples...)
			if err := writeArtifacts(opts.outputDirectory, summary, allSamples); err != nil {
				exitf("write partial results: %v", err)
			}
			fmt.Printf("concurrency=%d qps=%.2f p50=%dus p95=%dus p99=%dus errors=%d\n",
				current.Concurrency, current.QPS, current.P50US, current.P95US, current.P99US, current.ErrorCount)
		}
	}
	fmt.Printf("results written to %s\n", opts.outputDirectory)
}

func parseOptions() options {
	scenario := flag.String("scenario", "snapshot", "benchmark scenario")
	authMode := flag.String("auth-mode", "login", "authentication mode: login or gate-skip (load test only)")
	loginURL := flag.String("login-url", defaultLoginURL, "LoginSvr base URL, or comma-separated sticky per-user URLs")
	gateURL := flag.String("gate-url", "", "override Gate WebSocket URL, or comma-separated sticky per-user URLs (empty = use bootstrap response)")
	origin := flag.String("origin", defaultOrigin, "H5 Origin header")
	mode := flag.String("mode", "closed", "load mode: closed (wait for response) or open (pace by -target-qps)")
	concurrency := flag.String("concurrency", "1,10,25,50,100", "comma-separated virtual users / open-mode connection pool size")
	targetQPS := flag.String("target-qps", "", "open mode only: comma-separated offered QPS steps (e.g. 2000,4000,6000)")
	connectWorkers := flag.Int("connect-workers", 64, "parallel login/WebSocket handshakes during setup")
	warmup := flag.Duration("warmup", 10*time.Second, "warmup duration")
	duration := flag.Duration("duration", 60*time.Second, "measurement duration")
	timeout := flag.Duration("timeout", 5*time.Second, "per HTTP/WebSocket operation timeout")
	pingInterval := flag.Duration("ping-interval", 30*time.Second, "PING interval for connect_hold scenario")
	runID := flag.String("run-id", time.Now().UTC().Format("20060102_150405"), "unique local run identifier")
	output := flag.String("output", "", "output directory (default benchmark/results/<run-id>)")
	maxSamples := flag.Int("max-samples", 1_000_000, "maximum retained latency samples per run")
	accountFile := flag.String("account-file", "", "optional newline/CSV account-name file; first column is used")
	identityOutput := flag.String("identity-output", "", "optional safe CSV output: account_name,player_id,shard_id")
	cluster := flag.Bool("cluster", false, "use production cluster defaults (CLB 21.214.142.172)")
	flag.Parse()
	if *cluster {
		if *loginURL == defaultLoginURL {
			*loginURL = clusterLoginURL
		}
		if *gateURL == "" {
			*gateURL = clusterGateURL
		}
		if *origin == defaultOrigin {
			*origin = clusterOrigin
		}
	}
	values, err := parseConcurrencies(*concurrency)
	if err != nil {
		exitf("invalid -concurrency: %v", err)
	}
	rates, err := parseTargetQPSs(*targetQPS)
	if err != nil {
		exitf("invalid -target-qps: %v", err)
	}
	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	switch normalizedMode {
	case "closed", "open":
	default:
		exitf("unsupported -mode %q; valid: closed, open", *mode)
	}
	if normalizedMode == "open" {
		if *scenario != "snapshot" {
			exitf("open mode currently supports only -scenario snapshot")
		}
		if len(values) != 1 {
			exitf("open mode requires a single -concurrency value (connection pool size)")
		}
		if len(rates) == 0 {
			exitf("open mode requires -target-qps (e.g. -target-qps 2000,4000,6000)")
		}
	}
	normalizedAuthMode := strings.ToLower(strings.TrimSpace(*authMode))
	if normalizedAuthMode != "login" && normalizedAuthMode != "gate-skip" {
		exitf("unsupported -auth-mode %q; valid: login, gate-skip", *authMode)
	}
	var playerIDs map[string]uint64
	if normalizedAuthMode == "gate-skip" {
		if *accountFile == "" {
			exitf("-auth-mode gate-skip requires -account-file with account_name,player_id")
		}
		if strings.TrimSpace(*gateURL) == "" {
			exitf("-auth-mode gate-skip requires an explicit -gate-url")
		}
		var identityErr error
		playerIDs, identityErr = readPlayerIDs(*accountFile)
		if identityErr != nil {
			exitf("read gate-skip identities: %v", identityErr)
		}
	}
	if *warmup < 0 || *duration <= 0 || *timeout <= 0 || *maxSamples <= 0 {
		exitf("warmup must be >= 0; duration, timeout and max-samples must be positive")
	}
	if *connectWorkers <= 0 || *connectWorkers > 1024 {
		exitf("connect-workers must be in 1..1024")
	}
	if !validRunID(*runID) {
		exitf("run-id must be 1..18 lowercase letters, digits or underscores")
	}
	resolvedOutput := *output
	if resolvedOutput == "" {
		root, err := repositoryRoot()
		if err != nil {
			exitf("resolve repository root: %v", err)
		}
		resolvedOutput = filepath.Join(root, "benchmark", "results", *runID)
	}
	loginURLs, err := parseLoginURLs(*loginURL)
	if err != nil {
		exitf("invalid -login-url: %v", err)
	}
	gateURLs, err := parseGateURLs(*gateURL)
	if err != nil {
		exitf("invalid -gate-url: %v", err)
	}
	primaryGateURL := ""
	if len(gateURLs) > 0 {
		primaryGateURL = gateURLs[0]
	}
	return options{
		scenario: *scenario, authMode: normalizedAuthMode, loginURL: loginURLs[0], loginURLs: loginURLs,
		gateURL: primaryGateURL, gateURLs: gateURLs, origin: *origin,
		mode: normalizedMode, concurrencies: values, targetQPSs: rates, connectWorkers: *connectWorkers,
		warmup: *warmup, duration: *duration, timeout: *timeout, pingInterval: *pingInterval,
		runID: *runID, outputDirectory: resolvedOutput, maxSamples: *maxSamples,
		accountFile: *accountFile, identityOutput: *identityOutput, playerIDs: playerIDs,
	}
}

func readPlayerIDs(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(rows) < 2 || len(rows[0]) < 2 || strings.TrimSpace(rows[0][0]) != "account_name" || strings.TrimSpace(rows[0][1]) != "player_id" {
		return nil, errors.New("CSV must start with account_name,player_id")
	}
	identities := make(map[string]uint64, len(rows)-1)
	for index, row := range rows[1:] {
		if len(row) < 2 {
			return nil, fmt.Errorf("line %d must contain account_name,player_id", index+2)
		}
		name := strings.TrimSpace(row[0])
		playerID, parseErr := strconv.ParseUint(strings.TrimSpace(row[1]), 10, 64)
		if !ValidateBenchmarkAccountName(name) || parseErr != nil || playerID == 0 {
			return nil, fmt.Errorf("line %d has invalid account_name or player_id", index+2)
		}
		if _, exists := identities[name]; exists {
			return nil, fmt.Errorf("duplicate account %q", name)
		}
		identities[name] = playerID
	}
	return identities, nil
}

func benchAccountNames(runID string, count int) []string {
	names := make([]string, count)
	for index := range names {
		names[index] = fmt.Sprintf("bench_%s_%03d", runID, index+1)
	}
	return names
}

func accountNamesForRun(opts options, count int) ([]string, error) {
	if opts.accountFile == "" {
		return benchAccountNames(opts.runID, count), nil
	}
	body, err := os.ReadFile(opts.accountFile)
	if err != nil {
		return nil, fmt.Errorf("read account file: %w", err)
	}
	var names []string
	seen := make(map[string]struct{})
	for lineNumber, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(line, ",", 2)[0])
		if lineNumber == 0 && name == "account_name" {
			continue
		}
		if !ValidateBenchmarkAccountName(name) {
			return nil, fmt.Errorf("account file line %d has invalid account name %q", lineNumber+1, name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("account file contains duplicate account %q", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) < count {
		return nil, fmt.Errorf("account file has %d accounts, need %d", len(names), count)
	}
	return names[:count], nil
}

func ValidateBenchmarkAccountName(name string) bool {
	if len(name) < 3 || len(name) > 32 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for index := 1; index < len(name); index++ {
		value := name[index]
		if !((value >= 'a' && value <= 'z') || (value >= '0' && value <= '9') || value == '_') {
			return false
		}
	}
	return true
}

// authenticateAll performs the login and WebSocket handshakes for every account
// in parallel, bounded by -connect-workers. Setup is otherwise the dominant cost
// at high connection counts.
func authenticateAll(opts options, names []string) ([]*benchClient, error) {
	clients := make([]*benchClient, len(names))
	limit := opts.connectWorkers
	if limit <= 0 {
		limit = 1
	}
	if limit > len(names) {
		limit = len(names)
	}
	semaphore := make(chan struct{}, limit)
	var group sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var connected int
	start := time.Now()
	progressStep := len(names) / 10
	if progressStep < 100 {
		progressStep = 100
	}
	for index, name := range names {
		group.Add(1)
		go func(index int, name string) {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			mu.Lock()
			aborted := firstErr != nil
			mu.Unlock()
			if aborted {
				return
			}
			client, err := authenticate(opts, name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("authenticate %s: %w", name, err)
				}
				return
			}
			clients[index] = client
			connected++
			if connected%progressStep == 0 || connected == len(names) {
				fmt.Printf("connected %d/%d in %s\n",
					connected, len(names), time.Since(start).Truncate(time.Millisecond))
			}
		}(index, name)
	}
	group.Wait()
	if firstErr != nil {
		closeClients(clients)
		return nil, firstErr
	}
	if opts.identityOutput != "" {
		if err := writeIdentityCSV(opts.identityOutput, clients); err != nil {
			closeClients(clients)
			return nil, err
		}
	}
	return clients, nil
}

func writeIdentityCSV(path string, clients []*benchClient) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create identity output directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create identity output: %w", err)
	}
	writer := csv.NewWriter(file)
	writeErr := writer.Write([]string{"account_name", "player_id", "shard_id"})
	for _, client := range clients {
		if writeErr != nil || client == nil {
			continue
		}
		writeErr = writer.Write([]string{
			client.accountName,
			strconv.FormatUint(client.playerID, 10),
			strconv.FormatUint(uint64(routing.ShardForPlayer(client.playerID)), 10),
		})
	}
	writer.Flush()
	if writeErr == nil {
		writeErr = writer.Error()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write identity output: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close identity output: %w", closeErr)
	}
	return nil
}

func runSnapshot(opts options, concurrency int) (result, []sample, error) {
	names, err := accountNamesForRun(opts, concurrency)
	if err != nil {
		return result{}, nil, err
	}
	clients, err := authenticateAll(opts, names)
	if err != nil {
		return result{}, nil, err
	}
	defer closeClients(clients)

	warmupUntil := time.Now().Add(opts.warmup)
	for time.Now().Before(warmupUntil) {
		for _, client := range clients {
			_, _ = client.snapshot()
		}
	}
	start := time.Now()
	deadline := start.Add(opts.duration)
	var mu sync.Mutex
	latencies := make([]int64, 0)
	samples := make([]sample, 0)
	var successes, failures, dropped int64
	errorKinds := make(map[string]int64)
	var workers sync.WaitGroup
	for _, client := range clients {
		workers.Add(1)
		go func(client *benchClient) {
			defer workers.Done()
			for time.Now().Before(deadline) {
				latency, err := client.snapshot()
				mu.Lock()
				if err != nil {
					failures++
					errorKinds[classifyError(err)]++
					mu.Unlock()
					return
				}
				successes++
				if len(latencies) < opts.maxSamples {
					us := latency.Microseconds()
					latencies = append(latencies, us)
					samples = append(samples, sample{Concurrency: concurrency, LatencyUS: us})
				} else {
					dropped++
				}
				mu.Unlock()
			}
		}(client)
	}
	workers.Wait()
	elapsed := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return result{
		Concurrency: concurrency, DurationMS: elapsed.Milliseconds(), SuccessCount: successes,
		ErrorCount: failures, ErrorKinds: errorKinds, DroppedSampleCount: dropped,
		QPS:   float64(successes) / elapsed.Seconds(),
		P50US: percentile(latencies, 50), P95US: percentile(latencies, 95),
		P99US: percentile(latencies, 99), MaxUS: percentile(latencies, 100),
	}, samples, nil
}

// runSnapshotOpen paces GET_PLAYER_SNAPSHOT at targetQPS across a fixed connection
// pool. The pacer never waits for responses; if the pool is saturated beyond
// poolSize*4 outstanding starts, arrivals are shed (open-loop overload signal).
func runSnapshotOpen(opts options, poolSize int, targetQPS float64) (result, []sample, error) {
	if targetQPS <= 0 {
		return result{}, nil, fmt.Errorf("target qps must be positive")
	}
	accountNames, err := accountNamesForRun(opts, poolSize)
	if err != nil {
		return result{}, nil, err
	}
	clients, err := authenticateAll(opts, accountNames)
	if err != nil {
		return result{}, nil, err
	}
	defer closeClients(clients)

	warmupUntil := time.Now().Add(opts.warmup)
	for time.Now().Before(warmupUntil) {
		for _, client := range clients {
			_, _ = client.snapshot()
		}
	}

	clientPool := make(chan *benchClient, poolSize)
	for _, client := range clients {
		clientPool <- client
	}
	maxOutstanding := poolSize * 4
	if maxOutstanding < 32 {
		maxOutstanding = 32
	}
	outstanding := make(chan struct{}, maxOutstanding)

	start := time.Now()
	deadline := start.Add(opts.duration)
	var mu sync.Mutex
	latencies := make([]int64, 0)
	samples := make([]sample, 0)
	var successes, failures, dropped, offered, shed int64
	errorKinds := make(map[string]int64)
	var workers sync.WaitGroup

	interval := time.Duration(float64(time.Second) / targetQPS)
	if interval <= 0 {
		interval = time.Microsecond
	}
	next := time.Now()
	for time.Now().Before(deadline) {
		now := time.Now()
		if delay := next.Sub(now); delay > 0 {
			time.Sleep(delay)
		}
		next = next.Add(interval)
		offered++

		select {
		case outstanding <- struct{}{}:
		default:
			shed++
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-outstanding }()
			client := <-clientPool
			latency, err := client.snapshot()
			clientPool <- client
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures++
				errorKinds[classifyError(err)]++
				return
			}
			successes++
			if len(latencies) < opts.maxSamples {
				us := latency.Microseconds()
				latencies = append(latencies, us)
				samples = append(samples, sample{Concurrency: poolSize, LatencyUS: us})
			} else {
				dropped++
			}
		}()
	}
	workers.Wait()
	elapsed := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return result{
		Concurrency: poolSize, TargetQPS: targetQPS, OfferedCount: offered, ShedCount: shed,
		DurationMS: elapsed.Milliseconds(), SuccessCount: successes,
		ErrorCount: failures, ErrorKinds: errorKinds, DroppedSampleCount: dropped,
		QPS:   float64(successes) / elapsed.Seconds(),
		P50US: percentile(latencies, 50), P95US: percentile(latencies, 95),
		P99US: percentile(latencies, 99), MaxUS: percentile(latencies, 100),
	}, samples, nil
}

func authenticate(opts options, accountName string) (*benchClient, error) {
	opts.loginURL = selectLoginURL(opts.loginURLs, accountName)
	if len(opts.gateURLs) > 0 {
		opts.gateURL = selectLoginURL(opts.gateURLs, accountName)
	}
	if opts.authMode == "gate-skip" {
		playerID := opts.playerIDs[accountName]
		if playerID == 0 {
			return nil, fmt.Errorf("account %q has no player_id in -account-file", accountName)
		}
		ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, opts.gateURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{opts.origin}},
		})
		if err != nil {
			return nil, err
		}
		client := &benchClient{accountName: accountName, playerID: playerID, conn: conn, timeout: opts.timeout}
		if err := client.auth(""); err != nil {
			_ = conn.CloseNow()
			return nil, err
		}
		return client, nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Jar: jar, Timeout: opts.timeout}
	csrf, err := getCSRF(httpClient, opts)
	if err != nil {
		return nil, err
	}
	register := &httpv1.RegisterResponse{}
	if err := doProto(httpClient, opts, http.MethodPost, opts.loginURL+"/v1/auth/register",
		&httpv1.RegisterRequest{AccountName: accountName, Password: benchmarkPassword}, csrf, http.StatusCreated, register); err != nil {
		var statusError *httpStatusError
		if !errors.As(err, &statusError) || statusError.status != http.StatusConflict {
			return nil, fmt.Errorf("register: %w", err)
		}
		csrf, err = getCSRF(httpClient, opts)
		if err != nil {
			return nil, err
		}
		login := &httpv1.LoginResponse{}
		if err := doProto(httpClient, opts, http.MethodPost, opts.loginURL+"/v1/auth/login",
			&httpv1.LoginRequest{AccountName: accountName, Password: benchmarkPassword}, csrf, http.StatusOK, login); err != nil {
			return nil, fmt.Errorf("login: %w", err)
		}
		register.Session = login.GetSession()
	}
	playerID := register.GetSession().GetPlayerId()
	if playerID == 0 {
		return nil, errors.New("registration returned no player ID")
	}
	csrf, err = getCSRF(httpClient, opts)
	if err != nil {
		return nil, err
	}
	bootstrap := &httpv1.ClientBootstrapResponse{}
	if err := doProto(httpClient, opts, http.MethodGet, opts.loginURL+"/v1/bootstrap", nil, "", http.StatusOK, bootstrap); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if len(bootstrap.GetGateways()) != 1 {
		return nil, fmt.Errorf("bootstrap gateways=%d, want 1", len(bootstrap.GetGateways()))
	}
	gateway := bootstrap.GetGateways()[0]
	ticket := &httpv1.WsTicketResponse{}
	if err := doProto(httpClient, opts, http.MethodPost, opts.loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{TicketRequestId: newUUID(), GatewayId: gateway.GetGatewayId()},
		csrf, http.StatusCreated, ticket); err != nil {
		return nil, fmt.Errorf("issue ticket: %w", err)
	}
	wsURL := gateway.GetWebsocketUrl()
	if opts.gateURL != "" {
		wsURL = opts.gateURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{opts.origin}},
	})
	if err != nil {
		return nil, err
	}
	client := &benchClient{accountName: accountName, playerID: playerID, conn: conn, timeout: opts.timeout}
	if err := client.auth(ticket.GetWsTicket()); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	return client, nil
}

func parseLoginURLs(raw string) ([]string, error) {
	var urls []string
	seen := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimRight(strings.TrimSpace(item), "/")
		if item == "" || (!strings.HasPrefix(item, "http://") && !strings.HasPrefix(item, "https://")) {
			return nil, fmt.Errorf("%q must be an absolute HTTP(S) URL", item)
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			urls = append(urls, item)
		}
	}
	if len(urls) == 0 {
		return nil, errors.New("at least one LoginSvr URL is required")
	}
	return urls, nil
}

func parseGateURLs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var urls []string
	seen := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || (!strings.HasPrefix(item, "ws://") && !strings.HasPrefix(item, "wss://")) {
			return nil, fmt.Errorf("%q must be an absolute WebSocket URL", item)
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			urls = append(urls, item)
		}
	}
	return urls, nil
}

func selectLoginURL(urls []string, accountName string) string {
	if len(urls) <= 1 {
		return urls[0]
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(accountName))
	return urls[int(hash.Sum32()%uint32(len(urls)))]
}

func (c *benchClient) auth(ticket string) error {
	requestID := newUUID()
	targetPlayerID := uint64(0)
	if ticket == "" {
		targetPlayerID = c.playerID
	}
	if err := c.write(&wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST, Action: wsv1.Action_AUTH,
		RequestId: requestID, TargetPlayerId: targetPlayerID,
		Payload: &wsv1.WsEnvelope_AuthRequest{AuthRequest: &wsv1.AuthRequest{WsTicket: ticket}},
	}); err != nil {
		return err
	}
	response, err := c.read()
	if err != nil {
		return err
	}
	if response.GetError() != nil || response.GetAction() != wsv1.Action_AUTH ||
		response.GetRequestId() != requestID || response.GetAuthResponse().GetPlayerId() != c.playerID {
		return fmt.Errorf("invalid AUTH response: %+v", response)
	}
	return nil
}

func (c *benchClient) snapshot() (time.Duration, error) {
	requestID := newUUID()
	start := time.Now()
	if err := c.write(&wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST, Action: wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId: requestID, TargetPlayerId: c.playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{}},
	}); err != nil {
		return 0, err
	}
	for {
		response, err := c.read()
		if err != nil {
			return 0, err
		}
		if response.GetError() != nil {
			return 0, fmt.Errorf("snapshot_error_code_%d", response.GetError().GetCode())
		}
		// Skip PUSH messages.
		if response.GetMessageKind() == wsv1.MessageKind_PUSH {
			continue
		}
		if response.GetAction() != wsv1.Action_GET_PLAYER_SNAPSHOT ||
			response.GetRequestId() != requestID || response.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlayerId() != c.playerID {
			return 0, fmt.Errorf("invalid snapshot response: %+v", response)
		}
		return time.Since(start), nil
	}
}

func (c *benchClient) write(envelope *wsv1.WsEnvelope) error {
	body, err := proto.Marshal(envelope)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageBinary, body)
}

func (c *benchClient) read() (*wsv1.WsEnvelope, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	messageType, body, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if messageType != websocket.MessageBinary {
		return nil, fmt.Errorf("unexpected WebSocket message type %d", messageType)
	}
	envelope := &wsv1.WsEnvelope{}
	if err := proto.Unmarshal(body, envelope); err != nil {
		return nil, err
	}
	return envelope, nil
}

func getCSRF(client *http.Client, opts options) (string, error) {
	response := &httpv1.CsrfResponse{}
	if err := doProto(client, opts, http.MethodGet, opts.loginURL+"/v1/auth/csrf", nil, "", http.StatusOK, response); err != nil {
		return "", fmt.Errorf("get csrf: %w", err)
	}
	if response.GetCsrfToken() == "" {
		return "", errors.New("empty CSRF token")
	}
	return response.GetCsrfToken(), nil
}

func doProto(client *http.Client, opts options, method, endpoint string, request proto.Message, csrf string, wantStatus int, response proto.Message) error {
	var body io.Reader
	if request != nil {
		encoded, err := proto.Marshal(request)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	httpRequest, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Accept", protobufMediaType)
	httpRequest.Header.Set("Origin", opts.origin)
	if request != nil {
		httpRequest.Header.Set("Content-Type", protobufMediaType)
	}
	if csrf != "" {
		httpRequest.Header.Set("X-CSRF-Token", csrf)
		httpRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(httpResponse.Body, 2<<20))
	if err != nil {
		return err
	}
	if httpResponse.StatusCode != wantStatus {
		failure := &httpv1.HttpError{}
		_ = proto.Unmarshal(bodyBytes, failure)
		return &httpStatusError{
			method: method, endpoint: httpRequest.URL.Path,
			requestID: httpResponse.Header.Get("X-Request-ID"),
			status:    httpResponse.StatusCode,
			code:      strconv.FormatInt(int64(failure.GetCode()), 10),
		}
	}
	return proto.Unmarshal(bodyBytes, response)
}

func parseConcurrencies(raw string) ([]int, error) {
	seen := map[int]struct{}{}
	var values []int
	for _, item := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || value <= 0 || value > 20000 {
			return nil, fmt.Errorf("%q must be an integer in 1..20000", item)
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, errors.New("at least one concurrency is required")
	}
	sort.Ints(values)
	return values, nil
}

func parseTargetQPSs(raw string) ([]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	seen := map[float64]struct{}{}
	var values []float64
	for _, item := range strings.Split(raw, ",") {
		value, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		if err != nil || value <= 0 || value > 1_000_000 {
			return nil, fmt.Errorf("%q must be a number in (0, 1000000]", item)
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	sort.Float64s(values)
	return values, nil
}

func classifyError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "timeout"):
		return "timeout"
	case strings.Contains(message, "WebSocket"):
		return "websocket"
	case strings.Contains(message, "snapshot_error_code_"):
		return message
	case strings.Contains(message, "invalid snapshot response"):
		return "invalid_snapshot_response"
	default:
		return message
	}
}

func validRunID(value string) bool {
	if len(value) == 0 || len(value) > 18 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}

func percentile(sorted []int64, percent int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*percent + 99) / 100
	if index == 0 {
		index = 1
	}
	return sorted[index-1]
}

func parameterMap(opts options) map[string]string {
	params := map[string]string{
		"mode": opts.mode, "auth_mode": opts.authMode, "login_url": strings.Join(opts.loginURLs, ","), "gate_url": strings.Join(opts.gateURLs, ","), "origin": opts.origin,
		"concurrency": joinInts(opts.concurrencies),
		"warmup":      opts.warmup.String(), "duration": opts.duration.String(), "timeout": opts.timeout.String(),
		"ping_interval": opts.pingInterval.String(), "max_samples": strconv.Itoa(opts.maxSamples),
	}
	if len(opts.targetQPSs) > 0 {
		params["target_qps"] = joinFloats(opts.targetQPSs)
	}
	if opts.accountFile != "" {
		params["account_file"] = opts.accountFile
	}
	if opts.identityOutput != "" {
		params["identity_output"] = opts.identityOutput
	}
	return params
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

func joinFloats(values []float64) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.FormatFloat(value, 'f', -1, 64)
	}
	return strings.Join(parts, ",")
}

func newUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "benchmark")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("benchmark directory not found above working directory")
		}
		current = parent
	}
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func writeArtifacts(outputDirectory string, summary runSummary, samples []sample) error {
	if err := writeJSON(filepath.Join(outputDirectory, "summary.json"), summary); err != nil {
		return err
	}
	if err := writeSamples(filepath.Join(outputDirectory, "latency.csv"), samples); err != nil {
		return err
	}
	return writeReport(filepath.Join(outputDirectory, "report.md"), summary)
}

func writeSamples(path string, samples []sample) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"concurrency", "latency_us"}); err != nil {
		return err
	}
	for _, item := range samples {
		if err := writer.Write([]string{strconv.Itoa(item.Concurrency), strconv.FormatInt(item.LatencyUS, 10)}); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func writeReport(path string, summary runSummary) error {
	var body strings.Builder
	fmt.Fprintf(&body, "# R3 snapshot benchmark: %s\n\n", summary.RunID)
	body.WriteString("This is a local single-instance baseline, not a production capacity claim.\n\n")
	openMode := false
	for _, item := range summary.Results {
		if item.TargetQPS > 0 {
			openMode = true
			break
		}
	}
	if openMode {
		body.WriteString("| pool | target QPS | achieved QPS | shed | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |\n")
		body.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, item := range summary.Results {
			fmt.Fprintf(&body, "| %d | %.0f | %.2f | %d | %.3f | %.3f | %.3f | %.3f | %d |\n",
				item.Concurrency, item.TargetQPS, item.QPS, item.ShedCount,
				float64(item.P50US)/1000, float64(item.P95US)/1000,
				float64(item.P99US)/1000, float64(item.MaxUS)/1000, item.ErrorCount)
		}
	} else {
		body.WriteString("| concurrency | QPS | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |\n")
		body.WriteString("|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, item := range summary.Results {
			fmt.Fprintf(&body, "| %d | %.2f | %.3f | %.3f | %.3f | %.3f | %d |\n",
				item.Concurrency, item.QPS, float64(item.P50US)/1000, float64(item.P95US)/1000,
				float64(item.P99US)/1000, float64(item.MaxUS)/1000, item.ErrorCount)
		}
	}
	body.WriteString("\n## Error categories\n\n")
	for _, item := range summary.Results {
		if len(item.ErrorKinds) == 0 {
			continue
		}
		if item.TargetQPS > 0 {
			fmt.Fprintf(&body, "- pool %d target_qps %.0f: ", item.Concurrency, item.TargetQPS)
		} else {
			fmt.Fprintf(&body, "- concurrency %d: ", item.Concurrency)
		}
		keys := make([]string, 0, len(item.ErrorKinds))
		for key := range item.ErrorKinds {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			if index > 0 {
				body.WriteString(", ")
			}
			fmt.Fprintf(&body, "`%s`=%d", key, item.ErrorKinds[key])
		}
		body.WriteByte('\n')
	}
	body.WriteString("\n## Parameters\n\n")
	keys := make([]string, 0, len(summary.Parameters))
	for key := range summary.Parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&body, "- `%s`: `%s`\n", key, summary.Parameters[key])
	}
	return os.WriteFile(path, []byte(body.String()), 0o644)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "benchrunner: "+format+"\n", args...)
	os.Exit(2)
}
