//go:build !dev

package investment

import (
	"bytes"
	"io/fs"
	"testing"
)

func TestProductionChartAssetsIgnoreDeveloperDirectory(t *testing.T) {
	t.Setenv("WORKBENCH_DEV_CHART_DIR", "relative/invalid/path")

	assets, err := terminalChartAssets()
	if err != nil {
		t.Fatalf("production asset filesystem: %v", err)
	}
	got, err := fs.ReadFile(assets, "datafeed.js")
	if err != nil {
		t.Fatalf("read embedded chart asset: %v", err)
	}
	want, err := fs.ReadFile(chartAssets, "chart/datafeed.js")
	if err != nil {
		t.Fatalf("read embedded source asset: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("production chart assets must come from the embedded filesystem")
	}
}
