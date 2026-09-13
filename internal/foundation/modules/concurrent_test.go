package modules

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentEnableSettlesOnLatestIntent(t *testing.T) {
	registry := testRegistry(t)
	remote := newFakeRemote("token")
	t.Cleanup(remote.Close)
	attachDemo(t, registry, remote)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		enabled := i%2 == 0
		go func(enabled bool) {
			defer wg.Done()
			_, _ = registry.SetExternalEnabled(context.Background(), "demo_external", enabled)
		}(enabled)
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listed, _ := registry.GetListed("demo_external")
		if listed.Generation >= 8 && !listed.Pending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	listed, _ := registry.GetListed("demo_external")
	if listed.Generation < 8 {
		t.Fatalf("generation did not reach latest intent: %+v", listed)
	}
}
