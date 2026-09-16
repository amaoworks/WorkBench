package investment

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// openDService owns only the child it starts. apply and close are serialized by
// the module's connectMu; status may be read concurrently by HTTP requests.
type openDService struct {
	dir, binary    string
	mu             sync.Mutex
	state, message string
	cancel         context.CancelFunc
	done           chan struct{}
	record         futuRecord
	closed         bool
	install        func(context.Context, string) (string, error)
}

func newOpenDService(dir, binary string) *openDService {
	return &openDService{dir: dir, binary: binary, state: "stopped", install: installOpenD}
}

func (s *openDService) status() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.message
}

func (s *openDService) setStatus(state, message string) {
	s.mu.Lock()
	s.state, s.message = state, message
	s.mu.Unlock()
}

func (s *openDService) apply(rec futuRecord) {
	if s.closed {
		return
	}
	if s.done != nil && rec.Enabled && rec.Account == s.record.Account && rec.PasswordMD5 == s.record.PasswordMD5 && rec.Host == s.record.Host && rec.Port == s.record.Port {
		select {
		case <-s.done: // Saving again retries a failed start.
		default:
			return
		}
	}
	s.stop()
	if !rec.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done, s.record = cancel, make(chan struct{}), rec
	done := s.done
	s.setStatus("starting", "")
	go func() {
		defer close(done)
		if err := s.run(ctx, rec); err != nil && ctx.Err() == nil {
			s.setStatus("error", err.Error())
		}
	}()
}

func (s *openDService) stop() {
	if s.cancel != nil {
		s.setStatus("stopping", "")
		s.cancel()
		<-s.done
		s.cancel, s.done = nil, nil
	}
	s.setStatus("stopped", "")
}

func (s *openDService) close() { s.stop(); s.closed = true }

func (s *openDService) run(ctx context.Context, rec futuRecord) error {
	if rec.Account == "" || len(rec.PasswordMD5) != 32 {
		return errors.New("请填写富途牛牛账号和登录密码后重新保存")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("当前系统不支持自动运行 OpenD，请在部署配置中接入 Linux x86_64 OpenD 服务")
	}
	if s.binary == "" {
		if _, err := os.Stat("/lib64/ld-linux-x86-64.so.2"); err != nil {
			return errors.New("当前环境缺少 Linux 动态运行库，请使用配套 OpenD 容器部署")
		}
	}
	if rec.Host != "localhost" && (net.ParseIP(rec.Host) == nil || !net.ParseIP(rec.Host).IsLoopback()) {
		return errors.New("远程 OpenD 需要配置配套登录服务；本机启动仅支持回环地址")
	}
	dir, err := filepath.Abs(s.dir)
	if err != nil {
		return errors.New("无法确定 OpenD 运行目录")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建 OpenD 运行目录，请检查目录权限")
	}
	lock := flock.New(filepath.Join(dir, "service.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return errors.New("OpenD 运行目录已被另一工作台实例使用，或无法加锁")
	}
	defer lock.Close()
	binary := s.binary
	if binary == "" {
		s.setStatus("installing", "")
		binary, err = s.install(ctx, dir)
		if err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return errors.New("OpenD 程序路径无效")
	}
	host := rec.Host
	if host == "localhost" {
		host = "127.0.0.1"
	}
	// Never silently connect using a different account already occupying the port.
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(rec.Port)))
	if err != nil {
		return errors.New("OpenD 行情端口已被占用或不可用，请检查部署配置")
	}
	listener.Close()
	for _, sub := range []string{"home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			return errors.New("无法创建 OpenD 数据目录")
		}
	}
	config, err := writeOpenDConfig(dir, host, rec)
	if err != nil {
		return errors.New("无法写入 OpenD 登录配置，请检查目录权限")
	}
	defer os.Remove(config)
	cmd := exec.CommandContext(ctx, binary, "-no_monitor=1", "-cfg_file="+config)
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp"), "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LD_LIBRARY_PATH=" + filepath.Dir(binary)}
	// Credentials never appear in argv, the inherited environment, HTTP responses
	// or application logs. Only classify a few known failure types from output.
	output := &openDDiagnostics{}
	cmd.Stdout, cmd.Stderr = output, output
	configureOpenDProcess(cmd)
	cmd.WaitDelay = 3 * time.Second
	s.setStatus("starting", "")
	if err := cmd.Start(); err != nil {
		return errors.New("无法执行 OpenD，请确认程序可执行且系统已安装所需运行库")
	}
	defer cleanupOpenDProcess(cmd)
	s.setStatus("running", "")
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reason := output.reason(); reason != "" {
		return errors.New(reason)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("OpenD 已退出（退出码 %d），可重试启动；详细日志位于运行目录", exit.ExitCode())
		}
	}
	return errors.New("OpenD 已停止，可重试启动")
}

func writeOpenDConfig(dir, host string, rec futuRecord) (string, error) {
	config := struct {
		XMLName     xml.Name `xml:"futu_opend"`
		IP          string   `xml:"ip"`
		Port        int      `xml:"api_port"`
		Proto       int      `xml:"push_proto_type"`
		TelnetIP    string   `xml:"telnet_ip"`
		TelnetPort  int      `xml:"telnet_port"`
		Account     string   `xml:"login_account"`
		PasswordMD5 string   `xml:"login_pwd_md5"`
		Lang        string   `xml:"lang"`
		LogLevel    string   `xml:"log_level"`
	}{IP: host, Port: rec.Port, Proto: 1, TelnetIP: "127.0.0.1", TelnetPort: 22222, Account: rec.Account, PasswordMD5: rec.PasswordMD5, Lang: "chs", LogLevel: "info"}
	data, err := xml.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".login-*.xml")
	if err != nil {
		return "", err
	}
	_, err = file.Write(append([]byte(xml.Header), data...))
	err = errors.Join(err, file.Close())
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

type openDDiagnostics struct {
	mu   sync.Mutex
	tail string
}

func (d *openDDiagnostics) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tail += string(p)
	if len(d.tail) > 16384 {
		d.tail = d.tail[len(d.tail)-16384:]
	}
	return len(p), nil
}
func (d *openDDiagnostics) reason() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.Contains(d.tail, "error while loading shared libraries") {
		return "OpenD 缺少系统运行库，请安装对应运行库后重试"
	}
	if strings.Contains(d.tail, "服务器启动失败") {
		return "OpenD 服务端口已被占用或不可用，请检查部署配置"
	}
	if strings.Contains(d.tail, "登录失败") {
		return "富途牛牛登录失败，请核对账号密码并完成设备验证后重试"
	}
	return ""
}
