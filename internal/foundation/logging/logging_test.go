package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
)

func TestThresholdAppliesToExistingChildLoggers(t *testing.T) {
	var output bytes.Buffer
	logger, level, err := New(slog.New(slog.NewJSONHandler(&output, nil)).With("service", "test"), "info")
	if err != nil {
		t.Fatal(err)
	}
	child := logger.With("component", "scheduler").WithGroup("job").With("id", "maintenance")
	for _, minimum := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		level.Set(minimum)
		for _, emitted := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			output.Reset()
			child.Log(context.Background(), emitted, "test")
			if emitted < minimum {
				if output.Len() != 0 {
					t.Fatalf("%s leaked at threshold %s", emitted, minimum)
				}
				continue
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["level"] != emitted.String() || record["service"] != "test" || record["component"] != "scheduler" || record["job"].(map[string]any)["id"] != "maintenance" {
				t.Fatalf("lost attributes: %s", output.String())
			}
		}
	}
}

func TestConcurrentLevelChanges(t *testing.T) {
	logger, level, err := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), "info")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			child := logger.With("worker", i)
			for range 100 {
				level.Set(slog.LevelDebug)
				child.Debug("debug")
				level.Set(slog.LevelError)
				child.Error("error")
			}
		})
	}
	workers.Wait()
}

func TestParseLevelRejectsUnsupportedValues(t *testing.T) {
	for _, value := range []string{"", "trace", "off", "INFO+1", "warning"} {
		if _, err := ParseLevel(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if level, err := ParseLevel(" WARN "); err != nil || level != slog.LevelWarn {
		t.Fatalf("normalized level = %s, %v", level, err)
	}
}
