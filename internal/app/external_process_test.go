package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbench/internal/foundation/modules"
)

func TestIndependentExampleProcessAttachTwice(t *testing.T) {
	repo := repoRoot(t)
	exampleBin := filepath.Join(t.TempDir(), "example")
	build := exec.Command("go", "build", "-C", filepath.Join(repo, "examples", "external-module"), "-o", exampleBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, out)
	}
	for run := 1; run <= 2; run++ {
		t.Run(runName(run), func(t *testing.T) {
			runIndependentAttach(t, repo, exampleBin, run)
		})
	}
}

func runIndependentAttach(t *testing.T, repo, exampleBin string, run int) {
	t.Helper()
	data := filepath.Join(t.TempDir(), "example.json")
	example := exec.Command(exampleBin, "-listen", "127.0.0.1:0", "-token", "process-token", "-data", data, "-title", "第一版")
	example.Dir = repo
	stdout, err := example.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	example.Stderr = example.Stdout
	if err := example.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = example.Process.Kill(); _ = example.Wait() })
	addr := readListenAddr(t, stdout)
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	created, err := application.registry.Attach(context.Background(), modules.ConnectionInput{BaseURL: "http://" + addr, ServiceToken: "process-token"})
	if err != nil {
		t.Fatal(err)
	}
	createdJSON, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(createdJSON, []byte("process-token")) {
		t.Fatal("token leaked on attach")
	}
	listed := request(t, application.Handler(), http.MethodGet, "/api/modules", nil, "", cookies)
	if listed.Code != 200 || !bytes.Contains(listed.Body.Bytes(), []byte(`"kind":"external"`)) || !bytes.Contains(listed.Body.Bytes(), []byte("/apps/demo_external/overview")) {
		t.Fatalf("list = %s", listed.Body.String())
	}
	enabled := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":true}`), token, cookies)
	if enabled.Code != 200 {
		t.Fatalf("enable = %d %s", enabled.Code, enabled.Body.String())
	}
	page := request(t, application.Handler(), http.MethodGet, "/modules/demo_external/ui/index.html", nil, "", cookies)
	if page.Code != 200 || !bytes.Contains(page.Body.Bytes(), []byte("第一版")) {
		t.Fatalf("ui = %d %s", page.Code, page.Body.String())
	}

	_ = example.Process.Kill()
	_ = example.Wait()
	example = exec.Command(exampleBin, "-listen", addr, "-token", "process-token", "-data", data, "-title", "第二版")
	example.Dir = repo
	if err := example.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = example.Process.Kill(); _ = example.Wait() })
	time.Sleep(150 * time.Millisecond)
	page = request(t, application.Handler(), http.MethodGet, "/modules/demo_external/ui/index.html", nil, "", cookies)
	if page.Code != 200 || !bytes.Contains(page.Body.Bytes(), []byte("第二版")) {
		t.Fatalf("updated ui = %d %s", page.Code, page.Body.String())
	}
	if bytes.Contains(listed.Body.Bytes(), []byte("process-token")) {
		t.Fatal("token appeared in module list")
	}
	_ = run
}

func readListenAddr(t *testing.T, stdout interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "listening on ") {
				done <- strings.TrimPrefix(line, "listening on ")
				return
			}
		}
		done <- ""
	}()
	select {
	case addr := <-done:
		if addr == "" {
			t.Fatal("example did not print listen address")
		}
		return addr
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for example listen address")
	}
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func runName(run int) string {
	if run == 1 {
		return "first"
	}
	return "second"
}

func TestExampleReattachAfterUnregisterCanEnable(t *testing.T) {
	repo := repoRoot(t)
	exampleBin := filepath.Join(t.TempDir(), "example")
	build := exec.Command("go", "build", "-C", filepath.Join(repo, "examples", "external-module"), "-o", exampleBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, out)
	}
	example := exec.Command(exampleBin, "-listen", "127.0.0.1:0", "-token", "process-token", "-data", filepath.Join(t.TempDir(), "example.json"))
	example.Dir = repo
	stdout, err := example.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	example.Stderr = example.Stdout
	if err := example.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = example.Process.Kill(); _ = example.Wait() })
	addr := readListenAddr(t, stdout)
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	if _, err := application.registry.Attach(context.Background(), modules.ConnectionInput{BaseURL: "http://" + addr, ServiceToken: "process-token"}); err != nil {
		t.Fatal(err)
	}
	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":true}`), token, cookies); got.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodDelete, "/api/modules/external/demo_external", nil, token, cookies); got.Code != http.StatusNoContent {
		t.Fatalf("unregister = %d %s", got.Code, got.Body.String())
	}
	if _, err := application.registry.Attach(context.Background(), modules.ConnectionInput{BaseURL: "http://" + addr, ServiceToken: "process-token"}); err != nil {
		t.Fatal(err)
	}
	enabled := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":true}`), token, cookies)
	if enabled.Code != http.StatusOK {
		t.Fatalf("reattach enable = %d %s", enabled.Code, enabled.Body.String())
	}
	page := request(t, application.Handler(), http.MethodGet, "/modules/demo_external/ui/index.html", nil, "", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("reattach ui = %d %s", page.Code, page.Body.String())
	}
}
