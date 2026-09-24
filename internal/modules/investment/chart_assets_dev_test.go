//go:build dev

package investment

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDevChartAssetsReadLiveDiskContent(t *testing.T) {
	dir := t.TempDir()
	writeDevChartFile(t, dir, "index.html", "<html>dev</html>")
	writeDevChartFile(t, dir, "app.js", "export const version = 1;\n")
	assets := openDevChartAssets(t, dir)
	module := &Module{terminalAssets: assets}
	const path = "/investment/terminal/app.js"

	first := requestDevChartAsset(module, path)
	if first.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), []byte("export const version = 1;\n")) {
		t.Fatalf("first response: status %d body %q", first.Code, first.Body.Bytes())
	}
	firstETag := first.Header().Get("ETag")
	if firstETag == "" || first.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("script cache headers: %v", first.Header())
	}

	writeDevChartFile(t, dir, "app.js", "export const version = 2;\n")
	second := requestDevChartAsset(module, path)
	if second.Code != http.StatusOK || !bytes.Equal(second.Body.Bytes(), []byte("export const version = 2;\n")) {
		t.Fatalf("updated response: status %d body %q", second.Code, second.Body.Bytes())
	}
	if second.Header().Get("ETag") == firstETag {
		t.Fatal("updated disk content must receive a new ETag")
	}
}

func TestDevChartRejectsInvalidDirectories(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-chart")
	filePath := filepath.Join(t.TempDir(), "chart-file")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	noIndex := t.TempDir()
	cases := []struct {
		name string
		dir  string
	}{
		{name: "unset"},
		{name: "relative", dir: "chart"},
		{name: "missing", dir: missing},
		{name: "file", dir: filePath},
		{name: "missing index", dir: noIndex},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(devChartDirEnv, test.dir)
			if _, err := terminalChartAssets(); err == nil {
				t.Fatal("expected an invalid chart directory to fail")
			}
		})
	}
}

func TestDevChartRejectsTraversalAndSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	writeDevChartFile(t, dir, "index.html", "<html>dev</html>")
	outside := filepath.Join(t.TempDir(), "secret.js")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.js")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	module := &Module{terminalAssets: openDevChartAssets(t, dir)}
	for _, path := range []string{
		"/investment/terminal/%2e%2e/secret.js",
		"/investment/terminal/escape.js",
	} {
		got := requestDevChartAsset(module, path)
		if got.Code != http.StatusNotFound || bytes.Contains(got.Body.Bytes(), []byte("secret")) {
			t.Fatalf("unsafe path %q: status %d body %q", path, got.Code, got.Body.Bytes())
		}
	}
}

func openDevChartAssets(t *testing.T, dir string) fs.FS {
	t.Helper()
	t.Setenv(devChartDirEnv, dir)
	assets, err := terminalChartAssets()
	if err != nil {
		t.Fatalf("open dev chart assets: %v", err)
	}
	if closer, ok := assets.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	return assets
}

func writeDevChartFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requestDevChartAsset(module *Module, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	module.serveTerminal(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}
