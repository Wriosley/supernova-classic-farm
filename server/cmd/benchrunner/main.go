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
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	protobufMediaType = "application/x-protobuf"
	defaultLoginURL   = "http://127.0.0.1:8080"
	defaultOrigin     = "http://localhost:5173"
	benchmarkPassword = "benchmark-password-2026"
)

type options struct {
	scenario        string
	loginURL        string
	origin          string
	concurrencies   []int
	warmup          time.Duration
	duration        time.Duration
	timeout         time.Duration
	runID           string
	outputDirectory string
	maxSamples      int
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
	Concurrency        int              `json:"concurrency"`
	DurationMS         int64            `json:"duration_ms"`
	SuccessCount       int64            `json:"success_count"`
	ErrorCount         int64            `json:"error_count"`
	ErrorKinds         map[string]int64 `json:"error_kinds"`
	DroppedSampleCount int64            `json:"dropped_sample_count"`
	QPS                float64          `json:"qps"`
	P50US              int64            `json:"p50_us"`
	P95US              int64            `json:"p95_us"`
	P99US              int64            `json:"p99_us"`
	MaxUS              int64            `json:"max_us"`
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
	playerID uint64
	conn     *websocket.Conn
	timeout  time.Duration
}

type httpStatusError struct {
	status int
	code   string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status=%d error=%s", e.status, e.code)
}

func main() {
	opts := parseOptions()
	if opts.scenario != "snapshot" {
		exitf("unsupported scenario %q; the first baseline supports snapshot", opts.scenario)
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
	for _, concurrency := range opts.concurrencies {
		fmt.Printf("run=%s scenario=%s concurrency=%d warmup=%s duration=%s\n",
			opts.runID, opts.scenario, concurrency, opts.warmup, opts.duration)
		current, samples, err := runSnapshot(opts, concurrency)
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
	fmt.Printf("results written to %s\n", opts.outputDirectory)
}

func parseOptions() options {
	scenario := flag.String("scenario", "snapshot", "benchmark scenario")
	loginURL := flag.String("login-url", defaultLoginURL, "LoginSvr base URL")
	origin := flag.String("origin", defaultOrigin, "H5 Origin header")
	concurrency := flag.String("concurrency", "1,10,25,50,100", "comma-separated virtual users")
	warmup := flag.Duration("warmup", 10*time.Second, "warmup duration")
	duration := flag.Duration("duration", 60*time.Second, "measurement duration")
	timeout := flag.Duration("timeout", 5*time.Second, "per HTTP/WebSocket operation timeout")
	runID := flag.String("run-id", time.Now().UTC().Format("20060102_150405"), "unique local run identifier")
	output := flag.String("output", "", "output directory (default benchmark/results/<run-id>)")
	maxSamples := flag.Int("max-samples", 1_000_000, "maximum retained latency samples per run")
	flag.Parse()
	values, err := parseConcurrencies(*concurrency)
	if err != nil {
		exitf("invalid -concurrency: %v", err)
	}
	if *warmup < 0 || *duration <= 0 || *timeout <= 0 || *maxSamples <= 0 {
		exitf("warmup must be >= 0; duration, timeout and max-samples must be positive")
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
	return options{
		scenario: *scenario, loginURL: strings.TrimRight(*loginURL, "/"), origin: *origin,
		concurrencies: values, warmup: *warmup, duration: *duration, timeout: *timeout,
		runID: *runID, outputDirectory: resolvedOutput, maxSamples: *maxSamples,
	}
}

func runSnapshot(opts options, concurrency int) (result, []sample, error) {
	clients := make([]*benchClient, concurrency)
	for index := range clients {
		accountName := fmt.Sprintf("bench_%s_%03d", opts.runID, index+1)
		client, err := authenticate(opts, accountName)
		if err != nil {
			for _, connected := range clients {
				if connected != nil {
					_ = connected.conn.CloseNow()
				}
			}
			return result{}, nil, fmt.Errorf("authenticate %s: %w", accountName, err)
		}
		clients[index] = client
	}
	defer func() {
		for _, client := range clients {
			_ = client.conn.CloseNow()
		}
	}()

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

func authenticate(opts options, accountName string) (*benchClient, error) {
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
			return nil, err
		}
		csrf, err = getCSRF(httpClient, opts)
		if err != nil {
			return nil, err
		}
		login := &httpv1.LoginResponse{}
		if err := doProto(httpClient, opts, http.MethodPost, opts.loginURL+"/v1/auth/login",
			&httpv1.LoginRequest{AccountName: accountName, Password: benchmarkPassword}, csrf, http.StatusOK, login); err != nil {
			return nil, err
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
		return nil, err
	}
	if len(bootstrap.GetGateways()) != 1 {
		return nil, fmt.Errorf("bootstrap gateways=%d, want 1", len(bootstrap.GetGateways()))
	}
	gateway := bootstrap.GetGateways()[0]
	ticket := &httpv1.WsTicketResponse{}
	if err := doProto(httpClient, opts, http.MethodPost, opts.loginURL+"/v1/ws-tickets",
		&httpv1.WsTicketRequest{TicketRequestId: newUUID(), GatewayId: gateway.GetGatewayId()},
		csrf, http.StatusCreated, ticket); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, gateway.GetWebsocketUrl(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{opts.origin}},
	})
	if err != nil {
		return nil, err
	}
	client := &benchClient{playerID: playerID, conn: conn, timeout: opts.timeout}
	if err := client.auth(ticket.GetWsTicket()); err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	return client, nil
}

func (c *benchClient) auth(ticket string) error {
	requestID := newUUID()
	if err := c.write(&wsv1.WsEnvelope{
		ProtocolVersion: 1, MessageKind: wsv1.MessageKind_REQUEST, Action: wsv1.Action_AUTH,
		RequestId: requestID, Payload: &wsv1.WsEnvelope_AuthRequest{AuthRequest: &wsv1.AuthRequest{WsTicket: ticket}},
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
	response, err := c.read()
	if err != nil {
		return 0, err
	}
	if response.GetError() != nil {
		return 0, fmt.Errorf("snapshot_error_code_%d", response.GetError().GetCode())
	}
	if response.GetAction() != wsv1.Action_GET_PLAYER_SNAPSHOT ||
		response.GetRequestId() != requestID || response.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlayerId() != c.playerID {
		return 0, fmt.Errorf("invalid snapshot response: %+v", response)
	}
	return time.Since(start), nil
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
		return "", err
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
			status: httpResponse.StatusCode,
			code:   strconv.FormatInt(int64(failure.GetCode()), 10),
		}
	}
	return proto.Unmarshal(bodyBytes, response)
}

func parseConcurrencies(raw string) ([]int, error) {
	seen := map[int]struct{}{}
	var values []int
	for _, item := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || value <= 0 || value > 128 {
			return nil, fmt.Errorf("%q must be an integer in 1..128", item)
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
	return map[string]string{
		"login_url": opts.loginURL, "origin": opts.origin, "concurrency": joinInts(opts.concurrencies),
		"warmup": opts.warmup.String(), "duration": opts.duration.String(), "timeout": opts.timeout.String(),
		"max_samples": strconv.Itoa(opts.maxSamples),
	}
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
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
	body.WriteString("| concurrency | QPS | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |\n")
	body.WriteString("|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, item := range summary.Results {
		fmt.Fprintf(&body, "| %d | %.2f | %.3f | %.3f | %.3f | %.3f | %d |\n",
			item.Concurrency, item.QPS, float64(item.P50US)/1000, float64(item.P95US)/1000,
			float64(item.P99US)/1000, float64(item.MaxUS)/1000, item.ErrorCount)
	}
	body.WriteString("\n## Error categories\n\n")
	for _, item := range summary.Results {
		if len(item.ErrorKinds) == 0 {
			continue
		}
		fmt.Fprintf(&body, "- concurrency %d: ", item.Concurrency)
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
	for key, value := range summary.Parameters {
		fmt.Fprintf(&body, "- `%s`: `%s`\n", key, value)
	}
	return os.WriteFile(path, []byte(body.String()), 0o644)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "benchrunner: "+format+"\n", args...)
	os.Exit(2)
}
