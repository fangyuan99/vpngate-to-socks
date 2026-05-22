package runner

import (
	"context"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpngate/internal/vpngate"
)

func TestRunnerTestServerAllowsConcurrentTests(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32

	previous := testServerWithOpenVPN
	testServerWithOpenVPN = func(ctx context.Context, server vpngate.Server) (vpngate.OpenVPNTestResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		updateMaxActiveTests(&maxActive, current)

		started <- server.HostName

		select {
		case <-release:
		case <-ctx.Done():
			return vpngate.OpenVPNTestResult{}, ctx.Err()
		}

		return vpngate.OpenVPNTestResult{
			Duration: 1200 * time.Millisecond,
			Detail:   "握手成功",
		}, nil
	}
	defer func() {
		testServerWithOpenVPN = previous
	}()

	r := &Runner{
		logger:     log.New(io.Discard, "", 0),
		state:      StateDisconnected,
		updatedAt:  time.Now(),
		quarantine: make(map[string]nodeHealth),
	}

	type testResult struct {
		result vpngate.OpenVPNTestResult
		err    error
	}

	results := make(chan testResult, 2)
	var wg sync.WaitGroup
	for _, server := range []vpngate.Server{
		{HostName: "node-a", IP: "1.1.1.1"},
		{HostName: "node-b", IP: "2.2.2.2"},
	} {
		wg.Add(1)
		go func(server vpngate.Server) {
			defer wg.Done()
			result, err := r.TestServer(context.Background(), server)
			results <- testResult{result: result, err: err}
		}(server)
	}

	waitForStarted := func() {
		t.Helper()

		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for runner test to start")
		}
	}

	waitForStarted()
	waitForStarted()
	close(release)

	wg.Wait()
	close(results)

	if maxActive.Load() != 2 {
		t.Fatalf("max concurrent tests = %d, want 2", maxActive.Load())
	}

	r.mu.RLock()
	activeTests := r.activeTests
	r.mu.RUnlock()
	if activeTests != 0 {
		t.Fatalf("activeTests = %d, want 0 after all tests complete", activeTests)
	}

	for result := range results {
		if result.err != nil {
			t.Fatalf("TestServer() error = %v", result.err)
		}
		if result.result.Duration <= 0 {
			t.Fatalf("TestServer() duration = %s, want positive duration", result.result.Duration)
		}
	}
}

func updateMaxActiveTests(target *atomic.Int32, value int32) {
	for {
		current := target.Load()
		if value <= current {
			return
		}
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}
