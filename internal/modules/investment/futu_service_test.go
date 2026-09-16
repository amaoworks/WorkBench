package investment

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitOpenDState(t *testing.T, s *openDService, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := s.status(); state == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, message := s.status()
	t.Fatalf("service state %q, want %q: %s", state, want, message)
}

func testOpenDService(t *testing.T) (*openDService, futuRecord) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("native OpenD requires Linux amd64")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "fixture-opend")
	// exec ensures the supervisor owns the only child, with no shell grandchild.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s := newOpenDService(filepath.Join(dir, "runtime"), binary)
	t.Cleanup(s.close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return s, futuRecord{Enabled: true, Account: "fixture<&>account", PasswordMD5: strings.Repeat("a", 32), Host: "127.0.0.1", Port: port}
}

func TestOpenDServiceLifecycleAndPrivateConfig(t *testing.T) {
	s, rec := testOpenDService(t)
	s.apply(rec)
	waitOpenDState(t, s, "running")
	files, _ := filepath.Glob(filepath.Join(s.dir, ".login-*.xml"))
	if len(files) != 1 {
		t.Fatal("expected one private login configuration")
	}
	info, _ := os.Stat(files[0])
	raw, _ := os.ReadFile(files[0])
	if info.Mode().Perm() != 0600 || !strings.Contains(string(raw), "fixture&lt;&amp;&gt;account") {
		t.Fatal("unsafe login configuration")
	}
	original := s.done
	s.apply(rec)
	if original != s.done {
		t.Fatal("identical settings restarted the service")
	}
	rec.PasswordMD5 = strings.Repeat("b", 32)
	s.apply(rec)
	waitOpenDState(t, s, "running")
	select {
	case <-original:
	default:
		t.Fatal("old process not reaped before restart")
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatal("old credential file survived restart")
	}
	rec.Enabled = false
	s.apply(rec)
	waitOpenDState(t, s, "stopped")
	files, _ = filepath.Glob(filepath.Join(s.dir, ".login-*.xml"))
	if len(files) != 0 {
		t.Fatal("credential file survived stop")
	}
	s.close()
	rec.Enabled = true
	s.apply(rec)
	waitOpenDState(t, s, "stopped")
}

func TestOpenDServiceCancelInstallationAndRetry(t *testing.T) {
	s, rec := testOpenDService(t)
	binary := s.binary
	s.binary = ""
	started := make(chan struct{})
	s.install = func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	s.apply(rec)
	<-started
	rec.Enabled = false
	s.apply(rec)
	waitOpenDState(t, s, "stopped")
	s.install = func(context.Context, string) (string, error) { return "", errors.New("download fixture failed") }
	rec.Enabled = true
	s.apply(rec)
	waitOpenDState(t, s, "error")
	s.install = func(context.Context, string) (string, error) { return binary, nil }
	s.apply(rec)
	waitOpenDState(t, s, "running")
}

func TestOpenDServiceRefusesOccupiedPortAndReportsExit(t *testing.T) {
	s, rec := testOpenDService(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rec.Port = listener.Addr().(*net.TCPAddr).Port
	s.apply(rec)
	waitOpenDState(t, s, "error")
	_, message := s.status()
	if !strings.Contains(message, "端口已被占用") {
		t.Fatal(message)
	}
	listener.Close()
	if err := os.WriteFile(s.binary, []byte("#!/bin/sh\nprintf 'fixture secret\\n' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.apply(rec)
	waitOpenDState(t, s, "error")
	_, message = s.status()
	if !strings.Contains(message, "退出码 7") || strings.Contains(message, "secret") {
		t.Fatal(message)
	}
}

func TestOpenDArchiveRejectsEscapesAndLinks(t *testing.T) {
	for _, entry := range []struct {
		name string
		kind byte
		ok   bool
	}{
		{"release/FutuOpenD", tar.TypeReg, true}, {"../escape", tar.TypeReg, false}, {"/tmp/escape", tar.TypeReg, false}, {"release/link", tar.TypeSymlink, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			var data bytes.Buffer
			gz := gzip.NewWriter(&data)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Mode: 0755}); err != nil {
				t.Fatal(err)
			}
			tw.Close()
			gz.Close()
			dir := t.TempDir()
			err := extractOpenD(&data, dir)
			if (err == nil) != entry.ok {
				t.Fatalf("archive accepted=%v, want %v", err == nil, entry.ok)
			}
			if entry.ok {
				if _, err := findOpenDBinary(dir); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOpenDModuleDisableWorksWithCancelledRequest(t *testing.T) {
	s, rec := testOpenDService(t)
	m := openInvestmentModule(t)
	m.deps.FutuOpenDAddress = net.JoinHostPort(rec.Host, strconv.Itoa(rec.Port))
	m.opend = s
	if err := m.storeFutu(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	s.apply(rec)
	waitOpenDState(t, s, "running")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.OnEnabledChanged(ctx, false); err != nil {
		t.Fatal(err)
	}
	waitOpenDState(t, s, "stopped")
	if _, err := m.futu.ensure(context.Background()); err == nil {
		t.Fatal("disabled module connected to OpenD")
	}
	if err := m.OnEnabledChanged(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	waitOpenDState(t, s, "running")
}

func TestOpenDGatewayDoesNotConnectWhenManagedServiceFailed(t *testing.T) {
	m := openInvestmentModule(t)
	m.opend = newOpenDService(t.TempDir(), "unused")
	m.opend.setStatus("error", "fixture startup failure")
	if _, err := m.futu.ensureOverlay(context.Background()); !errors.Is(err, errOverlayOff) {
		t.Fatal("disabled night error contract changed", err)
	}
	rec, err := m.loadFutu(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rec.Enabled, rec.OvernightEnabled = true, true
	if err := m.storeFutu(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	for _, overlay := range []bool{false, true} {
		if _, err := m.futu.ensureConnection(context.Background(), overlay); err == nil || !strings.Contains(err.Error(), "服务尚未启动") {
			t.Fatalf("managed startup failure did not gate connection: %v", err)
		}
	}
}

// Opt-in validation of the large official distribution; ordinary tests never
// download or execute a real brokerage service.
func TestOpenDOfficialArchive(t *testing.T) {
	path := os.Getenv("WORKBENCH_TEST_OPEND_ARCHIVE")
	if path == "" {
		t.Skip("official archive not supplied")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	digest := sha256.New()
	archive := io.TeeReader(file, digest)
	dir := t.TempDir()
	if err := extractOpenD(archive, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, archive); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != openDArchiveSHA256 {
		t.Fatal("official package checksum mismatch")
	}
	binary, err := findOpenDBinary(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AppData.dat", "FutuOpenD.xml", "libssl.so.3", "libcrypto.so.3"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(binary), name)); err != nil {
			t.Fatal("incomplete command-line distribution", err)
		}
	}
}
