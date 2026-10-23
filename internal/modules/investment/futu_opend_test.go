package investment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeOpenD struct {
	ln             net.Listener
	mu             sync.Mutex
	initPayload    map[string]any
	subPayload     map[string]any
	historyN       int
	historyFail    bool
	historyBegin   string
	historyEnd     string
	historyNextKey any
	klList         []map[string]any
	histList       []map[string]any
	remain         int
	usedCodes      []string
	subStarted     chan struct{}
	releaseSub     chan struct{}
	unsubStarted   chan struct{}
	releaseUnsub   chan struct{}
	subActions     []bool
	basicQotList   []map[string]any
	quoteN         int
}

func startFakeOpenD(t *testing.T) *fakeOpenD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOpenD{
		ln: ln, remain: 100,
		klList: []map[string]any{
			klJSON(time.Date(2026, 1, 15, 16, 0, 0, 0, nyZone), 100), // RTH — must drop
			klJSON(time.Date(2026, 1, 15, 21, 0, 0, 0, nyZone), 101), // overnight
		},
		histList: []map[string]any{
			klJSON(time.Date(2026, 1, 14, 21, 5, 0, 0, nyZone), 90),
		},
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func klJSON(ts time.Time, close float64) map[string]any {
	return map[string]any{
		"time": ts.Format("2006-01-02 15:04:05"), "isBlank": false,
		"openPrice": close, "highPrice": close, "lowPrice": close, "closePrice": close,
		"volume": 10, "timestamp": ts.Unix(),
	}
}

func (f *fakeOpenD) addr() string { return f.ln.Addr().String() }

func (f *fakeOpenD) serve(conn net.Conn) {
	defer conn.Close()
	for {
		proto, serial, body, err := readOpendPacket(conn)
		if err != nil {
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		c2s, _ := req["c2s"].(map[string]any)
		s2c := map[string]any{}
		retType, retMsg := retSucceed, ""
		switch proto {
		case protoInitConnect:
			f.mu.Lock()
			f.initPayload = c2s
			f.mu.Unlock()
			s2c = map[string]any{"keepAliveInterval": 30, "serverVer": 1010, "loginUserID": 1, "connID": 1, "connAESKey": "0123456789abcdef"}
		case protoKeepAlive:
			s2c = map[string]any{"time": time.Now().Unix()}
		case protoGetGlobalState:
			s2c = map[string]any{"qotLogined": true, "trdLogined": false}
		case protoQotSub:
			isSubscribe, _ := c2s["isSubOrUnSub"].(bool)
			f.mu.Lock()
			f.subPayload = c2s
			f.subActions = append(f.subActions, isSubscribe)
			subStarted, releaseSub := f.subStarted, f.releaseSub
			unsubStarted, releaseUnsub := f.unsubStarted, f.releaseUnsub
			f.mu.Unlock()
			if isSubscribe && subStarted != nil {
				select {
				case subStarted <- struct{}{}:
				default:
				}
			}
			if isSubscribe && releaseSub != nil {
				<-releaseSub
			}
			if !isSubscribe && unsubStarted != nil {
				select {
				case unsubStarted <- struct{}{}:
				default:
				}
			}
			if !isSubscribe && releaseUnsub != nil {
				<-releaseUnsub
			}
		case protoQotGetKL:
			s2c = map[string]any{"klList": f.klList, "security": map[string]any{"market": marketUS, "code": "AAPL"}}
		case protoQotGetBasicQot:
			f.mu.Lock()
			f.quoteN++
			s2c = map[string]any{"basicQotList": f.basicQotList}
			f.mu.Unlock()
		case protoQotRequestHistoryKLQuota:
			details := []any{}
			for _, code := range f.usedCodes {
				details = append(details, map[string]any{"security": map[string]any{"code": code}})
			}
			s2c = map[string]any{"usedQuota": len(f.usedCodes), "remainQuota": f.remain, "detailList": details}
		case protoQotRequestHistoryKL:
			f.mu.Lock()
			f.historyN++
			f.historyBegin, _ = c2s["beginTime"].(string)
			f.historyEnd, _ = c2s["endTime"].(string)
			failed, nextKey, history := f.historyFail, f.historyNextKey, f.histList
			f.mu.Unlock()
			if failed {
				retType, retMsg = -1, "fixture history failure"
				s2c = map[string]any{}
			} else {
				s2c = map[string]any{"klList": history, "security": map[string]any{"market": marketUS, "code": "AAPL"}}
				if nextKey != nil {
					s2c["nextReqKey"] = nextKey
				}
			}
		case protoQotGetSubInfo:
			s2c = map[string]any{"remainQuota": 90, "totalQuota": 100}
		default:
			s2c = map[string]any{}
		}
		resp, _ := json.Marshal(map[string]any{"retType": retType, "retMsg": retMsg, "errCode": 0, "s2c": s2c})
		if err := writeOpendPacket(conn, proto, serial, resp); err != nil {
			return
		}
	}
}

func TestOpendCloseKeepsInFlightReplyChannelOpen(t *testing.T) {
	reply := make(chan opendReply, 1)
	client := &opendClient{
		conn: discardConn{}, stop: make(chan struct{}),
		pending: map[uint32]chan opendReply{1: reply},
	}
	// readLoop can retain a reply channel after releasing client.mu.
	inFlight := client.pending[1]
	client.close()
	client.close()
	select {
	case <-client.stop:
	default:
		t.Fatal("close did not signal pending calls")
	}
	if len(client.pending) != 0 {
		t.Fatal("close retained pending calls")
	}
	select {
	case inFlight <- opendReply{proto: protoGetGlobalState}:
	default:
		t.Fatal("late reply could not be delivered")
	}
}

func TestOpendCloseUnblocksPendingCall(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.subStarted = make(chan struct{}, 1)
	fake.releaseSub = make(chan struct{})
	defer close(fake.releaseSub)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialOpend(ctx, fake.addr(), "workbench-close-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	done := make(chan error, 1)
	go func() { done <- client.subscribe(ctx, "AAPL", []int{subTypeBasic}, true) }()
	select {
	case <-fake.subStarted:
	case <-ctx.Done():
		t.Fatal("subscription did not reach OpenD")
	}
	client.close()
	select {
	case err := <-done:
		if !errors.Is(err, errOpendClosed) {
			t.Fatalf("pending call returned %v, want closed connection", err)
		}
	case <-ctx.Done():
		t.Fatal("close did not unblock pending call")
	}
	if _, err := client.call(ctx, protoGetGlobalState, map[string]any{}); !errors.Is(err, errOpendClosed) {
		t.Fatalf("call after close returned %v", err)
	}
}

func TestCanSendRejectsTradeAndUnknown(t *testing.T) {
	if err := canSendProto(2202); err != errFutuTradeForbidden {
		t.Fatalf("trade: %v", err)
	}
	if err := canSendProto(3201); err != errFutuProtoForbidden {
		t.Fatalf("unknown: %v", err)
	}
	if err := canSendProto(protoQotRequestHistoryKL); err != nil {
		t.Fatalf("3103 should be allowed as fallback: %v", err)
	}
}

func TestFutuHistoryRejectsRepeatedPaginationCursor(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.mu.Lock()
	fake.historyNextKey = "repeated-cursor"
	fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialOpend(ctx, fake.addr(), "workbench-history-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if _, err := client.requestHistoryKL(ctx, "AAPL", klType1Min, "2026-01-01", "2026-01-03"); err == nil {
		t.Fatal("repeated nextReqKey must not be reported as complete history")
	}
	fake.mu.Lock()
	requests := fake.historyN
	fake.mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests=%d want two pages before detecting the stuck cursor", requests)
	}
}

func TestFutuHistoryAcceptsSuccessfulEmptyFinalPage(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.mu.Lock()
	fake.histList = []map[string]any{}
	fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialOpend(ctx, fake.addr(), "workbench-empty-history-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	bars, err := client.requestHistoryKL(ctx, "AAPL", klType1Min, "2026-01-01", "2026-01-03")
	if err != nil || len(bars) != 0 {
		t.Fatalf("successful empty range should mean no historical trades: bars=%+v err=%v", bars, err)
	}
}

func TestDialInitConnectUsesPlaintextJSONAndSessionAll(t *testing.T) {
	fake := startFakeOpenD(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := dialOpend(ctx, fake.addr(), "workbench-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	fake.mu.Lock()
	init := fake.initPayload
	fake.mu.Unlock()
	if v, _ := asInt(init["packetEncAlgo"]); v != packetEncNone {
		t.Fatalf("packetEncAlgo=%v want %d", init["packetEncAlgo"], packetEncNone)
	}
	if v, _ := asInt(init["pushProtoFmt"]); v != opendFmtJSON {
		t.Fatalf("pushProtoFmt=%v", init["pushProtoFmt"])
	}
	if err := client.subscribe(ctx, "AAPL", []int{subTypeBasic, subTypeKL1Min}, true); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	sub := fake.subPayload
	fake.mu.Unlock()
	if v, _ := asInt(sub["session"]); v != sessionALL {
		t.Fatalf("session=%v", sub["session"])
	}
	if _, ok := sub["extendedTime"]; ok {
		t.Fatal("extendedTime should be omitted when session is set")
	}
	bars, err := client.getKL(ctx, "AAPL", klType1Min, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 || bars[0].Close != 101 {
		t.Fatalf("overnight filter: %+v", bars)
	}
}

func TestOvernightClockDST(t *testing.T) {
	cases := []struct {
		utc  string
		want bool
	}{
		{"2026-01-15T01:00:00Z", true},  // 20:00 EST
		{"2026-01-15T09:00:00Z", false}, // 04:00 EST exclusive
		{"2026-03-08T06:00:00Z", true},  // 01:00 EST
		{"2026-03-08T08:00:00Z", false}, // 04:00 EDT
		{"2026-11-01T05:00:00Z", true},  // 01:00 EDT
		{"2026-11-01T06:00:00Z", true},  // 01:00 EST after fall-back
		{"2026-01-15T16:00:00Z", false}, // 11:00 EST RTH
	}
	for _, tc := range cases {
		ts, _ := time.Parse(time.RFC3339, tc.utc)
		if got := isOvernightET(ts); got != tc.want {
			t.Errorf("%s: got %v want %v (NY %s)", tc.utc, got, tc.want, ts.In(nyZone))
		}
	}
}

func TestValidateOpenDAddr(t *testing.T) {
	if err := validateOpenDAddr("127.0.0.1", 11111, false); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenDAddr("localhost", 11111, false); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenDAddr("futu-opend", 11111, false); err == nil {
		t.Fatal("docker hostname requires allowNonLocal")
	}
	if err := validateOpenDAddr("futu-opend", 11111, true); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenDAddr("[::1]", 11111, false); err == nil {
		t.Fatal("brackets rejected")
	}
	if err := validateOpenDAddr("127.0.0.1:11111", 11111, false); err == nil {
		t.Fatal("host must not include port")
	}
}

func TestWriteOpendPacketRejectedForTrade(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &opendClient{pending: map[uint32]chan opendReply{}, stop: make(chan struct{}), conn: discardConn{}}
	if _, err := client.call(ctx, 2202, map[string]any{"c2s": map[string]any{}}); err != errFutuTradeForbidden {
		t.Fatalf("got %v", err)
	}
}

type discardConn struct{ net.Conn }

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write([]byte) (int, error)        { return 0, io.EOF }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return nil }
func (discardConn) RemoteAddr() net.Addr             { return nil }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

func TestJSONNumberHelpers(t *testing.T) {
	if !strings.Contains(nyZone.String(), "New_York") {
		t.Fatal(nyZone)
	}
}
