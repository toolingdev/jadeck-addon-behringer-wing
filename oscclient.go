package main

// oscClient talks OSC to the WING on UDP 2223 over one socket, so many
// reads can be in flight at once (replies are matched by address). WING
// Remote Protocols 3.1 p.19-25:
//
//   - a GET is the address alone; the reply carries the same address with
//     ,sff (text, normalized, engineering) for floats, ,sfi for ints, ,s
//     for strings and enums;
//   - a SET is the address with one argument; the console doesn't echo it,
//     and an error comes back as "/*" (or "<address>*") ,s "<error>";
//   - "<node> ,b dd" returns the node's children definitions (with their
//     hashes) as a blob.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// oscReply is one message from the console.
type oscReply struct {
	addr string
	vals []any
}

type oscClient struct {
	conn    *net.UDPConn
	timeout time.Duration

	mu      sync.Mutex
	waiters map[string][]chan oscReply
	errs    []string // "/*" replies not matched to an address (newest last)
	errSeq  int
	closed  bool

	sem chan struct{} // reads in flight
}

func dialOSC(addr string) (*oscClient, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	c, err := net.DialUDP("udp", nil, ua)
	if err != nil {
		return nil, err
	}
	o := &oscClient{conn: c, timeout: 500 * time.Millisecond, waiters: map[string][]chan oscReply{}, sem: make(chan struct{}, 24)}
	go o.read()
	return o, nil
}

func (o *oscClient) Close() error {
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	return o.conn.Close()
}

func (o *oscClient) read() {
	buf := make([]byte, 65536)
	for {
		n, err := o.conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			o.mu.Lock()
			closed := o.closed
			o.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			// ICMP "port unreachable" surfaces here on some systems: keep reading.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		addr, vals, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		o.deliver(oscReply{addr, vals})
	}
}

func (o *oscClient) deliver(r oscReply) {
	o.mu.Lock()
	defer o.mu.Unlock()
	key := r.addr
	if strings.HasSuffix(key, "*") { // "/*" or "<address>*": a status / error
		key = strings.TrimSuffix(key, "*")
		if key == "/" || key == "" {
			if s := firstString(r.vals); s != "" && s != "OK" {
				o.errs = append(o.errs, s)
				if len(o.errs) > 16 {
					o.errs = o.errs[len(o.errs)-16:]
				}
				o.errSeq++
			}
			return
		}
		r.addr = key + "*"
	}
	ws := o.waiters[key]
	if len(ws) == 0 {
		return
	}
	ch := ws[0]
	if len(ws) == 1 {
		delete(o.waiters, key)
	} else {
		o.waiters[key] = ws[1:]
	}
	ch <- r
}

func (o *oscClient) wait(key string) (chan oscReply, func()) {
	ch := make(chan oscReply, 1)
	o.mu.Lock()
	o.waiters[key] = append(o.waiters[key], ch)
	o.mu.Unlock()
	return ch, func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		ws := o.waiters[key]
		for i, w := range ws {
			if w == ch {
				o.waiters[key] = append(ws[:i:i], ws[i+1:]...)
				break
			}
		}
		if len(o.waiters[key]) == 0 {
			delete(o.waiters, key)
		}
	}
}

// errMark returns the count of unmatched errors so far; errSince returns
// those that came after a mark.
func (o *oscClient) errMark() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.errSeq
}

func (o *oscClient) errSince(mark int) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := o.errSeq - mark
	if n <= 0 {
		return ""
	}
	if n > len(o.errs) {
		n = len(o.errs)
	}
	return strings.Join(o.errs[len(o.errs)-n:], "; ")
}

// errNoAnswer: the console didn't reply in time.
var errNoAnswer = errors.New("no answer")

// request sends msg and waits for the reply addressed to key. One retry
// (UDP can drop).
func (o *oscClient) request(ctx context.Context, key string, msg []byte) (oscReply, error) {
	select {
	case o.sem <- struct{}{}:
	case <-ctx.Done():
		return oscReply{}, ctx.Err()
	}
	defer func() { <-o.sem }()
	mark := o.errMark()
	for try := 0; try < 2; try++ {
		ch, cancel := o.wait(key)
		if _, err := o.conn.Write(msg); err != nil {
			cancel()
			return oscReply{}, fmt.Errorf("console unreachable: %w", err)
		}
		t := time.NewTimer(o.timeout)
		select {
		case r := <-ch:
			t.Stop()
			if strings.HasSuffix(r.addr, "*") {
				return r, consoleError(firstString(r.vals))
			}
			return r, nil
		case <-t.C:
			cancel()
		case <-ctx.Done():
			t.Stop()
			cancel()
			return oscReply{}, ctx.Err()
		}
	}
	if e := o.errSince(mark); e != "" {
		return oscReply{}, consoleError(e)
	}
	return oscReply{}, errNoAnswer
}

// consoleError is an error text the console sent back.
type consoleError string

func (e consoleError) Error() string { return "the console said: " + string(e) }

// get reads one parameter.
func (o *oscClient) get(ctx context.Context, path string) (value, error) {
	msg, err := Encode(path)
	if err != nil {
		return value{}, err
	}
	r, err := o.request(ctx, path, msg)
	if err != nil {
		return value{}, err
	}
	return valueFromOSC(r.vals)
}

// dd reads a node's children definitions.
func (o *oscClient) dd(ctx context.Context, path string) ([]nodeDef, error) {
	msg, err := Encode(path, []byte{tokDefReq})
	if err != nil {
		return nil, err
	}
	r, err := o.request(ctx, path, msg)
	if err != nil {
		return nil, err
	}
	for _, v := range r.vals {
		if b, ok := v.([]byte); ok {
			return parseDefs(b)
		}
	}
	if s := firstString(r.vals); s != "" {
		return nil, consoleError(s)
	}
	return nil, errors.New("no definitions in the reply")
}

// raw sends a message and returns the raw reply (the probe).
func (o *oscClient) raw(ctx context.Context, path string, args ...any) (oscReply, error) {
	msg, err := Encode(path, args...)
	if err != nil {
		return oscReply{}, err
	}
	return o.request(ctx, path, msg)
}

// set writes one parameter (no reply on success).
func (o *oscClient) set(path string, arg any) error {
	msg, err := Encode(path, arg)
	if err != nil {
		return err
	}
	if _, err := o.conn.Write(msg); err != nil {
		return fmt.Errorf("console unreachable: %w", err)
	}
	return nil
}

// ------------------------------------------------------------- values

// value is one parameter's value as the add-on keeps it.
type value struct {
	kind byte    // 'i' int, 'f' float, 's' string
	s    string  // text (the console's display text for numbers)
	f    float64 // engineering value (dB, Hz, s) or the int
	norm float64 // normalized 0..1, -1 when unknown
	at   time.Time
}

func (v value) Int() int { return int(v.f) }

// valueFromOSC reads a GET reply: ,sff / ,sfi triplets (WING), a single
// ,i / ,f / ,s (other shapes, subscription events).
func valueFromOSC(vals []any) (value, error) {
	v := value{norm: -1, at: time.Now()}
	if len(vals) == 0 {
		return v, errors.New("empty reply")
	}
	if len(vals) >= 3 {
		s, _ := vals[0].(string)
		n, okn := vals[1].(float32)
		v.s = s
		if okn {
			v.norm = float64(n)
		}
		switch x := vals[2].(type) {
		case int32:
			v.kind, v.f = 'i', float64(x)
			return v, nil
		case float32:
			v.kind, v.f = 'f', float64(x)
			return v, nil
		}
	}
	// One value (or an odd shape): use what's there, preferring the
	// typed number (oscInt's rule: int, then float, then text).
	if i, ok := firstOf[int32](vals); ok {
		v.kind, v.f = 'i', float64(i)
		if v.s == "" {
			v.s = strconv.Itoa(int(i))
		}
		return v, nil
	}
	if f, ok := firstOf[float32](vals); ok && len(vals) < 3 {
		v.kind, v.f = 'f', float64(f)
		if v.s == "" {
			v.s = trimFloat(float64(f))
		}
		return v, nil
	}
	if s, ok := firstOf[string](vals); ok {
		v.kind, v.s = 's', s
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			v.f = f
		}
		return v, nil
	}
	return v, errors.New("unexpected reply")
}

func firstOf[T any](vals []any) (T, bool) {
	for _, v := range vals {
		if x, ok := v.(T); ok {
			return x, true
		}
	}
	var z T
	return z, false
}

func firstString(vals []any) string {
	s, _ := firstOf[string](vals)
	return s
}
