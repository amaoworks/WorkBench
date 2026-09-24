//go:build dev

package investment

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const devChartDirEnv = "WORKBENCH_DEV_CHART_DIR"

type devChartFS struct {
	rootPath string
}

func (f devChartFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return os.OpenInRoot(f.rootPath, name)
}

func terminalChartAssets() (fs.FS, error) {
	dir := os.Getenv(devChartDirEnv)
	if dir == "" {
		return nil, fmt.Errorf("%s must name the investment chart directory in a dev build", devChartDirEnv)
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%s must be an absolute path", devChartDirEnv)
	}

	cleanDir := filepath.Clean(dir)
	info, err := os.Stat(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", devChartDirEnv, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s must name a directory", devChartDirEnv)
	}
	index, err := os.OpenInRoot(cleanDir, "index.html")
	if err != nil {
		return nil, fmt.Errorf("%s is missing index.html: %w", devChartDirEnv, err)
	}
	indexInfo, err := index.Stat()
	if closeErr := index.Close(); err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if !indexInfo.Mode().IsRegular() {
		return nil, errors.New("investment chart index.html must be a regular file")
	}
	return devChartFS{rootPath: cleanDir}, nil
}
