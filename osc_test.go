package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

func TestCodecRoundTrip(t *testing.T) {
	b, err := Encode("/ch/13/mute", int32(1), float32(0.5), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(b)%4 != 0 {
		t.Fatal("not 4-aligned")
	}
	a, v, err := Decode(b)
	if err != nil || a != "/ch/13/mute" || v[0] != int32(1) || v[1] != float32(0.5) || v[2] != "hi" {
		t.Fatalf("%q %v %v", a, v, err)
	}
	// Known bytes: "/?" + ",\0\0\0"
	p, _ := Encode("/?")
	if !bytes.Equal(p, []byte{'/', '?', 0, 0, ',', 0, 0, 0}) {
		t.Fatalf("% x", p)
	}
	for _, bad := range [][]byte{nil, []byte("x"), []byte("/abc"), append(oscString("/a"), oscString(",i")...)} {
		if _, _, err := Decode(bad); err == nil {
			t.Errorf("accepted % x", bad)
		}
	}
}

// fakeConsole is a real UDP OSC responder speaking a preset's dialect.
func fakeConsole(t *testing.T, p Preset, probeReply string) (string, func() map[int]int) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	var mu sync.Mutex
	state := map[int]int{}
	m := New("", p)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			addr, vals, err := Decode(buf[:n])
			if err != nil {
				continue
			}
			if addr == p.Probe {
				r, _ := Encode(addr, probeReply)
				pc.WriteTo(r, from)
				continue
			}
			for ch := 1; ch <= p.MaxChannel; ch++ {
				if m.muteAddr(ch) != addr {
					continue
				}
				mu.Lock()
				if len(vals) > 0 {
					state[ch] = int(vals[0].(int32))
				} else {
					v, ok := state[ch]
					if !ok {
						v = 1 - p.MutedValue // live by default
					}
					r, _ := Encode(addr, int32(v))
					pc.WriteTo(r, from)
				}
				mu.Unlock()
			}
		}
	}()
	return pc.LocalAddr().String(), func() map[int]int {
		mu.Lock()
		defer mu.Unlock()
		c := map[int]int{}
		for k, v := range state {
			c[k] = v
		}
		return c
	}
}

func TestPresets(t *testing.T) {
	for name, p := range Presets {
		t.Run(name, func(t *testing.T) {
			addr, state := fakeConsole(t, p, name+" console")
			m := New(addr, p)
			ctx := context.Background()
			id, err := m.Probe(ctx)
			if err != nil || !strings.Contains(id, name) {
				t.Fatalf("probe %q %v", id, err)
			}
			if muted, err := m.GetMute(ctx, 7); err != nil || muted {
				t.Fatalf("default should be live: %v %v", muted, err)
			}
			if err := m.SetMute(ctx, 7, true); err != nil {
				t.Fatal(err)
			}
			// wire value follows the preset's polarity (UDP set is async)
			got, seen := 0, false
			for i := 0; i < 100 && !seen; i++ {
				got, seen = state()[7]
				if !seen {
					time.Sleep(5 * time.Millisecond)
				}
			}
			if got != p.MutedValue {
				t.Fatalf("wire value %d want %d", got, p.MutedValue)
			}
			waitMuted := false
			for i := 0; i < 20 && !waitMuted; i++ {
				waitMuted, _ = m.GetMute(ctx, 7)
			}
			if !waitMuted {
				t.Fatal("read-back not muted")
			}
			if err := m.SetMute(ctx, p.MaxChannel+1, true); err == nil {
				t.Fatal("out-of-range channel accepted")
			}
		})
	}
}

func TestX32Addressing(t *testing.T) {
	if a := New("", Presets["x32"]).muteAddr(5); a != "/ch/05/mix/on" {
		t.Fatal(a)
	}
	if a := New("", Presets["wing"]).muteAddr(5); a != "/ch/5/mute" {
		t.Fatal(a)
	}
}

func TestFactory(t *testing.T) {
	f := func(js string) (addon.Mixer, error) {
		m, err := openMixer(json.RawMessage(js))
		if err != nil {
			return nil, err
		}
		return m.(addon.Mixer), nil
	}
	if _, err := f(`{"driver":"osc","preset":"wing"}`); err == nil {
		t.Fatal("missing host accepted")
	}
	m, err := f(`{"driver":"osc","preset":"x32","host":"1.2.3.4","maxChannel":16}`)
	if err != nil || m.MaxChannel() != 16 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := f(`{"driver":"osc","preset":"nope","host":"h"}`); err == nil {
		t.Fatal("unknown preset accepted")
	}
}
