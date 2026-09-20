package investment

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

const opendHeaderSize = 44

const opendFmtJSON = 1

const protoInitConnect uint32 = 1001

const protoGetGlobalState uint32 = 1002

const protoNotify uint32 = 1003

const protoKeepAlive uint32 = 1004

const protoQotSub uint32 = 3001

const protoQotGetSubInfo uint32 = 3003

const protoQotGetBasicQot uint32 = 3004

const protoQotUpdateBasicQot uint32 = 3005

const protoQotGetKL uint32 = 3006

const protoQotUpdateKL uint32 = 3007

const protoQotRequestHistoryKL uint32 = 3103

const protoQotRequestHistoryKLQuota uint32 = 3104

const retSucceed = 0

const packetEncNone = -1

const sessionALL = 3

const marketUS = 11

const rehabNone = 0

const subTypeBasic = 1

const klType1Min = 1

const klType5Min = 6

const klType15Min = 7

const klType30Min = 8

const subTypeKL1Min = 11

const subTypeKL5Min = 7

const subTypeKL15Min = 8

const subTypeKL30Min = 9

const opendClientVer = 300

const maxOpendBody = 4 << 20

var errFutuTradeForbidden = errors.New("富途牛牛交易协议已禁用")

var errFutuProtoForbidden = errors.New("不允许的 OpenD 协议")

var errOpendClosed = errors.New("OpenD 连接已关闭")

var allowedSend = map[uint32]struct{}{
	protoInitConnect: {}, protoGetGlobalState: {}, protoKeepAlive: {},
	protoQotSub: {}, protoQotGetSubInfo: {}, protoQotGetBasicQot: {}, protoQotGetKL: {},
	protoQotRequestHistoryKL: {}, protoQotRequestHistoryKLQuota: {},
}

func canSendProto(id uint32) error {
	if id >= 2000 && id < 3000 {
		return errFutuTradeForbidden
	}
	if _, ok := allowedSend[id]; !ok {
		return errFutuProtoForbidden
	}
	return nil
}

type opendReply struct {
	proto uint32
	body  []byte
}

type opendClient struct {
	conn    net.Conn
	mu      sync.Mutex
	serial  uint32
	pending map[uint32]chan opendReply
	onKL    func(symbol, resolution string, bar futuBar)
	onQuote func(futuOvernightQuote)
	closed  bool
	stop    chan struct{}
}

func dialOpend(ctx context.Context, addr, clientID string) (*opendClient, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	client := &opendClient{conn: conn, pending: make(map[uint32]chan opendReply), stop: make(chan struct{})}
	go client.readLoop()
	s2c, err := client.call(ctx, protoInitConnect, map[string]any{"c2s": map[string]any{
		"clientVer": opendClientVer, "clientID": clientID, "recvNotify": true,
		"packetEncAlgo": packetEncNone, "pushProtoFmt": opendFmtJSON, "programmingLanguage": "Go",
	}})
	if err != nil {
		client.close()
		return nil, err
	}
	interval := 10
	if v, ok := asInt(s2c["keepAliveInterval"]); ok && v > 0 {
		interval = v
	}
	go client.keepAlive(interval)
	return client, nil
}

func (c *opendClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.stop)
	_ = c.conn.Close()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *opendClient) keepAlive(intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = c.call(ctx, protoKeepAlive, map[string]any{"c2s": map[string]any{"time": time.Now().Unix()}})
			cancel()
		}
	}
}

func (c *opendClient) call(ctx context.Context, proto uint32, req any) (map[string]any, error) {
	if err := canSendProto(proto); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan opendReply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errOpendClosed
	}
	c.serial++
	serial := c.serial
	if err := writeOpendPacket(c.conn, proto, serial, payload); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.pending[serial] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, serial)
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.stop:
		return nil, errOpendClosed
	case reply, ok := <-ch:
		if !ok {
			return nil, errOpendClosed
		}
		return parseFutuS2C(reply.body)
	}
}

func writeOpendPacket(conn net.Conn, proto, serial uint32, payload []byte) error {
	if len(payload) > maxOpendBody {
		return errors.New("OpenD 请求过大")
	}
	var hdr [opendHeaderSize]byte
	hdr[0], hdr[1] = 'F', 'T'
	binary.LittleEndian.PutUint32(hdr[2:], proto)
	hdr[6] = opendFmtJSON
	hdr[7] = 0
	binary.LittleEndian.PutUint32(hdr[8:], serial)
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(payload)))
	sum := sha1.Sum(payload)
	copy(hdr[16:36], sum[:])
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func readOpendPacket(conn net.Conn) (proto, serial uint32, body []byte, err error) {
	var hdr [opendHeaderSize]byte
	if _, err = io.ReadFull(conn, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	if hdr[0] != 'F' || hdr[1] != 'T' {
		return 0, 0, nil, errors.New("OpenD 协议头无效")
	}
	proto = binary.LittleEndian.Uint32(hdr[2:])
	serial = binary.LittleEndian.Uint32(hdr[8:])
	n := binary.LittleEndian.Uint32(hdr[12:])
	if n > maxOpendBody {
		return 0, 0, nil, errors.New("OpenD 包体过大")
	}
	body = make([]byte, n)
	_, err = io.ReadFull(conn, body)
	return proto, serial, body, err
}

func (c *opendClient) readLoop() {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		proto, serial, body, err := readOpendPacket(c.conn)
		if err != nil {
			c.close()
			return
		}
		switch proto {
		case protoQotUpdateKL:
			c.dispatchKL(body)
		case protoQotUpdateBasicQot:
			c.dispatchQuote(body)
		case protoNotify:
			// Quote-only: ignore trade-unlock and other notifications.
		default:
			c.mu.Lock()
			ch := c.pending[serial]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- opendReply{proto: proto, body: body}:
				default:
				}
			}
		}
	}
}

func parseFutuS2C(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var envelope map[string]any
	if err := dec.Decode(&envelope); err != nil {
		return nil, err
	}
	ret, _ := asInt(envelope["retType"])
	if ret != retSucceed {
		msg, _ := envelope["retMsg"].(string)
		if msg == "" {
			msg = "OpenD 请求失败"
		}
		return nil, fmt.Errorf("%s（%d）", msg, ret)
	}
	s2c, _ := envelope["s2c"].(map[string]any)
	if s2c == nil {
		s2c = map[string]any{}
	}
	return s2c, nil
}
