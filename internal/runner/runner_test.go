package runner

import (
	"context"
	"io"
	"log"
	"runtime"
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
	testServerWithOpenVPN = func(ctx context.Context, server vpngate.Server, options vpngate.OpenVPNTestOptions) (vpngate.OpenVPNTestResult, error) {
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

func TestRunnerTestServerAllowsTestWhileConnected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("connected test bypass currently depends on Linux fwmark routing")
	}

	previousTestServer := testServerWithOpenVPN
	previousResolveIP := resolveIPExecutableForRunner
	previousEnsureBypass := ensureBypassPolicyRoutingForRunner
	defer func() {
		testServerWithOpenVPN = previousTestServer
		resolveIPExecutableForRunner = previousResolveIP
		ensureBypassPolicyRoutingForRunner = previousEnsureBypass
	}()

	var receivedOptions vpngate.OpenVPNTestOptions
	testServerWithOpenVPN = func(ctx context.Context, server vpngate.Server, options vpngate.OpenVPNTestOptions) (vpngate.OpenVPNTestResult, error) {
		receivedOptions = options
		return vpngate.OpenVPNTestResult{
			Duration: 1200 * time.Millisecond,
			Detail:   "握手成功",
		}, nil
	}
	resolveIPExecutableForRunner = func() (string, error) {
		return "/sbin/ip", nil
	}
	ensureBypassPolicyRoutingForRunner = func(ipExecutable, gateway, iface string, table, mark int) error {
		if ipExecutable != "/sbin/ip" {
			t.Fatalf("ipExecutable = %q, want %q", ipExecutable, "/sbin/ip")
		}
		if gateway != "192.168.31.1" {
			t.Fatalf("gateway = %q, want %q", gateway, "192.168.31.1")
		}
		if iface != "eth0" {
			t.Fatalf("iface = %q, want %q", iface, "eth0")
		}
		if table != 100 {
			t.Fatalf("table = %d, want 100", table)
		}
		if mark != 1 {
			t.Fatalf("mark = %d, want 1", mark)
		}
		return nil
	}

	current := &ConnectionInfo{HostName: "connected-node", IP: "1.1.1.1"}
	r := &Runner{
		logger:            log.New(io.Discard, "", 0),
		state:             StateConnected,
		current:           current,
		updatedAt:         time.Now(),
		connectedAt:       time.Now(),
		originalGateway:   "192.168.31.1",
		originalInterface: "eth0",
		quarantine:        make(map[string]nodeHealth),
	}

	result, err := r.TestServer(context.Background(), vpngate.Server{HostName: "other-node", IP: "2.2.2.2"})
	if err != nil {
		t.Fatalf("TestServer() error = %v", err)
	}
	if result.Duration <= 0 {
		t.Fatalf("TestServer() duration = %s, want positive duration", result.Duration)
	}
	if receivedOptions.BypassMark != 1 {
		t.Fatalf("BypassMark = %d, want 1", receivedOptions.BypassMark)
	}

	r.mu.RLock()
	state := r.state
	stillCurrent := r.current
	proc := r.proc
	r.mu.RUnlock()

	if state != StateConnected {
		t.Fatalf("state = %s, want %s", state, StateConnected)
	}
	if stillCurrent != current {
		t.Fatal("current connection changed during test")
	}
	if proc != nil {
		t.Fatal("runner proc changed during test")
	}
}

func TestRunnerTestServerRejectsTransientConnectionStates(t *testing.T) {
	for _, state := range []State{StateConnecting, StateDisconnecting} {
		t.Run(string(state), func(t *testing.T) {
			r := &Runner{
				logger:     log.New(io.Discard, "", 0),
				state:      state,
				current:    &ConnectionInfo{HostName: "busy-node", IP: "1.1.1.1"},
				updatedAt:  time.Now(),
				quarantine: make(map[string]nodeHealth),
			}

			_, err := r.TestServer(context.Background(), vpngate.Server{HostName: "other-node", IP: "2.2.2.2"})
			if err == nil {
				t.Fatal("TestServer() error = nil, want conflict")
			}
		})
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
