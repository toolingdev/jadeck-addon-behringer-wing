package main

// Wing is one WING console connection.
//
// Transport (README "How it talks to the console"):
//   - OSC, UDP 2223: every read and every write, by path. Writes are
//     always read back, and "/*" error replies are reported.
//   - "<node> ,b dd" over OSC: node definitions, which carry the 32-bit
//     hash of every parameter. That is how native events are mapped back
//     to paths; the hash function itself is not documented.
//   - Native, TCP 2222: the console pushes every change to every
//     connected client (up to 24), with no subscription to fight over.
//     The add-on only listens there (plus a keep-alive) and asks for
//     meters on channel 3.
//   - When the native link is down (blocked port, 24 clients already) or
//     the hashes couldn't be learned, the add-on polls what is on screen
//     over OSC instead. It never takes the console's single OSC
//     subscription.
//
// Nothing is written on connect: opening a connection only reads.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

const (
	defaultOSCPort    = 2223
	defaultNativePort = 2222
)

type wingConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`       // OSC
	NativePort  int    `json:"nativePort"` // TCP
	MeterPort   int    `json:"meterPort"`  // UDP for meter data; 0 = any free port
	ReadOnly    bool   `json:"readOnly"`
	MuteStrips  string `json:"muteStrips"`
	FaderStrips string `json:"faderStrips"`
	MeterStrips string `json:"meterStrips"`
	DCAStrips   string `json:"dcaStrips"`
	Meters      string `json:"meters"` // "auto" (while a widget shows them) or "off"
	Labels      string `json:"labels"` // "ch:3=Pulpit, ch:4=Choir"
}

// identity is what the console says about itself (/? and discovery):
// "WING,<ip>,<name>,<model>,<serial>,<firmware>" (p.16, p.20).
type identity struct {
	IP, Name, Model, Serial, Firmware string
}

func parseIdentity(s string) (identity, bool) {
	p := strings.Split(strings.TrimRight(s, "\x00\r\n "), ",")
	if len(p) < 6 || !strings.EqualFold(p[0], "WING") {
		return identity{}, false
	}
	return identity{IP: p[1], Name: p[2], Model: p[3], Serial: p[4], Firmware: strings.Join(p[5:], ",")}, true
}

// ModelName is a friendly name for the model code.
func (id identity) ModelName() string {
	switch strings.ToLower(id.Model) {
	case "ngc-full", "wing-fullsize":
		return "WING (full size)"
	case "":
		return "WING"
	}
	return "WING " + id.Model
}

// Errors the add-on reports instead of silently doing nothing.
var (
	errReadOnly = errors.New("this connection is read only (turn off “Read only” in the connection's settings to change things)")
	errOSCLock  = errors.New("the console's OSC lock is on (WING: Setup → Remote → OSC read only), so it ignores changes from jadeck")
	errOffline  = errors.New("the console isn't answering (off, unplugged or on another network)")
)

type Wing struct {
	cfg    wingConfig
	osc    *oscClient
	labels map[stripRef]string
	muteL  []stripRef
	faderL []stripRef
	meterL []stripRef
	dcaL   []stripRef

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	nmu    sync.Mutex
	notify addon.Notifier

	mu          sync.Mutex
	vals        map[string]value
	hashes      map[uint32]string
	defs        map[string]nodeDef
	scenes      []string
	ident       identity
	online      bool
	everOnline  bool
	lastErr     string
	syncedAt    time.Time
	oscLockPath string
	hot         map[stripRef]time.Time // strips frames asked for lately
	probeReport string
	dirty       bool
	learned     map[string][]string    // node -> children names (detail.go)
	detailHot   map[stripRef]time.Time // strips a channel widget shows
	detailRead  map[stripRef]time.Time // when their detail was last read
	fxModel     map[int]string         // FX slot -> model seen

	native nativeLink
	meters meterState

	writeMu sync.Mutex
}

func openWing(c wingConfig) (*Wing, error) {
	c.Host = strings.TrimSpace(c.Host)
	if c.Host == "" {
		return nil, fmt.Errorf("enter the console's IP address: %w", addon.ErrNotConfigured)
	}
	if c.Port == 0 {
		c.Port = defaultOSCPort
	}
	if c.NativePort == 0 {
		c.NativePort = defaultNativePort
	}
	if c.Meters == "" {
		c.Meters = "auto"
	}
	w := &Wing{cfg: c, vals: map[string]value{}, hashes: map[uint32]string{}, defs: map[string]nodeDef{},
		hot: map[stripRef]time.Time{}, labels: map[stripRef]string{}, oscLockPath: "/$ctl/osc/ronly",
		learned: map[string][]string{}, detailHot: map[stripRef]time.Time{}, detailRead: map[stripRef]time.Time{}, fxModel: map[int]string{}}
	var err error
	lists := []struct {
		dst  *[]stripRef
		text string
		def  string
		what string
	}{
		{&w.muteL, c.MuteStrips, "ch:1-16", "Mute grid strips"},
		{&w.faderL, c.FaderStrips, "ch:1-8, main:1", "Fader strips"},
		{&w.meterL, c.MeterStrips, "ch:1-16, main:1", "Meter strips"},
		{&w.dcaL, c.DCAStrips, "dca:1-8", "DCA strips"},
	}
	for _, l := range lists {
		t := l.text
		if strings.TrimSpace(t) == "" {
			t = l.def
		}
		if *l.dst, err = parseStripList(t); err != nil {
			return nil, fmt.Errorf("%s: %w", l.what, err)
		}
	}
	for _, part := range strings.FieldsFunc(c.Labels, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		s, err := parseStrip(k)
		if err != nil {
			return nil, fmt.Errorf("Labels: %w", err)
		}
		w.labels[s] = strings.TrimSpace(v)
	}
	host := c.Host
	if w.osc, err = dialOSC(net.JoinHostPort(host, strconv.Itoa(c.Port))); err != nil {
		return nil, fmt.Errorf("console address: %w", err)
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.done = make(chan struct{})
	go w.run()
	return w, nil
}

func (w *Wing) Close() error {
	w.cancel()
	<-w.done
	w.meters.close()
	return w.osc.Close()
}

// SetNotify: the add-on tells the controller when something changed
// (at most 10 times a second).
func (w *Wing) SetNotify(n addon.Notifier) {
	w.nmu.Lock()
	w.notify = n
	w.nmu.Unlock()
}

func (w *Wing) markDirty() {
	w.mu.Lock()
	w.dirty = true
	w.mu.Unlock()
}

func (w *Wing) flushNotify() {
	w.mu.Lock()
	d := w.dirty
	w.dirty = false
	w.mu.Unlock()
	if !d {
		return
	}
	w.nmu.Lock()
	n := w.notify
	w.nmu.Unlock()
	if n != nil {
		n()
	}
}

// ------------------------------------------------------------- params

// stripParams are the parameters read and followed for each strip.
func stripParams(s stripRef) []string {
	switch {
	case s.Kind == "mgrp":
		return []string{"name", "mute"}
	case s.Kind == "dca":
		return []string{"name", "col", "mute", "fdr", "$solo"}
	}
	out := []string{"$name", "name", "$col", "col", "mute", "$mute", "fdr", "$fdr", "$solo", "tags"}
	// Stereo: an input strip follows its source's mode (in/set/$mode: M,
	// ST, M/S); a bus, main or matrix is stereo unless busmono is on.
	if s.Kind == "ch" || s.Kind == "aux" {
		// $g: the preamp gain of the patched source (none for sources
		// without a preamp); it is a channel-strip control, never phantom.
		out = append(out, "in/set/$mode", "in/set/$g")
	} else {
		out = append(out, "busmono")
	}
	return out
}

// hotParams are the ones that change during a service (polled when the
// native link is down).
func hotParams(s stripRef) []string {
	switch s.Kind {
	case "mgrp":
		return []string{"mute"}
	case "dca":
		return []string{"mute", "fdr", "$solo"}
	}
	return []string{"mute", "$mute", "fdr", "$fdr", "$solo"}
}

// globalParams: status, locks, scenes, recorder, player, talkback.
var globalParams = []string{
	"/$syscfg/tcplock", "/$ctl/$stat/cnslock",
	"/$stat/solo", "/$stat/usbstate", "/$stat/usbvolname",
	"/$ctl/lib/$actidx", "/$ctl/lib/$active", "/$ctl/lib/$actshow", "/$ctl/lib/$activeid",
	"/rec/$actstate", "/rec/$time", "/rec/$actfile",
	"/play/$actstate", "/play/$song", "/play/$artist", "/play/$pos", "/play/$total", "/play/$actfile",
	"/cfg/talk/A/$on", "/cfg/talk/B/$on", "/cfg/talk/A/mode", "/cfg/talk/B/mode",
}

// ddNodes: nodes whose children's hashes the add-on learns, so native
// events for them can be mapped back to paths.
func ddNodes() []string {
	out := []string{"/$ctl/lib", "/rec", "/play", "/cfg/talk/A", "/cfg/talk/B", "/$stat", "/$syscfg", "/$ctl/$stat"}
	for _, s := range allStrips() {
		out = append(out, s.Path())
	}
	return out
}

func (w *Wing) put(path string, v value) {
	w.mu.Lock()
	w.vals[path] = v
	w.dirty = true
	w.mu.Unlock()
}

func (w *Wing) val(path string) (value, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, ok := w.vals[path]
	return v, ok
}

func (w *Wing) str(path string) string {
	v, _ := w.val(path)
	return v.s
}

func (w *Wing) num(path string) (float64, bool) {
	v, ok := w.val(path)
	if !ok {
		return 0, false
	}
	return v.f, true
}

// readAll reads paths over OSC, many at once. Paths the console doesn't
// have are skipped (and counted).
func (w *Wing) readAll(ctx context.Context, paths []string) (failed int, firstErr error) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := w.osc.get(ctx, p)
			if err != nil {
				mu.Lock()
				failed++
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			w.put(p, v)
		}(p)
	}
	wg.Wait()
	return failed, firstErr
}

// hello reads /? (the console's identity). It proves the console answers.
func (w *Wing) hello(ctx context.Context) (identity, error) {
	r, err := w.osc.raw(ctx, "/?")
	if err != nil {
		// A real WING (fw 3.1.2) answers /? on the address it also uses for
		// errors ("/*"), so the identity arrives looking like an error text.
		var ce consoleError
		if errors.As(err, &ce) {
			if id, ok := parseIdentity(strings.SplitN(string(ce), "; ", 2)[0]); ok {
				return id, nil
			}
		}
		if errors.Is(err, errNoAnswer) {
			return identity{}, errOffline
		}
		return identity{}, err
	}
	id, ok := parseIdentity(firstString(r.vals))
	if !ok {
		return identity{}, fmt.Errorf("something answered on the OSC port, but not as a WING (%q)", firstString(r.vals))
	}
	return id, nil
}

// readLock finds the OSC lock (the doc spells it /$ctl/OSC/ronly, the
// snapshot /$ctl/osc/ronly; whichever answers is kept).
func (w *Wing) readLock(ctx context.Context) {
	w.mu.Lock()
	p := w.oscLockPath
	w.mu.Unlock()
	for _, path := range []string{p, "/$ctl/osc/ronly", "/$ctl/OSC/ronly"} {
		if v, err := w.osc.get(ctx, path); err == nil {
			w.put("lock.osc", v)
			w.mu.Lock()
			w.oscLockPath = path
			w.mu.Unlock()
			return
		}
	}
}

// fullSync reads everything the widgets show and learns the hashes.
func (w *Wing) fullSync(ctx context.Context) error {
	id, err := w.hello(ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.ident = id
	w.mu.Unlock()
	w.readLock(ctx)
	var paths []string
	for _, s := range allStrips() {
		for _, p := range stripParams(s) {
			paths = append(paths, s.Path()+"/"+p)
		}
	}
	paths = append(paths, globalParams...)
	if failed, err := w.readAll(ctx, paths); failed == len(paths) {
		return fmt.Errorf("the console answered /? but no parameter: %v", err)
	}
	w.learnHashes(ctx)
	w.loadScenes(ctx)
	w.mu.Lock()
	w.syncedAt = time.Now()
	w.mu.Unlock()
	return nil
}

// learnHashes asks for the definitions of every node the add-on follows.
func (w *Wing) learnHashes(ctx context.Context) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	hashes := map[uint32]string{}
	defs := map[string]nodeDef{}
	var mu sync.Mutex
	for _, n := range ddNodes() {
		wg.Add(1)
		sem <- struct{}{}
		go func(n string) {
			defer wg.Done()
			defer func() { <-sem }()
			ds, err := w.osc.dd(ctx, n)
			if err != nil {
				return
			}
			mu.Lock()
			for _, d := range ds {
				p := n + "/" + d.Name
				if d.Name == "" {
					p = n + "/" + strconv.Itoa(d.Index)
				}
				hashes[d.Hash] = p
				defs[p] = d
			}
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	w.mu.Lock()
	for h, p := range hashes {
		w.hashes[h] = p
	}
	for p, d := range defs {
		w.defs[p] = d
	}
	w.mu.Unlock()
}

// loadScenes reads the open show's scene list. A plain GET of $scenes
// returns only the first name; the definition of /$ctl/lib lists them
// all as the items of the $scenes enum (p.70, p.90).
func (w *Wing) loadScenes(ctx context.Context) {
	ds, err := w.osc.dd(ctx, "/$ctl/lib")
	var names []string
	if err == nil {
		for _, d := range ds {
			if d.Name == "$scenes" {
				names = append(names, d.Items...)
				break
			}
		}
	}
	if names == nil {
		if v, err := w.osc.get(ctx, "/$ctl/lib/$scenes"); err == nil && strings.TrimSpace(v.s) != "" {
			names = []string{v.s}
		}
	}
	w.mu.Lock()
	w.scenes = names
	w.dirty = true
	w.mu.Unlock()
}

// ---------------------------------------------------------------- run

func (w *Wing) setOffline(err error) {
	w.mu.Lock()
	w.online = false
	w.lastErr = err.Error()
	w.dirty = true
	w.mu.Unlock()
}

func (w *Wing) isOnline() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.online
}

// run keeps the connection: sync, follow changes, re-sync after a drop.
func (w *Wing) run() {
	defer close(w.done)
	ctx := w.ctx
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.flushNotify()
			}
		}
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := w.fullSync(sctx)
		cancel()
		if err != nil {
			w.setOffline(err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 15*time.Second)
			continue
		}
		backoff = time.Second
		w.mu.Lock()
		w.online, w.everOnline, w.lastErr, w.dirty = true, true, "", true
		w.mu.Unlock()
		w.follow(ctx)
	}
}

// follow runs while the console answers: the native link, meters, and
// polling when the native link isn't giving events.
func (w *Wing) follow(ctx context.Context) {
	fctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.nativeLoop(fctx) }()
	go func() { defer wg.Done(); w.meterLoop(fctx) }()
	defer wg.Wait()

	poll := time.NewTicker(2 * time.Second)
	health := time.NewTicker(5 * time.Second)
	resync := time.NewTicker(60 * time.Second)
	defer poll.Stop()
	defer health.Stop()
	defer resync.Stop()
	misses := 0
	for {
		select {
		case <-fctx.Done():
			return
		case <-poll.C:
			if !w.nativeLive() {
				pctx, c := context.WithTimeout(fctx, 4*time.Second)
				w.pollHot(pctx)
				c()
			}
		case <-health.C:
			hctx, c := context.WithTimeout(fctx, 3*time.Second)
			_, err := w.hello(hctx)
			if err == nil {
				w.readLock(hctx)
			}
			c()
			if err != nil {
				if misses++; misses >= 2 {
					w.setOffline(err)
					return
				}
			} else {
				misses = 0
			}
		case <-resync.C:
			// Names, colours and the scene list rarely change; a slow
			// full read keeps them right even if an event was missed.
			sctx, c := context.WithTimeout(fctx, 20*time.Second)
			if err := w.fullSync(sctx); err != nil && fctx.Err() == nil {
				c()
				w.setOffline(err)
				return
			}
			c()
		}
	}
}

// pollHot reads what's on screen: the configured strips, every DCA and
// mute group, strips frames asked for, and the transport/status values.
func (w *Wing) pollHot(ctx context.Context) {
	set := map[stripRef]bool{}
	for _, l := range [][]stripRef{w.muteL, w.faderL, w.meterL, w.dcaL} {
		for _, s := range l {
			set[s] = true
		}
	}
	for n := 1; n <= 8; n++ {
		set[stripRef{"mgrp", n}] = true
	}
	set[stripRef{"main", 1}] = true
	w.mu.Lock()
	for s, at := range w.hot {
		if time.Since(at) < 30*time.Second {
			set[s] = true
		} else {
			delete(w.hot, s)
		}
	}
	w.mu.Unlock()
	paths := append([]string(nil), globalParams...)
	for s := range set {
		for _, p := range hotParams(s) {
			paths = append(paths, s.Path()+"/"+p)
		}
	}
	w.readAll(ctx, paths)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// --------------------------------------------------------- live events

// applyEvent stores a pushed change (native D7 <hash> <value>).
func (w *Wing) applyEvent(hash uint32, nv nval) {
	w.mu.Lock()
	path, ok := w.hashes[hash]
	def := w.defs[path]
	w.native.events++
	if !ok {
		w.native.unknown++
		w.mu.Unlock()
		return
	}
	w.native.lastPaths = append(w.native.lastPaths, path)
	if len(w.native.lastPaths) > 8 {
		w.native.lastPaths = w.native.lastPaths[1:]
	}
	w.mu.Unlock()
	v := value{norm: -1, at: time.Now()}
	switch nv.kind {
	case 'i':
		v.kind, v.f, v.s = 'i', float64(nv.i), strconv.FormatInt(nv.i, 10)
		if def.Type() == typeEnum && nv.i >= 0 && int(nv.i) < len(def.Items) {
			v.kind, v.s = 's', def.Items[nv.i]
		}
	case 'f':
		v.kind, v.f, v.s = 'f', nv.f, trimFloat(nv.f)
		if strings.HasSuffix(path, "fdr") {
			v.norm = dbToPos(nv.f)
		}
	case 's':
		v.kind, v.s = 's', nv.s
		if f, err := strconv.ParseFloat(nv.s, 64); err == nil {
			v.f = f
		}
	case 'n':
		// A normalized value only: read the real one over OSC.
		go func() {
			ctx, c := context.WithTimeout(w.ctx, 2*time.Second)
			defer c()
			if r, err := w.osc.get(ctx, path); err == nil {
				w.put(path, r)
			}
		}()
		return
	default:
		return
	}
	w.put(path, v)
	if strings.HasSuffix(path, "/mdl") {
		w.forget(strings.TrimSuffix(path, "/mdl")) // a new model has other parameters
	}
	if path == "/$ctl/lib/$actshow" {
		go func() {
			ctx, c := context.WithTimeout(w.ctx, 3*time.Second)
			defer c()
			w.loadScenes(ctx)
		}()
	}
}

// ----------------------------------------------------------- strip view

// stripView is one strip as the widgets see it.
type stripView struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	N      int      `json:"n"`
	Tag    string   `json:"tag"`   // "CH 3"
	Name   string   `json:"name"`  // the console's name, else the user's label, else ""
	Label  string   `json:"label"` // name, or the tag when unnamed
	Color  string   `json:"color"`
	Muted  bool     `json:"muted"`  // the strip's own mute button
	EMuted bool     `json:"emuted"` // effectively muted (own, DCA or mute group)
	DB     *float64 `json:"db,omitempty"`
	EDB    *float64 `json:"edb,omitempty"` // after DCA
	Pos    *float64 `json:"pos,omitempty"` // 0..1 fader position
	Solo   bool     `json:"solo"`
	Stereo bool     `json:"stereo"`           // two meters (L/R); pan acts as balance
	Gain   *float64 `json:"gain,omitempty"`   // preamp gain (dB), absent: no preamp
	MuteBy string   `json:"muteBy,omitempty"` // muted by "DCA 2 · Band" / "MGRP 1 · Choir"
	// meters (only while running)
	L    *float64 `json:"l,omitempty"`
	R    *float64 `json:"r,omitempty"`
	Gate *bool    `json:"gate,omitempty"`
	Dyn  *bool    `json:"dyn,omitempty"`
	GR   *float64 `json:"gr,omitempty"`
}

func fp(f float64) *float64 { return &f }

func (w *Wing) view(s stripRef) stripView {
	p := s.Path() + "/"
	v := stripView{ID: s.ID(), Kind: s.Kind, N: s.N, Tag: s.Label()}
	w.mu.Lock()
	defer w.mu.Unlock()
	get := func(k string) (value, bool) { x, ok := w.vals[p+k]; return x, ok }
	name := ""
	if x, ok := get("$name"); ok && strings.TrimSpace(x.s) != "" {
		name = x.s
	} else if x, ok := get("name"); ok {
		name = x.s
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = w.labels[s]
	}
	v.Name = name
	v.Label = name
	if v.Label == "" {
		v.Label = v.Tag
	}
	col := 0
	if x, ok := get("$col"); ok && x.f > 0 {
		col = int(x.f)
	} else if x, ok := get("col"); ok {
		col = int(x.f)
	}
	v.Color = colorHex(col)
	if x, ok := get("mute"); ok {
		v.Muted = x.f >= 1
	}
	v.EMuted = v.Muted
	if x, ok := get("$mute"); ok {
		// $mute is 0..2; 0 = live is the only value the doc implies
		// for sure, so anything else counts as muted.
		v.EMuted = x.f >= 1 || v.Muted
	}
	if x, ok := get("fdr"); ok && s.Kind != "mgrp" {
		v.DB = fp(x.f)
		pos := x.norm
		if pos < 0 {
			pos = dbToPos(x.f)
		}
		v.Pos = fp(math.Round(pos*1000) / 1000)
	}
	if x, ok := get("$fdr"); ok {
		v.EDB = fp(x.f)
	} else {
		v.EDB = v.DB
	}
	if x, ok := get("$solo"); ok {
		v.Solo = x.f >= 1
	}
	switch s.Kind {
	case "ch", "aux":
		if x, ok := get("in/set/$mode"); ok {
			m := strings.ToUpper(strings.TrimSpace(x.s))
			v.Stereo = m == "ST" || m == "M/S"
		}
	case "bus", "main", "mtx":
		v.Stereo = true
		if x, ok := get("busmono"); ok && x.f >= 1 {
			v.Stereo = false
		}
	}
	if v.EMuted && !v.Muted {
		v.MuteBy = w.muteReasonLocked(s)
	}
	if x, ok := get("in/set/$g"); ok && (s.Kind == "ch" || s.Kind == "aux") && x.kind == 'f' {
		v.Gain = fp(x.f)
	}
	return v
}

// muteReasonLocked names what mutes a strip that isn't muted itself: a
// muted DCA (#Dn tag) or mute group (#Mn tag, the firmware's format;
// unconfirmed in the doc). w.mu held.
func (w *Wing) muteReasonLocked(s stripRef) string {
	tags := w.vals[s.Path()+"/tags"].s
	var why []string
	for _, t := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		var kind string
		switch {
		case strings.HasPrefix(t, "#D"):
			kind = "dca"
		case strings.HasPrefix(t, "#M"):
			kind = "mgrp"
		default:
			continue
		}
		n, err := strconv.Atoi(t[2:])
		if err != nil || n < 1 || n > kinds[kind].count {
			continue
		}
		g := stripRef{kind, n}
		if w.vals[g.Path()+"/mute"].f >= 1 {
			lbl := g.Label()
			if nm := strings.TrimSpace(w.vals[g.Path()+"/name"].s); nm != "" {
				lbl += " · " + nm
			}
			why = append(why, lbl)
		}
	}
	if len(why) == 0 {
		return "DCA or mute group"
	}
	return strings.Join(why, ", ")
}

// ---------------------------------------------------------------- writes

// write sets path to arg, reads back readPath (path when "") after
// delay, and checks it with ok. Locks and read-only are refused up front;
// a console error or an unchanged value is reported.
func (w *Wing) write(ctx context.Context, path string, arg any, readPath string, delay time.Duration, ok func(value) bool) (value, error) {
	if w.cfg.ReadOnly {
		return value{}, errReadOnly
	}
	if !w.isOnline() {
		return value{}, errOffline
	}
	if lv, has := w.val("lock.osc"); has && lv.f >= 1 {
		return value{}, errOSCLock
	}
	if readPath == "" {
		readPath = path
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	mark := w.osc.errMark()
	if err := w.osc.set(path, arg); err != nil {
		return value{}, err
	}
	if delay > 0 && !sleepCtx(ctx, delay) {
		return value{}, ctx.Err()
	}
	v, err := w.osc.get(ctx, readPath)
	if e := w.osc.errSince(mark); e != "" {
		return v, consoleError(e)
	}
	if err != nil {
		return v, fmt.Errorf("couldn't read %s back: %w", readPath, err)
	}
	w.put(readPath, v)
	if ok != nil && !ok(v) {
		w.readLock(ctx)
		if lv, has := w.val("lock.osc"); has && lv.f >= 1 {
			return v, errOSCLock
		}
		shown := v.s
		if shown == "" {
			shown = trimFloat(v.f)
		}
		return v, fmt.Errorf("the console didn't take the change (%s is still %s); check the console's locks (Setup → Remote)", readPath, shown)
	}
	return v, nil
}

func (w *Wing) refresh(ctx context.Context, paths ...string) { w.readAll(ctx, paths) }

// setMute sets a strip's own mute (mode on, off or toggle).
func (w *Wing) setMute(ctx context.Context, s stripRef, mode string) (bool, error) {
	want := true
	switch strings.ToLower(mode) {
	case "", "toggle":
		cur, err := w.ownMute(ctx, s)
		if err != nil {
			return false, err
		}
		want = !cur
	case "on", "mute", "muted", "true", "1":
		want = true
	case "off", "unmute", "live", "false", "0":
		want = false
	default:
		return false, fmt.Errorf("mode %q: use on, off or toggle", mode)
	}
	iv := int32(0)
	if want {
		iv = 1
	}
	_, err := w.write(ctx, s.Path()+"/mute", iv, "", 0, func(v value) bool { return (v.f >= 1) == want })
	if err != nil {
		return false, err
	}
	if kinds[s.Kind].chLike {
		w.refresh(ctx, s.Path()+"/$mute")
	}
	return want, nil
}

func (w *Wing) ownMute(ctx context.Context, s stripRef) (bool, error) {
	if w.nativeLive() {
		if v, ok := w.val(s.Path() + "/mute"); ok {
			return v.f >= 1, nil
		}
	}
	v, err := w.osc.get(ctx, s.Path()+"/mute")
	if err != nil {
		if errors.Is(err, errNoAnswer) {
			return false, errOffline
		}
		return false, err
	}
	w.put(s.Path()+"/mute", v)
	return v.f >= 1, nil
}

// setFader sets a strip's fader in dB (-144 = -inf .. +10).
func (w *Wing) setFader(ctx context.Context, s stripRef, db float64) (float64, error) {
	if s.Kind == "mgrp" {
		return 0, fmt.Errorf("%s has no fader", s.Label())
	}
	if math.IsNaN(db) {
		return 0, errors.New("no level given")
	}
	db = math.Max(-144, math.Min(10, db))
	v, err := w.write(ctx, s.Path()+"/fdr", float32(db), "", 0, func(v value) bool {
		return math.Abs(v.f-db) < 0.6 || (db <= -143.5 && v.f <= -143.5)
	})
	if err != nil {
		return 0, err
	}
	if kinds[s.Kind].chLike {
		w.refresh(ctx, s.Path()+"/$fdr")
	}
	return v.f, nil
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
