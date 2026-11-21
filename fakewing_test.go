package main

// fakeWing is a WING as WING Remote Protocols 3.1 describes it, for the
// tests: OSC on UDP (triplet replies, "/*" errors, ,b dd definitions,
// silent drops under the OSC lock), the native protocol on TCP (framing,
// pushed D7 events to every client, keep-alive replies, at most 24
// clients), meters on channel 3 sent as UDP, and UDP discovery.
// Hashes are made up (crc32 of the path) so the add-on must learn them.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fparam struct {
	typ   byte // 'i' int, 'f' float, 'l' log float, 's' string, 'e' enum
	i     int
	f     float64
	s     string
	min   float64
	max   float64
	items []string
	ro    bool
	hash  uint32
	fader bool
}

type fclient struct {
	conn net.Conn
	enc  *nrpEncoder
	mu   sync.Mutex
	// meters
	port     int
	id       uint32
	strips   []stripRef
	renewed  time.Time
	requests int
}

type fakeWing struct {
	t      *testing.T
	mu     sync.Mutex
	params map[string]*fparam
	byHash map[uint32]string
	nodes  map[string][]string // node -> child names, in order

	osc  net.PacketConn
	tcp  net.Listener
	disc net.PacketConn
	mpc  net.PacketConn // sends meter datagrams

	clients   []*fclient
	sets      int    // writes received over OSC (accepted or not)
	failNext  string // next set answers "/*" with this error
	dropSets  bool   // ignore sets without saying so (like a lock)
	maxClient int
	meterDB   float64
}

func newFakeWing(t *testing.T) *fakeWing {
	f := &fakeWing{t: t, params: map[string]*fparam{}, byHash: map[uint32]string{}, nodes: map[string][]string{}, maxClient: 24, meterDB: -20}
	f.build()
	var err error
	if f.osc, err = net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if f.tcp, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if f.disc, err = net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if f.mpc, err = net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		f.osc.Close()
		f.tcp.Close()
		f.disc.Close()
		f.mpc.Close()
		f.mu.Lock()
		for _, c := range f.clients {
			c.conn.Close()
		}
		f.mu.Unlock()
	})
	go f.serveOSC()
	go f.serveTCP()
	go f.serveDiscovery()
	go f.sendMeters(done)
	return f
}

func (f *fakeWing) oscPort() int    { return f.osc.LocalAddr().(*net.UDPAddr).Port }
func (f *fakeWing) nativePort() int { return f.tcp.Addr().(*net.TCPAddr).Port }

func (f *fakeWing) add(path string, p fparam) {
	p.hash = crc32.ChecksumIEEE([]byte(path))
	if path == "/ch/1/mute" {
		p.hash = 0xf50f69f8 // the doc's example
	}
	f.params[path] = &p
	f.byHash[p.hash] = path
	i := strings.LastIndex(path, "/")
	node, name := path[:i], path[i+1:]
	if node == "" {
		node = "/"
	}
	f.nodes[node] = append(f.nodes[node], name)
}

var fakeScenes = []string{"Walk-in", "Worship~Band + choir", "Sermon", "Special", "Walk-out"}

func (f *fakeWing) build() {
	fdr := fparam{typ: 'f', f: -144, min: -144, max: 10, fader: true}
	for _, s := range allStrips() {
		p := s.Path()
		name := ""
		if s == (stripRef{"ch", 1}) {
			name = "Pulpit"
		}
		if s == (stripRef{"mgrp", 1}) {
			name = "Choir"
		}
		f.add(p+"/name", fparam{typ: 's', s: name})
		f.add(p+"/mute", fparam{typ: 'i', min: 0, max: 1})
		if s.Kind == "mgrp" {
			continue
		}
		f.add(p+"/col", fparam{typ: 'i', i: 1 + s.N%18, min: 1, max: 18})
		f.add(p+"/fdr", fdr)
		f.add(p+"/$solo", fparam{typ: 'i', min: 0, max: 1})
		if s.Kind == "dca" {
			continue
		}
		f.add(p+"/$name", fparam{typ: 's', s: name, ro: true})
		f.add(p+"/$col", fparam{typ: 'i', i: 1 + s.N%18, min: 1, max: 18, ro: true})
		f.add(p+"/$mute", fparam{typ: 'i', min: 0, max: 2, ro: true})
		f.add(p+"/$fdr", fparam{typ: 'f', f: -144, min: -144, max: 10, ro: true, fader: true})
		tags := ""
		switch s {
		case stripRef{"ch", 5}, stripRef{"ch", 6}:
			tags = "#M1"
		case stripRef{"ch", 7}, stripRef{"ch", 8}:
			tags = "#D1"
		}
		f.add(p+"/tags", fparam{typ: 's', s: tags})
		f.addProcessing(s)
	}
	for n := 1; n <= 16; n++ {
		node := fmt.Sprintf("/fx/%d", n)
		f.add(node+"/mdl", fparam{typ: 'e', items: []string{"NONE", "EXT", "HALL", "ST-DL"}})
		f.add(node+"/fxmix", fparam{typ: 'f', f: 100, min: 0, max: 100})
		f.add(node+"/$esrc", fparam{typ: 'i', min: 0, max: 400, ro: true})
	}
	f.setModel(1, "HALL")
	f.setModel(2, "ST-DL")
	f.add("/$ctl/osc/ronly", fparam{typ: 'i', min: 0, max: 1})
	f.add("/$syscfg/tcplock", fparam{typ: 'i', min: 0, max: 1})
	f.add("/$syscfg/$firmware", fparam{typ: 's', s: "3.1.2", ro: true})
	f.add("/$ctl/$stat/cnslock", fparam{typ: 's', s: "", ro: true})
	f.add("/$stat/solo", fparam{typ: 'i', min: 0, max: 1, ro: true})
	f.add("/$stat/usbstate", fparam{typ: 'e', items: []string{"-", "ERR", "IDLE", "BUSY"}, i: 2, ro: true})
	f.add("/$stat/usbvolname", fparam{typ: 's', s: "SERMONS", ro: true})
	f.add("/$ctl/lib/$scenes", fparam{typ: 'e', items: fakeScenes, ro: true})
	f.add("/$ctl/lib/$actidx", fparam{typ: 'i', min: 0, max: 16384, ro: true})
	f.add("/$ctl/lib/$active", fparam{typ: 's', ro: true})
	f.add("/$ctl/lib/$actshow", fparam{typ: 's', s: "I:SHOWS/SUNDAY.show", ro: true})
	f.add("/$ctl/lib/$activeid", fparam{typ: 'i', min: 0, max: 128, ro: true})
	f.add("/$ctl/lib/$actionidx", fparam{typ: 'i', min: 0, max: 16384})
	f.add("/$ctl/lib/$action", fparam{typ: 'e', items: []string{"IDLE", "GO", "PREV", "NEXT", "GOPREV", "GONEXT", "GOTAG"}})
	f.add("/rec/$action", fparam{typ: 'e', items: []string{"STOP", "REC", "PAUSE", "NEWFILE"}})
	f.add("/rec/$actstate", fparam{typ: 'e', items: []string{"STOP", "REC", "PAUSE", "ERROR"}, ro: true})
	f.add("/rec/$time", fparam{typ: 'f', min: 0, max: 35999, ro: true})
	f.add("/rec/$actfile", fparam{typ: 's', ro: true})
	f.add("/play/$action", fparam{typ: 'e', items: []string{"IDLE", "STOP", "PLAY", "PAUSE", "NEXT", "PREV", "PLAYFILE"}})
	f.add("/play/$actstate", fparam{typ: 'e', items: []string{"STOP", "PLAY", "PAUSE", "ERROR"}, ro: true})
	f.add("/play/$song", fparam{typ: 's', s: "Prelude", ro: true})
	f.add("/play/$artist", fparam{typ: 's', ro: true})
	f.add("/play/$actfile", fparam{typ: 's', ro: true})
	f.add("/play/$pos", fparam{typ: 'f', min: 0, max: 35999})
	f.add("/play/$total", fparam{typ: 'f', f: 200, min: 0, max: 35999, ro: true})
	for _, b := range []string{"A", "B"} {
		f.add("/cfg/talk/"+b+"/$on", fparam{typ: 'i', min: 0, max: 1})
		f.add("/cfg/talk/"+b+"/mode", fparam{typ: 'e', items: []string{"AUTO", "PUSH", "LATCH"}, i: 2})
	}
	// nodes as children of their parents (so dd on a parent lists them)
	for _, n := range sortedKeys(f.nodes) {
		for p := n; p != "/" && p != ""; {
			i := strings.LastIndex(p, "/")
			parent := p[:i]
			if parent == "" {
				parent = "/"
			}
			name := p[i+1:]
			if !contains(f.nodes[parent], name) {
				f.nodes[parent] = append(f.nodes[parent], name)
			}
			p = parent
		}
	}
}

// addProcessing adds the channel-strip parameters the doc lists for the
// default models (p.47-65): input, filters, gate, EQ, compressor, sends,
// mains, pan, inserts, mono/stereo.
func (f *fakeWing) addProcessing(s stripRef) {
	p := s.Path()
	lin := func(min, max float64, steps int, v float64) fparam { return fparam{typ: 'f', f: v, min: min, max: max} }
	logf := func(min, max float64, v float64) fparam { return fparam{typ: 'l', f: v, min: min, max: max} }
	sw := fparam{typ: 'i', min: 0, max: 1}
	switch s.Kind {
	case "ch", "aux":
		mode := 0 // M
		if s == (stripRef{"ch", 9}) {
			mode = 1 // a stereo source
		}
		f.add(p+"/in/set/$mode", fparam{typ: 'e', items: []string{"M", "ST", "M/S"}, i: mode, ro: true})
		f.add(p+"/in/set/inv", sw)
		f.add(p+"/in/set/trim", lin(-18, 18, 361, 0))
		f.add(p+"/in/set/bal", lin(-9, 9, 181, 0))
		if s.Kind == "ch" {
			f.add(p+"/in/set/$g", lin(-2.5, 45, 20, 20))
		}
		f.add(p+"/in/set/$vph", sw)
		f.add(p+"/in/conn/grp", fparam{typ: 'e', items: []string{"OFF", "LCL", "AUX", "A", "B", "C"}, i: 1})
		f.add(p+"/in/conn/in", fparam{typ: 'i', i: s.N, min: 1, max: 64})
		f.add(p+"/flt/lc", sw)
		f.add(p+"/flt/lcf", logf(20, 2000, 100))
		f.add(p+"/flt/lcs", fparam{typ: 'e', items: []string{"6", "12", "18", "24"}, i: 3})
		f.add(p+"/flt/hc", sw)
		f.add(p+"/flt/hcf", logf(50, 20000, 10000))
	default:
		f.add(p+"/busmono", fparam{typ: 'i', min: 0, max: 1, i: map[bool]int{true: 1}[s == (stripRef{"bus", 2})]})
	}
	if s.Kind == "ch" {
		for k, v := range map[string]fparam{"on": sw, "thr": lin(-80, 0, 161, -40), "range": lin(3, 60, 115, 40), "att": lin(0, 120, 121, 10),
			"hld": lin(0, 200, 200, 10), "rel": logf(4, 4000, 199)} {
			f.add(p+"/gate/"+k, v)
		}
		f.add(p+"/gate/mdl", fparam{typ: 'e', items: []string{"GATE", "DUCK", "E88"}})
	}
	f.add(p+"/eq/on", sw)
	f.add(p+"/eq/mdl", fparam{typ: 'e', items: []string{"STD", "SOUL", "E88"}})
	for _, b := range []string{"l", "1", "2", "3", "4", "h"} {
		f.add(p+"/eq/"+b+"g", lin(-15, 15, 301, 0))
		f.add(p+"/eq/"+b+"f", logf(20, 20000, 1000))
		f.add(p+"/eq/"+b+"q", logf(0.44, 10, 1))
	}
	f.add(p+"/eq/leq", fparam{typ: 'e', items: []string{"PEQ", "SHV"}, i: 1})
	f.add(p+"/eq/heq", fparam{typ: 'e', items: []string{"PEQ", "SHV"}, i: 1})
	f.add(p+"/dyn/on", sw)
	f.add(p+"/dyn/mdl", fparam{typ: 'e', items: []string{"COMP", "EXP", "LA"}})
	f.add(p+"/dyn/thr", lin(-60, 0, 121, -10))
	f.add(p+"/dyn/ratio", fparam{typ: 'e', items: []string{"1.1", "1.5", "2.0", "3.0", "4.0", "10", "100"}, i: 3})
	f.add(p+"/dyn/att", lin(0, 120, 121, 10))
	f.add(p+"/dyn/rel", logf(4, 4000, 100))
	f.add(p+"/dyn/gain", lin(-6, 12, 37, 0))
	if s.Kind == "ch" || s.Kind == "aux" || s.Kind == "bus" {
		for m := 1; m <= 4; m++ {
			n := fmt.Sprintf("%s/main/%d/", p, m)
			f.add(n+"on", fparam{typ: 'i', min: 0, max: 1, i: map[bool]int{true: 1}[m == 1]})
			f.add(n+"lvl", fparam{typ: 'f', f: 0, min: -144, max: 10, fader: true})
			f.add(n+"pre", sw)
		}
		for b := 1; b <= 16; b++ {
			if s.Kind == "bus" && b == s.N {
				continue
			}
			n := fmt.Sprintf("%s/send/%d/", p, b)
			f.add(n+"on", sw)
			f.add(n+"lvl", fparam{typ: 'f', f: -144, min: -144, max: 10, fader: true})
			f.add(n+"mode", fparam{typ: 'e', items: []string{"PRE", "POST", "GRP"}, i: 1})
		}
	}
	if s.Kind != "mtx" {
		for x := 1; x <= 8; x++ {
			n := fmt.Sprintf("%s/send/MX%d/", p, x)
			f.add(n+"on", sw)
			f.add(n+"lvl", fparam{typ: 'f', f: -144, min: -144, max: 10, fader: true})
			f.add(n+"mode", fparam{typ: 'e', items: []string{"PRE", "POST", "GRP"}, i: 1})
		}
	}
	f.add(p+"/pan", lin(-100, 100, 201, 0))
	f.add(p+"/wid", lin(-150, 150, 61, 100))
	for _, ins := range []string{"preins", "postins"} {
		f.add(p+"/"+ins+"/on", sw)
		f.add(p+"/"+ins+"/ins", fparam{typ: 'e', items: []string{"NONE", "FX1", "FX2", "FX3"}})
	}
}

// setModel loads an FX model: its parameters replace the old ones (the
// console's tree changes shape, p.26).
func (f *fakeWing) setModel(slot int, mdl string) {
	node := fmt.Sprintf("/fx/%d", slot)
	keep := map[string]bool{"mdl": true, "fxmix": true, "$esrc": true}
	var kids []string
	for _, k := range f.nodes[node] {
		if keep[k] {
			kids = append(kids, k)
		} else {
			delete(f.byHash, f.params[node+"/"+k].hash)
			delete(f.params, node+"/"+k)
		}
	}
	f.nodes[node] = kids
	p := f.params[node+"/mdl"]
	for i, it := range p.items {
		if it == mdl {
			p.i = i
		}
	}
	switch mdl {
	case "HALL":
		f.add(node+"/pdel", fparam{typ: 'i', i: 20, min: 0, max: 200})
		f.add(node+"/size", fparam{typ: 'i', i: 60, min: 0, max: 100})
		f.add(node+"/dcy", fparam{typ: 'l', f: 1.8, min: 0.2, max: 5})
		f.add(node+"/lc", fparam{typ: 'l', f: 100, min: 20, max: 400})
	case "ST-DL":
		f.add(node+"/time", fparam{typ: 'f', f: 400, min: 1, max: 3000})
		f.add(node+"/feed", fparam{typ: 'f', f: 30, min: 0, max: 100})
	case "EXT":
		f.add(node+"/egrp", fparam{typ: 'e', items: []string{"OFF", "LCL"}})
		f.add(node+"/ein", fparam{typ: 'i', i: 1, min: 1, max: 64})
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- OSC

func (f *fakeWing) serveOSC() {
	buf := make([]byte, 65536)
	for {
		n, from, err := f.osc.ReadFrom(buf)
		if err != nil {
			return
		}
		addr, vals, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		for _, r := range f.handleOSC(addr, vals) {
			f.osc.WriteTo(r, from)
		}
	}
}

func oscErr(msg string) [][]byte {
	b, _ := Encode("/*", msg)
	return [][]byte{b}
}

func (f *fakeWing) handleOSC(addr string, vals []any) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if addr == "/?" {
		// like the real console (fw 3.1.2): the identity comes back on "/*"
		b, _ := Encode("/*", "WING,127.0.0.1,Test WING,wing-fullsize,S0000001,3.1.2-0-g53fb5e0d:release")
		return [][]byte{b}
	}
	if len(vals) == 1 {
		if blob, ok := vals[0].([]byte); ok {
			if len(blob) == 1 && blob[0] == tokDefReq {
				out := f.defsOf(addr)
				if out == nil {
					return oscErr("NODE NOT FOUND")
				}
				b, _ := Encode(addr, out)
				return [][]byte{b}
			}
			return oscErr("NODE NOT FOUND")
		}
	}
	p := f.params[addr]
	if p == nil {
		return oscErr("NODE NOT FOUND")
	}
	if len(vals) == 0 {
		b, _ := Encode(addr, f.oscValue(p)...)
		return [][]byte{b}
	}
	// a set
	f.sets++
	if f.failNext != "" {
		e := f.failNext
		f.failNext = ""
		return oscErr(e)
	}
	if f.params["/$ctl/osc/ronly"].i == 1 || f.dropSets || p.ro {
		return nil // dropped without a word, as the console does
	}
	if err := f.assign(addr, p, vals[0]); err != "" {
		return oscErr(err)
	}
	return nil
}

func (f *fakeWing) oscValue(p *fparam) []any {
	switch p.typ {
	case 'i':
		norm := 0.0
		if p.max > p.min {
			norm = (float64(p.i) - p.min) / (p.max - p.min)
		}
		return []any{strconv.Itoa(p.i), float32(norm), int32(p.i)}
	case 'f', 'l':
		norm := 0.0
		if p.fader {
			norm = dbToPos(p.f)
		} else if p.max > p.min {
			norm = (p.f - p.min) / (p.max - p.min)
		}
		txt := strconv.FormatFloat(p.f, 'f', 1, 64)
		if p.fader && p.f <= -144 {
			txt = "-oo"
		}
		return []any{txt, float32(norm), float32(p.f)}
	case 'e':
		if p.i >= 0 && p.i < len(p.items) {
			return []any{p.items[p.i]}
		}
		return []any{""}
	}
	return []any{p.s}
}

// assign sets a parameter the way the console converts types (p.22).
func (f *fakeWing) assign(path string, p *fparam, v any) string {
	num := func() (float64, bool) {
		switch x := v.(type) {
		case int32:
			return float64(x), true
		case float32:
			return float64(x), true
		case string:
			g, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			return g, err == nil
		}
		return 0, false
	}
	switch p.typ {
	case 'i':
		n, ok := num()
		if !ok {
			return "VALUE ERROR"
		}
		if n == -1 && p.max == 1 {
			n = float64(1 - p.i) // toggle
		}
		if n < p.min || n > p.max {
			return "VALUE ERROR"
		}
		p.i = int(n)
	case 'f', 'l':
		n, ok := num()
		if !ok {
			return "VALUE ERROR"
		}
		if n < p.min-1e-6 || n > p.max+1e-6 {
			return "VALUE ERROR"
		}
		p.f = n
	case 'e':
		if s, ok := v.(string); ok {
			idx := -1
			for i, it := range p.items {
				if strings.EqualFold(it, s) {
					idx = i
				}
			}
			if idx < 0 {
				return "VALUE ERROR"
			}
			p.i = idx
		} else if n, ok := num(); ok && int(n) >= 0 && int(n) < len(p.items) {
			p.i = int(n)
		} else {
			return "VALUE ERROR"
		}
	default:
		s, _ := v.(string)
		p.s = s
	}
	f.changed(path)
	f.effects(path)
	return ""
}

// effects: what the console does after a write (effective values,
// transports, show control).
func (f *fakeWing) effects(path string) {
	set := func(p string, fn func(*fparam)) {
		if x := f.params[p]; x != nil {
			fn(x)
			f.changed(p)
		}
	}
	switch {
	case strings.HasSuffix(path, "/mute") || strings.HasSuffix(path, "/fdr"):
		f.recomputeEffective()
	case strings.HasPrefix(path, "/fx/") && strings.HasSuffix(path, "/mdl"):
		var n int
		fmt.Sscanf(path, "/fx/%d/mdl", &n)
		p := f.params[path]
		f.setModel(n, p.items[p.i])
	case strings.HasSuffix(path, "/$solo"):
		any1 := 0
		for _, s := range faderStrips() {
			if x := f.params[s.Path()+"/$solo"]; x != nil && x.i == 1 {
				any1 = 1
			}
		}
		set("/$stat/solo", func(p *fparam) { p.i = any1 })
	case path == "/rec/$action":
		act := f.params[path].items[f.params[path].i]
		m := map[string]int{"STOP": 0, "REC": 1, "PAUSE": 2}
		if st, ok := m[act]; ok {
			set("/rec/$actstate", func(p *fparam) { p.i = st })
			if act == "REC" {
				set("/rec/$actfile", func(p *fparam) { p.s = "R_TEST.wav" })
			}
		}
	case path == "/play/$action":
		act := f.params[path].items[f.params[path].i]
		m := map[string]int{"STOP": 0, "PLAY": 1, "PAUSE": 2}
		if st, ok := m[act]; ok {
			set("/play/$actstate", func(p *fparam) { p.i = st })
		}
	case path == "/$ctl/lib/$action":
		act := f.params[path].items[f.params[path].i]
		idx := f.params["/$ctl/lib/$actionidx"].i
		cur := f.params["/$ctl/lib/$actidx"].i
		next := cur
		switch act {
		case "GO":
			next = idx
		case "GOTAG":
			next = idx // tags = positions in the fake
		case "GONEXT":
			next = cur + 1
			if cur == 0 {
				next = 1
			}
		case "GOPREV":
			next = cur - 1
		}
		if next >= 1 && next <= len(fakeScenes) {
			set("/$ctl/lib/$actidx", func(p *fparam) { p.i = next })
			set("/$ctl/lib/$active", func(p *fparam) { p.s = fmt.Sprintf("I:SHOWS/scene_%d.snap", next) })
		}
	}
}

// recomputeEffective: $mute = 1 own mute, 2 muted by a mute group or DCA
// (the add-on's reading of the doc; unverified), $fdr = fader + DCA.
func (f *fakeWing) recomputeEffective() {
	for _, s := range faderStrips() {
		if !kinds[s.Kind].chLike {
			continue
		}
		p := s.Path()
		tags := f.params[p+"/tags"].s
		em := 0
		if f.params[p+"/mute"].i == 1 {
			em = 1
		} else {
			for g := 1; g <= 8; g++ {
				if strings.Contains(tags, fmt.Sprintf("#M%d", g)) && f.params[fmt.Sprintf("/mgrp/%d/mute", g)].i == 1 {
					em = 2
				}
			}
			for d := 1; d <= 16; d++ {
				if strings.Contains(tags, fmt.Sprintf("#D%d", d)) && f.params[fmt.Sprintf("/dca/%d/mute", d)].i == 1 {
					em = 2
				}
			}
		}
		if x := f.params[p+"/$mute"]; x.i != em {
			x.i = em
			f.changed(p + "/$mute")
		}
		ef := f.params[p+"/fdr"].f
		for d := 1; d <= 16; d++ {
			if strings.Contains(tags, fmt.Sprintf("#D%d", d)) {
				ef = math.Max(-144, ef+math.Max(-144, f.params[fmt.Sprintf("/dca/%d/fdr", d)].f))
			}
		}
		if x := f.params[p+"/$fdr"]; x.f != ef {
			x.f = ef
			f.changed(p + "/$fdr")
		}
	}
}

// desk changes a value as if from the console's surface.
func (f *fakeWing) desk(path string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.params[path]
	if p == nil {
		f.t.Fatalf("fake: no %s", path)
	}
	wasRO := p.ro
	p.ro = false
	if e := f.assign(path, p, v); e != "" {
		f.t.Fatalf("fake desk %s: %s", path, e)
	}
	p.ro = wasRO
}

func (f *fakeWing) get(path string) fparam {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.params[path]; p != nil {
		return *p
	}
	return fparam{}
}

func (f *fakeWing) setsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets
}

// defsOf answers "<path> ,b dd": a node's children, or a parameter's own
// definition, then DE.
func (f *fakeWing) defsOf(path string) []byte {
	var out []byte
	def := func(full string, parent uint32, idx int) {
		if p := f.params[full]; p != nil {
			out = append(out, encodeDef(f.paramDef(full, p, parent))...)
			return
		}
		name := full[strings.LastIndex(full, "/")+1:]
		out = append(out, encodeDef(nodeDef{Parent: parent, Hash: crc32.ChecksumIEEE([]byte(full)), Index: idx, Name: name, Long: strings.ToUpper(name)})...)
	}
	if p := f.params[path]; p != nil {
		def(path, crc32.ChecksumIEEE([]byte(path[:strings.LastIndex(path, "/")])), 0)
		return append(out, tokEnd)
	}
	kids, ok := f.nodes[path]
	if !ok {
		return nil
	}
	ph := crc32.ChecksumIEEE([]byte(path))
	for i, k := range kids {
		full := path + "/" + k
		if path == "/" {
			full = "/" + k
		}
		def(full, ph, i+1)
	}
	return append(out, tokEnd)
}

func (f *fakeWing) paramDef(path string, p *fparam, parent uint32) nodeDef {
	name := path[strings.LastIndex(path, "/")+1:]
	d := nodeDef{Parent: parent, Hash: p.hash, Name: name, Long: strings.ToUpper(name)}
	switch p.typ {
	case 'i':
		d.Flags = typeInt << 4
		d.Min, d.Max = p.min, p.max
	case 'f', 'l':
		if p.fader {
			d.Flags = typeFader<<4 | 1
		} else if p.typ == 'l' {
			d.Flags = typeLogF << 4
			d.Min, d.Max, d.Steps = p.min, p.max, 100
		} else {
			d.Flags = typeLinF << 4
			d.Min, d.Max, d.Steps = p.min, p.max, 1000
		}
	case 'e':
		d.Flags = typeEnum << 4
		d.Items = p.items
		d.ItemLong = p.items
	default:
		d.Flags = typeString << 4
		d.MaxLen = 64
	}
	if p.ro {
		d.Flags |= 1 << 9
	}
	return d
}

// ------------------------------------------------------------- native

func (f *fakeWing) serveTCP() {
	for {
		c, err := f.tcp.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		if len(f.clients) >= f.maxClient {
			f.mu.Unlock()
			c.Close()
			continue
		}
		cl := &fclient{conn: c, enc: newNRPEncoder()}
		f.clients = append(f.clients, cl)
		f.mu.Unlock()
		go f.serveClient(cl)
	}
}

func (f *fakeWing) drop(cl *fclient) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.clients {
		if x == cl {
			f.clients = append(f.clients[:i], f.clients[i+1:]...)
			break
		}
	}
	cl.conn.Close()
}

func (cl *fclient) send(ch int, payload []byte) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.conn.SetWriteDeadline(time.Now().Add(time.Second))
	cl.conn.Write(cl.enc.frame(ch, payload))
}

func (f *fakeWing) serveClient(cl *fclient) {
	defer f.drop(cl)
	dec := newNRPDecoder()
	var ch1, ch3 []byte
	buf := make([]byte, 4096)
	for {
		cl.conn.SetReadDeadline(time.Now().Add(10 * time.Second)) // the console's idle timeout
		n, err := cl.conn.Read(buf)
		if err != nil {
			return
		}
		dec.feed(buf[:n], func(ch int, db byte) {
			switch ch {
			case chanAudio:
				ch1 = append(ch1, db)
			case chanMeters:
				ch3 = append(ch3, db)
			}
		})
		// channel 1: the add-on only sends DA DD (root definition)
		for len(ch1) >= 2 {
			if ch1[0] == tokRoot && ch1[1] == tokDefReq {
				f.mu.Lock()
				d := f.defsOf("/")
				f.mu.Unlock()
				cl.send(chanAudio, d)
				ch1 = ch1[2:]
				continue
			}
			ch1 = ch1[1:]
		}
		ch3 = f.meterTokens(cl, ch3)
	}
}

// meterTokens parses channel-3 requests: D3 port.w, D4 id.l, then DC
// <family index>... DE for a definition, or D4 id alone to renew.
func (f *fakeWing) meterTokens(cl *fclient, b []byte) []byte {
	for len(b) > 0 {
		switch b[0] {
		case tokI16:
			if len(b) < 3 {
				return b
			}
			f.mu.Lock()
			cl.port = int(binary.BigEndian.Uint16(b[1:]))
			f.mu.Unlock()
			b = b[3:]
		case tokI32:
			if len(b) < 5 {
				return b
			}
			id := binary.BigEndian.Uint32(b[1:])
			rest := b[5:]
			if len(rest) > 0 && rest[0] == tokReq {
				end := -1
				for i := 1; i < len(rest); i++ {
					if rest[i] == tokEnd {
						end = i
						break
					}
				}
				if end < 0 {
					return b
				}
				var strips []stripRef
				fam := map[byte]string{0xAB: "ch", 0xAC: "aux", 0xAD: "bus", 0xAE: "main", 0xAF: "mtx", 0xA5: "dca"}
				for i := 1; i+1 < end; i += 2 {
					strips = append(strips, stripRef{fam[rest[i]], int(rest[i+1]) + 1})
				}
				f.mu.Lock()
				cl.id, cl.strips, cl.renewed = id, strips, time.Now()
				cl.requests++
				f.mu.Unlock()
				b = rest[end+1:]
				continue
			}
			f.mu.Lock()
			if id == cl.id {
				cl.renewed = time.Now()
			}
			f.mu.Unlock()
			b = rest
		default:
			b = b[1:]
		}
	}
	return b
}

// changed pushes D7 <hash> <value> to every native client (f.mu held).
func (f *fakeWing) changed(path string) {
	p := f.params[path]
	var v nval
	switch p.typ {
	case 'i':
		v = nval{kind: 'i', i: int64(p.i)}
	case 'f', 'l':
		v = nval{kind: 'f', f: p.f}
	case 'e':
		v = nval{kind: 'i', i: int64(p.i)} // enums travel as their index
	default:
		v = nval{kind: 's', s: p.s}
	}
	msg := binary.BigEndian.AppendUint32([]byte{tokHash}, p.hash)
	msg = append(msg, encodeValue(v)...)
	for _, cl := range f.clients {
		go cl.send(chanAudio, msg)
	}
}

// sendMeters sends each live meter subscription every 50 ms.
func (f *fakeWing) sendMeters(done chan struct{}) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		f.mu.Lock()
		for _, cl := range f.clients {
			if cl.port == 0 || len(cl.strips) == 0 || time.Since(cl.renewed) > 5*time.Second {
				continue
			}
			b := binary.BigEndian.AppendUint32(nil, cl.id)
			for _, s := range cl.strips {
				lv := int16(f.meterDB * 256)
				for w := 0; w < meterWords(s); w++ {
					v := lv
					if w >= 4 {
						v = 0
					}
					if w == 8 { // gate LED on for channels
						v = 1
					}
					b = binary.BigEndian.AppendUint16(b, uint16(v))
				}
			}
			host := cl.conn.RemoteAddr().(*net.TCPAddr).IP
			f.mpc.WriteTo(b, &net.UDPAddr{IP: host, Port: cl.port})
		}
		f.mu.Unlock()
	}
}

func (f *fakeWing) meterSub() (requests int, strips []stripRef, live bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cl := range f.clients {
		requests += cl.requests
		if len(cl.strips) > 0 && time.Since(cl.renewed) <= 5*time.Second {
			strips, live = cl.strips, true
		}
	}
	return
}

func (f *fakeWing) clientCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

// ---------------------------------------------------------- discovery

func (f *fakeWing) serveDiscovery() {
	buf := make([]byte, 64)
	for {
		n, from, err := f.disc.ReadFrom(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) == "WING?" {
			f.disc.WriteTo([]byte("WING,127.0.0.1,Test WING,ngc-full,S0000001,3.1.2"), from)
		}
	}
}
