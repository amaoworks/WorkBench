package investment

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 10.10 removed documented account/password configuration. Keep the last
// supported configuration-login release until interactive login is integrated.
const openDVersion = "10.9.6908"
const openDArchiveSHA256 = "28b350d25843de28e88c8d13d9eef3b451b509ee06a5ad927e8abf752ffb726c"

func installOpenD(ctx context.Context, dir string) (string, error) {
	destination := filepath.Join(dir, "versions", openDVersion)
	if binary, err := findOpenDBinary(destination); err == nil {
		return binary, nil
	}
	stage, err := os.MkdirTemp(dir, ".install-*")
	if err != nil {
		return "", errors.New("无法创建 OpenD 安装目录，请检查目录权限")
	}
	defer os.RemoveAll(stage)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Hostname() != "softwaredownload.futunn.com" {
			return errors.New("unexpected OpenD download redirect")
		}
		return nil
	}}
	url := "https://softwaredownload.futunn.com/Futu_OpenD_" + openDVersion + "_Ubuntu18.04.tar.gz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Workbench OpenD installer")
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("下载富途牛牛服务失败，请检查网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("富途牛牛官方下载暂不可用，请稍后重试")
	}
	digest := sha256.New()
	archive := io.TeeReader(io.LimitReader(response.Body, 512<<20), digest)
	if err := extractOpenD(archive, stage); err != nil {
		return "", errors.New("富途牛牛安装包无效或解压失败，请重试")
	}
	if _, err := io.Copy(io.Discard, archive); err != nil || hex.EncodeToString(digest.Sum(nil)) != openDArchiveSHA256 {
		return "", errors.New("富途牛牛安装包校验失败，请重试")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	binary, err := findOpenDBinary(stage)
	if err != nil {
		return "", errors.New("安装包中未找到 OpenD 程序")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return "", errors.New("无法创建 OpenD 版本目录")
	}
	// Only retain the command-line distribution; the archive also contains a GUI.
	if err := os.Rename(filepath.Dir(binary), destination); err != nil {
		return "", errors.New("无法保存 OpenD 安装文件，请检查目录权限")
	}
	return filepath.Join(destination, "FutuOpenD"), nil
}

// Do not trust paths or links in an archive, even from the official distributor.
func extractOpenD(source io.Reader, destination string) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	for count := 0; count < 10000; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(header.Name)
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return errors.New("unsafe archive path")
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > 512<<20 {
				return errors.New("archive file too large")
			}
			total += header.Size
			if total > 1<<30 {
				return errors.New("archive too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if header.Mode&0111 != 0 || filepath.Base(target) == "FutuOpenD" {
				mode = 0700
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return err
			}
		default:
			return errors.New("archive links and special files are not supported")
		}
	}
	return errors.New("too many archive entries")
}

func findOpenDBinary(dir string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() && entry.Name() == "FutuOpenD" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}
