package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Preset is a console's OSC dialect.
type Preset struct {
	Port        int    `json:"port"`
	MuteAddress string `json:"muteAddress"` // {n} = channel, {nn} = 2-digit
	MutedValue  int    `json:"mutedValue"`  // value meaning "muted"
	Probe       string `json:"probe"`
	MaxChannel  int    `json:"maxChannel"`
}

var Presets = map[string]Preset{
	// The WING's mute address in the generic driver. WING connections
	// use the full client (wing.go); this entry only keeps the generic
	// driver's WING dialect (3-value replies) under test.
	"wing": {Port: 2223, MuteAddress: "/ch/{n}/mute", MutedValue: 1, Probe: "/?", MaxChannel: 48},
	// X32/M32 OSC (unofficial X32 OSC protocol doc). /mix/on is "on",
	// so 0 = muted. Untested against a real desk — experimental.
	"x32": {Port: 10023, MuteAddress: "/ch/{nn}/mix/on", MutedValue: 0, Probe: "/info", MaxChannel: 32},
}

type Mixer struct {
	addr    string
	p       Preset
	timeout time.Duration
}

func New(addr string, p Preset) *Mixer {
	return &Mixer{addr: addr, p: p, timeout: 600 * time.Millisecond}
}

func (m *Mixer) MaxChannel() int { return m.p.MaxChannel }

func (m *Mixer) muteAddr(ch int) string {
	a := strings.ReplaceAll(m.p.MuteAddress, "{nn}", fmt.Sprintf("%02d", ch))
	return strings.ReplaceAll(a, "{n}", strconv.Itoa(ch))
}

// exchange sends one message on a fresh UDP socket (consoles reply to
// the sender's port) and optionally waits for the reply.
func (m *Mixer) exchange(ctx context.Context, msg []byte, wantReply bool) (string, []any, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", m.addr)
	if err != nil {
		return "", nil, fmt.Errorf("mixer unreachable: %w", err)
	}
	defer c.Close()
	dl := time.Now().Add(m.timeout)
	if t, ok := ctx.Deadline(); ok && t.Before(dl) {
		dl = t
	}
	c.SetDeadline(dl)
	if _, err := c.Write(msg); err != nil {
		return "", nil, fmt.Errorf("mixer unreachable: %w", err)
	}
	if !wantReply {
		return "", nil, nil
	}
	buf := make([]byte, 8192)
	n, err := c.Read(buf)
	if err != nil {
		return "", nil, fmt.Errorf("mixer at %s did not answer", m.addr)
	}
	return Decode(buf[:n])
}

func (m *Mixer) Probe(ctx context.Context) (string, error) {
	msg, _ := Encode(m.p.Probe)
	addr, vals, err := m.exchange(ctx, msg, true)
	if err != nil {
		return "", err
	}
	if addr != m.p.Probe {
		return "", fmt.Errorf("unexpected reply %q to the mixer probe", addr)
	}
	var parts []string
	for _, v := range vals {
		parts = append(parts, fmt.Sprint(v))
	}
	return strings.Join(parts, " "), nil
}

func (m *Mixer) check(ch int) error {
	if ch < 1 || ch > m.p.MaxChannel {
		return fmt.Errorf("channel %d is outside 1..%d", ch, m.p.MaxChannel)
	}
	return nil
}

func (m *Mixer) GetMute(ctx context.Context, ch int) (bool, error) {
	if err := m.check(ch); err != nil {
		return false, err
	}
	a := m.muteAddr(ch)
	msg, _ := Encode(a)
	addr, vals, err := m.exchange(ctx, msg, true)
	if err != nil {
		return false, err
	}
	if addr != a || len(vals) == 0 {
		return false, fmt.Errorf("unexpected mute reply for channel %d", ch)
	}
	v, ok := oscInt(vals)
	if !ok {
		return false, fmt.Errorf("unexpected mute value type for channel %d", ch)
	}
	return v == m.p.MutedValue, nil
}

func (m *Mixer) SetMute(ctx context.Context, ch int, muted bool) error {
	if err := m.check(ch); err != nil {
		return err
	}
	v := 1 - m.p.MutedValue // "live" value
	if muted {
		v = m.p.MutedValue
	}
	msg, _ := Encode(m.muteAddr(ch), int32(v))
	_, _, err := m.exchange(ctx, msg, false) // OSC set is fire-and-forget; callers read back
	return err
}

// oscInt reads a whole-number reply. An X32 answers one int; a WING
// answers the same value three ways (string "1", float 1.0, int 1), the
// string first. The int is preferred, then the float, then the text.
func oscInt(vals []any) (int, bool) {
	for _, v := range vals {
		if x, ok := v.(int32); ok {
			return int(x), true
		}
	}
	for _, v := range vals {
		if x, ok := v.(float32); ok {
			return int(x + 0.5), true
		}
	}
	for _, v := range vals {
		if x, ok := v.(string); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
