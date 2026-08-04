package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewWithWriterWritesStructuredFields(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewWithWriter(&output, "gate", "test", "info")
	if err != nil {
		t.Fatalf("NewWithWriter() error = %v", err)
	}

	logger.Info("ready", "port", 8081)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	if entry["service"] != "gate" || entry["environment"] != "test" {
		t.Fatalf("missing stable fields: %v", entry)
	}
	if entry["port"] != float64(8081) {
		t.Fatalf("port = %v", entry["port"])
	}
}

func TestNewWithWriterRejectsUnknownLevel(t *testing.T) {
	if _, err := NewWithWriter(&bytes.Buffer{}, "gate", "test", "verbose"); err == nil {
		t.Fatal("expected an error")
	}
}
