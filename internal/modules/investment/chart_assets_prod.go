//go:build !dev

package investment

import (
	"io/fs"
)

func terminalChartAssets() (fs.FS, error) {
	return fs.Sub(chartAssets, "chart")
}
