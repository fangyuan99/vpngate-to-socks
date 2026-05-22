package web

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vpngate/internal/runner"
	"vpngate/internal/runnerclient"
	"vpngate/internal/vpngate"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type stubRunnerControl struct {
	enabled             bool
	status              func(context.Context) (runner.Status, error)
	connect             func(context.Context, vpngate.Server) (runner.Status, error)
	disconnect          func(context.Context) (runner.Status, error)
	testServer          func(context.Context, vpngate.Server) (vpngate.OpenVPNTestResult, error)
	updateAutoReconnect func(context.Context, runner.AutoReconnectConfig) (runner.Status, error)
}

func (s *stubRunnerControl) Enabled() bool {
	return s != nil && s.enabled
}

func (s *stubRunnerControl) Status(ctx context.Context) (runner.Status, error) {
	if s != nil && s.status != nil {
		return s.status(ctx)
	}

	return runner.Status{}, nil
}

func (s *stubRunnerControl) Connect(ctx context.Context, server vpngate.Server) (runner.Status, error) {
	if s != nil && s.connect != nil {
		return s.connect(ctx, server)
	}

	return runner.Status{}, nil
}

func (s *stubRunnerControl) Disconnect(ctx context.Context) (runner.Status, error) {
	if s != nil && s.disconnect != nil {
		return s.disconnect(ctx)
	}

	return runner.Status{}, nil
}

func (s *stubRunnerControl) TestServer(ctx context.Context, server vpngate.Server) (vpngate.OpenVPNTestResult, error) {
	if s != nil && s.testServer != nil {
		return s.testServer(ctx, server)
	}

	return vpngate.OpenVPNTestResult{}, nil
}

func (s *stubRunnerControl) UpdateAutoReconnect(ctx context.Context, config runner.AutoReconnectConfig) (runner.Status, error) {
	if s != nil && s.updateAutoReconnect != nil {
		return s.updateAutoReconnect(ctx, config)
	}

	return runner.Status{}, nil
}

func TestSelectRecommendedServer(t *testing.T) {
	servers := []vpngate.Server{
		{HostName: "jp-zero-users", IP: "0.0.0.0", CountryLong: "Japan", CountryShort: "JP", TotalUsers: 0, Uptime: 1, NumVPNSessions: 1, OpenVPNConfigDataBase64: "cfg0"},
		{HostName: "jp-more-users", IP: "1.1.1.1", CountryLong: "Japan", CountryShort: "JP", TotalUsers: 20, Uptime: 10, NumVPNSessions: 1, OpenVPNConfigDataBase64: "cfg1"},
		{HostName: "kr-top", IP: "2.2.2.2", CountryLong: "Korea Republic of", CountryShort: "KR", TotalUsers: 1, Uptime: 1, NumVPNSessions: 1, OpenVPNConfigDataBase64: "cfg2"},
		{HostName: "jp-best", IP: "3.3.3.3", CountryLong: "Japan", CountryShort: "JP", TotalUsers: 5, Uptime: 3, NumVPNSessions: 2, OpenVPNConfigDataBase64: "cfg3"},
		{HostName: "jp-higher-uptime", IP: "4.4.4.4", CountryLong: "Japan", CountryShort: "JP", TotalUsers: 5, Uptime: 9, NumVPNSessions: 1, OpenVPNConfigDataBase64: "cfg4"},
	}

	server, ok := selectRecommendedServer(servers, "", "JP")
	if !ok {
		t.Fatal("selectRecommendedServer() ok = false, want true")
	}

	if server.HostName != "jp-best" {
		t.Fatalf("selectRecommendedServer() host = %q, want %q", server.HostName, "jp-best")
	}
}

func TestBuildPageDataSortsRowsByRequestedField(t *testing.T) {
	app, err := NewApp(log.New(io.Discard, "", 0), nil, nil)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	app.mu.Lock()
	app.servers = []vpngate.Server{
		{HostName: "slow", IP: "1.1.1.1", CountryLong: "Japan", CountryShort: "JP", Speed: 100, TotalUsers: 10, Uptime: 10, NumVPNSessions: 10, OpenVPNConfigDataBase64: "cfg1"},
		{HostName: "fast", IP: "2.2.2.2", CountryLong: "Japan", CountryShort: "JP", Speed: 500, TotalUsers: 5, Uptime: 5, NumVPNSessions: 5, OpenVPNConfigDataBase64: "cfg2"},
		{HostName: "mid", IP: "3.3.3.3", CountryLong: "Japan", CountryShort: "JP", Speed: 300, TotalUsers: 8, Uptime: 8, NumVPNSessions: 8, OpenVPNConfigDataBase64: "cfg3"},
	}
	app.mu.Unlock()

	page := app.buildPageData("", "", "", "", "speed", "desc")
	if len(page.Rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(page.Rows))
	}

	if page.Rows[0].Name != "fast" || page.Rows[1].Name != "mid" || page.Rows[2].Name != "slow" {
		t.Fatalf("sorted rows = [%s %s %s], want [fast mid slow]", page.Rows[0].Name, page.Rows[1].Name, page.Rows[2].Name)
	}
}

func TestHandleAutoReconnectUpdatesRunnerConfig(t *testing.T) {
	var received runner.AutoReconnectConfig
	runnerStub := &stubRunnerControl{
		enabled: true,
		updateAutoReconnect: func(ctx context.Context, config runner.AutoReconnectConfig) (runner.Status, error) {
			received = config
			return runner.Status{
				AutoReconnect: runner.AutoReconnectStatus{
					Enabled:                true,
					PreferredCountry:       "JP",
					MonitorIntervalSeconds: 15,
				},
			}, nil
		},
	}

	app, err := NewApp(log.New(io.Discard, "", 0), nil, runnerStub)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	form := url.Values{
		"auto_reconnect_enabled": []string{"1"},
		"auto_country":           []string{"JP"},
		"auto_monitor_interval":  []string{"15"},
		"q":                      []string{"japan"},
		"country":                []string{"JP"},
		"sort":                   []string{"speed"},
		"order":                  []string{"desc"},
	}
	req := httptest.NewRequest(http.MethodPost, "/vpn/auto-reconnect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")

	recorder := httptest.NewRecorder()
	app.handleAutoReconnect(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("handleAutoReconnect() status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if !received.Enabled {
		t.Fatal("received.Enabled = false, want true")
	}
	if received.PreferredCountry != "JP" {
		t.Fatalf("received.PreferredCountry = %q, want %q", received.PreferredCountry, "JP")
	}
	if received.MonitorInterval != 15*time.Second {
		t.Fatalf("received.MonitorInterval = %s, want %s", received.MonitorInterval, 15*time.Second)
	}

	var response actionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !response.OK {
		t.Fatalf("response.OK = false, error = %q", response.Error)
	}
	if !strings.Contains(response.Notice, "自动重连已启用") {
		t.Fatalf("notice = %q, want substring %q", response.Notice, "自动重连已启用")
	}
}

func TestHandleVPNConnectUsesLatestServerList(t *testing.T) {
	var connectCalls atomic.Int32
	runnerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect" {
			http.NotFound(w, r)
			return
		}

		connectCalls.Add(1)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": runner.Status{State: runner.StateConnecting, SocksListenAddr: "127.0.0.1:1080"},
		})
	}))
	defer runnerServer.Close()

	app := mustNewTestApp(t, latestListResponse("fresh-node", "2.2.2.2", 200), runnerServer.URL, runnerServer.Client())
	app.mu.Lock()
	app.servers = []vpngate.Server{{HostName: "stale-node", IP: "1.1.1.1", CountryLong: "Japan", CountryShort: "JP"}}
	app.mu.Unlock()

	form := url.Values{
		"hostname": []string{"stale-node"},
		"ip":       []string{"1.1.1.1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/vpn/connect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")

	recorder := httptest.NewRecorder()
	app.handleVPNConnect(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("handleVPNConnect() status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	var response actionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if response.OK {
		t.Fatal("handleVPNConnect() response.OK = true, want false")
	}
	if !strings.Contains(response.Error, "未在最新节点列表中找到对应节点") {
		t.Fatalf("handleVPNConnect() error = %q, want substring %q", response.Error, "未在最新节点列表中找到对应节点")
	}
	if connectCalls.Load() != 0 {
		t.Fatalf("runner connect calls = %d, want 0", connectCalls.Load())
	}
}

func TestHandleVPNConnectForwardsLatestServerPayload(t *testing.T) {
	type connectPayload struct {
		Server vpngate.Server `json:"server"`
	}

	payloadCh := make(chan connectPayload, 1)
	runnerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect" {
			http.NotFound(w, r)
			return
		}

		defer r.Body.Close()
		var payload connectPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payloadCh <- payload

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": runner.Status{State: runner.StateConnecting, SocksListenAddr: "127.0.0.1:1080"},
		})
	}))
	defer runnerServer.Close()

	app := mustNewTestApp(t, strings.Join([]string{
		"*vpn_servers",
		"#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64",
		"shared-node,1.1.1.1,300,12,450,Japan,JP,1,10,100,1000,2weeks,Fresh Operator,Fresh Message,ZnJlc2gtY29uZmln",
		"*",
	}, "\n"), runnerServer.URL, runnerServer.Client())
	app.mu.Lock()
	app.servers = []vpngate.Server{{
		HostName:                "shared-node",
		IP:                      "1.1.1.1",
		CountryLong:             "Japan",
		CountryShort:            "JP",
		Operator:                "Stale Operator",
		Message:                 "Stale Message",
		OpenVPNConfigDataBase64: "c3RhbGUtY29uZmln",
	}}
	app.mu.Unlock()

	form := url.Values{
		"hostname": []string{"shared-node"},
		"ip":       []string{"1.1.1.1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/vpn/connect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")

	recorder := httptest.NewRecorder()
	app.handleVPNConnect(recorder, req)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("handleVPNConnect() status = %d, want %d", recorder.Code, http.StatusAccepted)
	}

	var received connectPayload
	select {
	case received = <-payloadCh:
	default:
		t.Fatal("runner connect request was not received")
	}

	if received.Server.Operator != "Fresh Operator" {
		t.Fatalf("forwarded operator = %q, want %q", received.Server.Operator, "Fresh Operator")
	}
	if received.Server.OpenVPNConfigDataBase64 != "ZnJlc2gtY29uZmln" {
		t.Fatalf("forwarded config = %q, want %q", received.Server.OpenVPNConfigDataBase64, "ZnJlc2gtY29uZmln")
	}
}

func TestHandleVPNConnectRecommendedConnectsBestFilteredServer(t *testing.T) {
	type connectPayload struct {
		Server vpngate.Server `json:"server"`
	}

	payloadCh := make(chan connectPayload, 1)
	runnerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect" {
			http.NotFound(w, r)
			return
		}

		defer r.Body.Close()
		var payload connectPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payloadCh <- payload

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": runner.Status{State: runner.StateConnecting, SocksListenAddr: "127.0.0.1:1080"},
		})
	}))
	defer runnerServer.Close()

	app := mustNewTestApp(t, strings.Join([]string{
		"*vpn_servers",
		"#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64",
		"jp-zero,0.0.0.0,999,1,999,Japan,JP,1,1,0,1000,2weeks,Operator Zero,,ZHVtbXk=",
		"jp-mid,1.1.1.1,150,20,300,Japan,JP,10,10,100,1000,2weeks,Operator One,,ZHVtbXk=",
		"kr-top,2.2.2.2,400,30,500,Korea Republic of,KR,1,10,1,1000,2weeks,Operator Two,,ZHVtbXk=",
		"jp-best,3.3.3.3,300,25,450,Japan,JP,2,3,5,1000,2weeks,Operator Three,,ZHVtbXk=",
		"jp-higher-uptime,4.4.4.4,999,10,900,Japan,JP,1,9,5,1000,2weeks,Operator Four,,ZHVtbXk=",
		"*",
	}, "\n"), runnerServer.URL, runnerServer.Client())

	form := url.Values{
		"country": []string{"JP"},
	}
	req := httptest.NewRequest(http.MethodPost, "/vpn/connect/recommended", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Accept", "application/json")

	recorder := httptest.NewRecorder()
	app.handleVPNConnectRecommended(recorder, req)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("handleVPNConnectRecommended() status = %d, want %d", recorder.Code, http.StatusAccepted)
	}

	var received connectPayload
	select {
	case received = <-payloadCh:
	default:
		t.Fatal("runner connect request was not received")
	}

	if received.Server.HostName != "jp-best" {
		t.Fatalf("connected host = %q, want %q", received.Server.HostName, "jp-best")
	}

	var response actionResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if !response.OK {
		t.Fatalf("handleVPNConnectRecommended() response.OK = false, error = %q", response.Error)
	}
	if !strings.Contains(response.Notice, "已开始连接推荐节点 jp-best") {
		t.Fatalf("handleVPNConnectRecommended() notice = %q, want substring %q", response.Notice, "已开始连接推荐节点 jp-best")
	}
}

func TestHandleServerTestAllowsConcurrentRequests(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32

	runnerStub := &stubRunnerControl{
		enabled: true,
		testServer: func(ctx context.Context, server vpngate.Server) (vpngate.OpenVPNTestResult, error) {
			current := active.Add(1)
			defer active.Add(-1)
			updateMaxAtomic(&maxActive, current)

			started <- server.HostName

			select {
			case <-release:
			case <-ctx.Done():
				return vpngate.OpenVPNTestResult{}, ctx.Err()
			}

			return vpngate.OpenVPNTestResult{
				Duration: 1500 * time.Millisecond,
				Detail:   "握手成功",
			}, nil
		},
	}

	app, err := NewApp(log.New(io.Discard, "", 0), nil, runnerStub)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	app.mu.Lock()
	app.servers = []vpngate.Server{
		{HostName: "node-a", IP: "1.1.1.1"},
		{HostName: "node-b", IP: "2.2.2.2"},
	}
	app.mu.Unlock()

	type requestResult struct {
		code int
		body actionResponse
	}

	makeRequest := func(hostName, ip string) requestResult {
		form := url.Values{
			"hostname": []string{hostName},
			"ip":       []string{ip},
		}
		req := httptest.NewRequest(http.MethodPost, "/servers/test", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("Accept", "application/json")

		recorder := httptest.NewRecorder()
		app.handleServerTest(recorder, req)

		var response actionResponse
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}

		return requestResult{code: recorder.Code, body: response}
	}

	results := make(chan requestResult, 2)
	var wg sync.WaitGroup
	for _, server := range []vpngate.Server{
		{HostName: "node-a", IP: "1.1.1.1"},
		{HostName: "node-b", IP: "2.2.2.2"},
	} {
		wg.Add(1)
		go func(server vpngate.Server) {
			defer wg.Done()
			results <- makeRequest(server.HostName, server.IP)
		}(server)
	}

	waitForStarted := func() {
		t.Helper()

		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent test request to start")
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

	for result := range results {
		if result.code != http.StatusOK {
			t.Fatalf("handleServerTest() status = %d, want %d", result.code, http.StatusOK)
		}
		if !result.body.OK {
			t.Fatalf("handleServerTest() response.OK = false, error = %q", result.body.Error)
		}
		if result.body.Test == nil {
			t.Fatal("handleServerTest() response.Test = nil, want payload")
		}
		if result.body.Test.Status != "测试通过" {
			t.Fatalf("handleServerTest() response.Test.Status = %q, want %q", result.body.Test.Status, "测试通过")
		}
	}
}

func updateMaxAtomic(target *atomic.Int32, value int32) {
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

func mustNewTestApp(t *testing.T, listResponse, runnerURL string, runnerHTTPClient *http.Client) *App {
	t.Helper()

	app, err := NewApp(
		log.New(io.Discard, "", 0),
		&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != vpngate.IPhoneAPIURL {
				t.Fatalf("unexpected list request URL: %s", req.URL.String())
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(listResponse)),
				Request:    req,
			}, nil
		})},
		runnerclient.New(runnerURL, runnerHTTPClient),
	)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	return app
}

func latestListResponse(hostName, ip string, score int64) string {
	return strings.Join([]string{
		"*vpn_servers",
		"#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64",
		hostName + "," + ip + "," + strconv.FormatInt(score, 10) + ",10,200,Japan,JP,1,10,100,1000,2weeks,Operator One,,ZHVtbXk=",
		"*",
	}, "\n")
}
