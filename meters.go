package main

// Meters (WING Remote Protocols 3.1 p.98-99): the add-on asks on the
// native link's channel 3 for a set of strips, and the console sends the
// levels as UDP datagrams to a port of the add-on's choosing:
//
//	<report id: 4 bytes> then int16 words (big endian), in request order
//	V2 strips (ch aux bus main mtx): in L, in R, out L, out R, gate key,
//	  gate gain, dyn key, dyn gain, gate LED, dyn state, automix (11 words)
//	DCA: pre L, pre R, post L, post R (4 words)
//
// Levels are 1/256 dB. The request lapses after 5 s unless the report id
// is sent again, so the stream stops by itself once no widget asks.
// Meters run only while a widget polls them (resource "live"/"meters"),
// and stop 10 s after the last poll.

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"time"
)

// meterIdle: stop this long after the last poll (a var for tests).
var meterIdle = 10 * time.Second

const (
	meterRenewEvery = 3 * time.Second // resend the report id (console: < 5 s)
	maxMeters       = 64
)

type meterVal struct {
	inL, inR, outL, outR float64 // dB
	gate, dyn            bool
	gr                   float64 // dyn gain reduction, dB (approximate)
	at                   time.Time
}

type meterState struct {
	mu       sync.Mutex
	demand   map[stripRef]time.Time
	pc       net.PacketConn
	port     int
	id       uint32
	strips   []stripRef // current subscription, in request order
	sentAt   time.Time
	needSend bool
	vals     map[stripRef]meterVal
	packets  int
	firstPkt time.Time
	lastPkt  time.Time
	note     string
}

// want records that a widget is showing these strips' meters.
func (m *meterState) want(strips []stripRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.demand == nil {
		m.demand = map[stripRef]time.Time{}
	}
	now := time.Now()
	for _, s := range strips {
		if _, ok := meterFamily[s.Kind]; ok {
			m.demand[s] = now
		}
	}
}

// resend: the native link reconnected, the request must go again.
func (m *meterState) resend() {
	m.mu.Lock()
	m.needSend = true
	m.mu.Unlock()
}

func (m *meterState) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pc != nil {
		m.pc.Close()
		m.pc = nil
	}
}

// running reports whether levels are arriving.
func (m *meterState) running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.strips) > 0 && time.Since(m.lastPkt) < 2*time.Second
}

func (m *meterState) get(s stripRef) (meterVal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vals[s]
	if !ok || time.Since(v.at) > 2*time.Second {
		return meterVal{}, false
	}
	return v, true
}

// meterLoop starts, changes, renews and stops the console's meter stream
// to match what widgets poll.
func (w *Wing) meterLoop(ctx context.Context) {
	m := &w.meters
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.strips = nil
			m.mu.Unlock()
			return
		case <-t.C:
		}
		if w.cfg.Meters == "off" {
			continue
		}
		m.mu.Lock()
		var want []stripRef
		for s, at := range m.demand {
			if time.Since(at) > meterIdle {
				delete(m.demand, s)
				continue
			}
			want = append(want, s)
		}
		sortStrips(want)
		if len(want) > maxMeters {
			want = want[:maxMeters]
		}
		if len(want) == 0 {
			// Nobody is looking: stop renewing; the console stops in 5 s.
			m.strips = nil
			m.mu.Unlock()
			continue
		}
		if m.pc == nil {
			pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(w.cfg.MeterPort))
			if err != nil {
				m.note = "can't open a UDP port for meters: " + err.Error()
				m.mu.Unlock()
				continue
			}
			m.pc = pc
			m.port = pc.LocalAddr().(*net.UDPAddr).Port
			go w.meterRead(pc)
		}
		changed := !sameStrips(want, m.strips)
		var payload []byte
		switch {
		case changed || m.needSend:
			m.id++
			m.strips = want
			m.firstPkt, m.packets = time.Time{}, 0
			payload = meterRequest(m.id, m.port, want)
		case time.Since(m.sentAt) >= meterRenewEvery:
			payload = meterRenew(m.id)
		}
		m.mu.Unlock()
		if payload == nil {
			continue
		}
		err := w.nativeSend(chanMeters, payload)
		m.mu.Lock()
		if err != nil {
			m.needSend = true
			m.note = "meters need the console's native port (TCP 2222)"
		} else {
			m.needSend, m.sentAt = false, time.Now()
			if time.Since(m.lastPkt) > 3*time.Second && !m.sentAt.IsZero() && m.packets == 0 && m.firstPkt.IsZero() {
				m.note = "asked the console for meters; waiting for them on UDP port " + strconv.Itoa(m.port)
			}
		}
		m.mu.Unlock()
	}
}

func sameStrips(a, b []stripRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// meterRead receives meter datagrams from the console.
func (w *Wing) meterRead(pc net.PacketConn) {
	m := &w.meters
	buf := make([]byte, 8192)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if ua, ok := from.(*net.UDPAddr); ok {
			if ip := net.ParseIP(w.cfg.Host); ip != nil && !ip.IsLoopback() && !ua.IP.Equal(ip) {
				continue // only the console's own datagrams
			}
		}
		m.parse(buf[:n])
	}
}

func (m *meterState) parse(b []byte) {
	if len(b) < 4 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if binary.BigEndian.Uint32(b) != m.id || len(m.strips) == 0 {
		return
	}
	now := time.Now()
	if m.vals == nil {
		m.vals = map[stripRef]meterVal{}
	}
	off := 4
	word := func(i int) float64 { return float64(int16(binary.BigEndian.Uint16(b[off+2*i:]))) }
	for _, s := range m.strips {
		nw := meterWords(s)
		if off+2*nw > len(b) {
			break
		}
		v := meterVal{at: now,
			inL: word(0) / 256, inR: word(1) / 256, outL: word(2) / 256, outR: word(3) / 256}
		if nw >= 11 {
			v.gr = -word(7) * 20 / 256 // 256 ≈ 20 dB of gain reduction (most models)
			if v.gr < 0 {
				v.gr = -v.gr
			}
			v.gate = word(8) != 0
			v.dyn = word(9) != 0
		}
		m.vals[s] = v
		off += 2 * nw
	}
	if m.firstPkt.IsZero() {
		m.firstPkt = now
	}
	m.packets++
	m.lastPkt = now
	m.note = ""
}

// rateHz is the measured meter update rate.
func (m *meterState) rateHz() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.lastPkt.Sub(m.firstPkt).Seconds()
	if m.packets < 2 || d <= 0 {
		return 0
	}
	return float64(m.packets-1) / d
}
