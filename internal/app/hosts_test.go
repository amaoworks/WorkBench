package app

import (
	"slices"
	"testing"
)

func TestUniqueHostsIsCaseInsensitiveAndKeepsOrder(t *testing.T) {
	got := uniqueHosts([]string{"w.vm2.de5.net", " ", "Example.test", "W.VM2.DE5.NET", "example.test", "127.0.0.1:9091"})
	want := []string{"w.vm2.de5.net", "Example.test", "127.0.0.1:9091"}
	if !slices.Equal(got, want) {
		t.Fatalf("uniqueHosts = %q, want %q", got, want)
	}
}
