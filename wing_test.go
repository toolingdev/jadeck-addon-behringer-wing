package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
	"github.com/toolingdev/jadeck/sdk/addon/addontest"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The framing examples from WING Remote Protocols p.95-96.
func TestNRPFraming(t *testing.T) {
	e := newNRPEncoder()
	e.ch = 3
	if got := e.frame(0, unhex(t, "d702dfaf0e02")); !bytes.Equal(got, unhex(t, "dfd0d702dfaf0e02")) {
		t.Fatalf("ex1 % x", got)
	}
	if got := e.frame(1, unhex(t, "d702dfdf0e02")); !bytes.Equal(got, unhex(t, "dfd1d702dfdf0e02")) {
		t.Fatalf("ex2 % x", got)
	}
	if got := e.frame(0, unhex(t, "d702dfd10e02")); !bytes.Equal(got, unhex(t, "dfd0d702dfded10e02")) {
		t.Fatalf("ex3 % x", got)
	}
	// receive: D702DFDEAF0E02 is D702DFAF0E02
	var out []byte
	d := newNRPDecoder()
	d.feed(unhex(t, "dfd1 d702dfdeaf0e02"), func(ch int, b byte) {
		if ch == 1 {
			out = append(out, b)
		}
	})
	if !bytes.Equal(out, unhex(t, "d702dfaf0e02")) {
		t.Fatalf("rx % x", out)
	}
	// round trip with every awkward byte
	payload := []byte{0xdf, 0xd0, 0xdf, 0xde, 0xdf, 0xdf, 0xd1, 0x00, 0xdf}
	out = nil
	d = newNRPDecoder()
	d.feed(newNRPEncoder().frame(1, payload), func(ch int, b byte) { out = append(out, b) })
	if !bytes.Equal(out, payload) {
		t.Fatalf("round trip % x -> % x", payload, out)
	}
}

// Node definitions from the doc (p.27-29).
func TestParseDefs(t *testing.T) {
	ds, err := parseDefs(unhex(t, "df001fdf17c242beefaeab000003246f6e06444157204f4e00400000000000000001de"))
	if err != nil || len(ds) != 1 {
		t.Fatal(ds, err)
	}
	d := ds[0]
	if d.Parent != 0xdf17c242 || d.Hash != 0xbeefaeab || d.Name != "$on" || d.Long != "DAW ON" || d.Type() != typeInt || d.Min != 0 || d.Max != 1 {
		t.Fatalf("%+v", d)
	}
	root := "df00180000000097a0043900000524737461740553544154450000df001100000000edca7af9000003636667000000" +
		"df001700000000" + "70b10139" + "0000026368074348414e4e454c0000de"
	ds, err = parseDefs(unhex(t, root))
	if err != nil || len(ds) != 3 || ds[0].Name != "$stat" || ds[1].Name != "cfg" || ds[2].Name != "ch" || ds[2].Hash != 0x70b10139 {
		t.Fatalf("%+v %v", ds, err)
	}
	// enum round trip
	e := nodeDef{Hash: 7, Name: "$scenes", Flags: typeEnum << 4, Items: []string{"A", "B~c"}, ItemLong: []string{"", ""}}
	got, err := parseDefs(append(encodeDef(e), tokEnd))
	if err != nil || len(got[0].Items) != 2 || got[0].Items[1] != "B~c" {
		t.Fatal(got, err)
	}
}

// Pushed events from the doc (p.30).
func TestEvents(t *testing.T) {
	type ev struct {
		h uint32
		v nval
	}
	var got []ev
	p := &nrpParser{onValue: func(h uint32, v nval) { got = append(got, ev{h, v}) }}
	p.feed(unhex(t, "d738ae75c2d5c3100000d77e463474d5c3100000")...)
	b := unhex(t, "d7f50f69f801d726855cd301")
	p.feed(b[:3]...) // split across reads
	p.feed(b[3:]...)
	if len(got) != 4 || got[0].h != 0x38ae75c2 || got[0].v.f != -144 || got[2].h != 0xf50f69f8 || got[2].v.i != 1 || got[3].v.i != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestMeterRequest(t *testing.T) {
	// doc: DF D3 D3 3737 (port 14135), D4 00000002 DC A0 01 DE (V1 family);
	// the add-on asks for the V2 family AB.
	got := meterRequest(2, 14135, []stripRef{{"ch", 2}})
	if !bytes.Equal(got, unhex(t, "d33737 d400000002 dc ab01 de")) {
		t.Fatalf("% x", got)
	}
	if !bytes.Equal(meterRenew(2), unhex(t, "d400000002")) {
		t.Fatal("renew")
	}
}

func TestStrips(t *testing.T) {
	l, err := parseStripList("ch:1-3, aux2, MAIN 1, 41, dca:1-dca:2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids(l), ",") != "ch:1,ch:2,ch:3,aux:2,main:1,aux:1,dca:1,dca:2" {
		t.Fatal(ids(l))
	}
	for _, bad := range []string{"ch:41", "bus:17", "foo:1", "dca:0"} {
		if _, err := parseStrip(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if s, _ := channelStrip(45); s != (stripRef{"aux", 5}) {
		t.Fatal(s)
	}
	for _, db := range []float64{-144, -60, -45, -30, -10, -3, 0, 4, 10} {
		if got := posToDB(dbToPos(db)); got-db > 0.1 || db-got > 0.1 {
			t.Errorf("law %v -> %v", db, got)
		}
	}
	if dbToPos(0) != 0.75 || dbToPos(-3) != 0.675 || dbToPos(10) != 1 {
		t.Fatal("doc points")
	}
	if colorHex(13) != "#ffb81a" || colorHex(0) != "#707070" {
		t.Fatal("colours")
	}
}

func TestDiscovery(t *testing.T) {
	f := newFakeWing(t)
	ids := discover([]string{f.disc.LocalAddr().String()}, 300*time.Millisecond)
	if len(ids) != 1 || ids[0].Name != "Test WING" || ids[0].Firmware != "3.1.2" || ids[0].ModelName() != "WING (full size)" {
		t.Fatalf("%+v", ids)
	}
}

// ------------------------------------------------------- integration

func wingConf(f *fakeWing, extra map[string]any) map[string]any {
	c := map[string]any{"preset": "wing", "host": "127.0.0.1", "port": f.oscPort(), "nativePort": f.nativePort(),
		"muteStrips": "ch:1-8, aux:1", "faderStrips": "ch:1-2, main:1", "labels": "ch:2=Lectern"}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func state(h *addontest.Harness) map[string]any {
	var st map[string]any
	h.Call("w", "addon.state", nil, &st)
	return st
}

func act(h *addontest.Harness, name string, p map[string]any) (map[string]any, error) {
	var out map[string]any
	err := h.TryCall("w", "addon.action", map[string]any{"name": name, "params": p}, &out)
	return out, err
}

func item(st map[string]any, key, id string) map[string]any {
	l, _ := st[key].([]any)
	for _, x := range l {
		m, _ := x.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func startWing(t *testing.T, f *fakeWing, extra map[string]any) *addontest.Harness {
	h := addontest.Start(t, spec())
	h.Open("w", "osc", wingConf(f, extra))
	waitFor(t, "first sync", 10*time.Second, func() bool { return state(h)["connected"] == true })
	return h
}

func TestWingEndToEnd(t *testing.T) {
	f := newFakeWing(t)
	h := startWing(t, f, nil)
	if n := f.setsCount(); n != 0 {
		t.Fatalf("%d writes on connect", n)
	}
	waitFor(t, "native link", 5*time.Second, func() bool { return state(h)["transport"] == "native" })

	st := state(h)
	if it := item(st, "mutes", "ch:1"); it["label"] != "Pulpit" || it["active"] != false {
		t.Fatalf("console name: %v", it)
	}
	if it := item(st, "mutes", "ch:2"); it["label"] != "Lectern" {
		t.Fatalf("fallback label: %v", it)
	}
	if it := item(st, "mutes", "aux:1"); it == nil || it["color"] == "" {
		t.Fatalf("aux: %v", it)
	}

	// mute and read back
	if r, err := act(h, "mute", map[string]any{"strip": "ch:3", "mode": "on"}); err != nil || r["muted"] != true {
		t.Fatal(r, err)
	}
	if f.get("/ch/3/mute").i != 1 {
		t.Fatal("not muted on the console")
	}
	if it := item(state(h), "mutes", "ch:3"); it["tone"] != "pgm" || it["active"] != true {
		t.Fatalf("%v", it)
	}
	// toggle back
	if r, err := act(h, "mute", map[string]any{"strip": "ch:3"}); err != nil || r["muted"] != false {
		t.Fatal(r, err)
	}

	// core mixer: 41 is AUX 1
	if err := h.TryCall("w", "mixer.setMute", map[string]any{"channel": 41, "muted": true}, nil); err != nil {
		t.Fatal(err)
	}
	if f.get("/aux/1/mute").i != 1 || f.get("/ch/41/mute").typ != 0 {
		t.Fatal("41 should be aux 1")
	}
	var gm map[string]bool
	h.Call("w", "mixer.getMute", map[string]any{"channel": 41}, &gm)
	if !gm["muted"] {
		t.Fatal("getMute 41")
	}
	var probe map[string]string
	h.Call("w", "mixer.probe", nil, &probe)
	if id := probe["identity"]; !strings.Contains(id, "Test WING") || !strings.Contains(id, "3.1.2") {
		t.Fatal(probe)
	}

	// faders: dB, and a slider's {"main:1": pos}
	if r, err := act(h, "set_fader", map[string]any{"strip": "ch:1", "db": -10}); err != nil || r["db"] != -10.0 {
		t.Fatal(r, err)
	}
	if _, err := act(h, "set_fader", map[string]any{"main:1": 0.75}); err != nil {
		t.Fatal(err)
	}
	if f.get("/main/1/fdr").f != 0 {
		t.Fatal(f.get("/main/1/fdr").f)
	}
	if st := state(h); st["mainPos"] != 0.75 {
		t.Fatal(st["mainPos"], st["mainLabel"])
	}

	// a change at the desk arrives as a native event (no polling)
	f.desk("/ch/4/mute", int32(1))
	waitFor(t, "desk mute event", time.Second, func() bool { return item(state(h), "mutes", "ch:4")["active"] == true })
	f.desk("/ch/1/$name", "Speaker")
	waitFor(t, "rename event", time.Second, func() bool { return item(state(h), "mutes", "ch:1")["label"] == "Speaker" })

	// mute group 1 holds ch 5-6: effective mute without own mute
	if _, err := act(h, "mute_group", map[string]any{"group": 1, "mode": "on"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "group mute shown", time.Second, func() bool {
		it := item(state(h), "mutes", "ch:5")
		return it["tone"] == "pgm" && it["active"] == false && strings.Contains(it["label"].(string), "group")
	})
	if it := item(state(h), "muteGroups", "mgrp:1"); it["label"] != "Choir" || it["tone"] != "pgm" {
		t.Fatal(it)
	}

	// scenes: list from the definition, go, next, by tag
	st = state(h)
	secs, _ := st["scenes"].([]any)
	if len(secs) != 1 || len(secs[0].(map[string]any)["items"].([]any)) != 5 {
		t.Fatalf("scenes %v", st["scenes"])
	}
	if r, err := act(h, "scene_go", map[string]any{"index": "3"}); err != nil || r["scene"] != 3.0 {
		t.Fatal(r, err)
	}
	if r, err := act(h, "scene_go_next", nil); err != nil || r["scene"] != 4.0 {
		t.Fatal(r, err)
	}
	if r, err := act(h, "scene_go_tag", map[string]any{"tag": 2}); err != nil || r["scene"] != 2.0 {
		t.Fatal(r, err)
	}
	if state(h)["sceneText"] != "Scene 2: Worship" {
		t.Fatal(state(h)["sceneText"])
	}
	if _, err := act(h, "scene_go", map[string]any{"index": 9}); err == nil {
		t.Fatal("scene 9 of 5 accepted")
	}

	// recorder, player, talkback
	// recording is held to confirm: without confirmed nothing happens
	sets := f.setsCount()
	for _, a := range []string{"rec_start", "rec_stop", "rec_new_file"} {
		if _, err := act(h, a, nil); err == nil || !strings.Contains(err.Error(), "hold") {
			t.Fatalf("%s without confirmed: %v", a, err)
		}
	}
	if f.setsCount() != sets {
		t.Fatal("an unconfirmed recorder action reached the console")
	}
	for _, id := range []string{"rec", "stop", "newfile"} {
		b := item(state(h), "recButtons", id)
		if b["hold"] != 5.0 || b["params"].(map[string]any)["confirmed"] != true {
			t.Fatalf("%s: %v", id, b)
		}
	}
	if b := item(state(h), "recButtons", "pause"); b["hold"] != nil {
		t.Fatal("pause is a tap")
	}
	if b := item(h.State("w", map[string]any{"holdSeconds": 8.0}), "recButtons", "stop"); b["hold"] != 8.0 {
		t.Fatal(b)
	}
	if b := item(h.State("w", map[string]any{"holdSeconds": 30.0}), "recButtons", "stop"); b["hold"] != 10.0 {
		t.Fatal("hold not clamped", b)
	}
	if r, err := act(h, "rec_start", map[string]any{"confirmed": true}); err != nil || r["state"] != "REC" {
		t.Fatal(r, err)
	}
	if b := item(state(h), "recButtons", "rec"); b["tone"] != "pgm" {
		t.Fatal(b)
	}
	if _, err := act(h, "rec_stop", map[string]any{"confirmed": true}); err != nil {
		t.Fatal(err)
	}
	if r, err := act(h, "play_play", nil); err != nil || r["state"] != "PLAY" {
		t.Fatal(r, err)
	}
	if _, err := act(h, "play_seek", map[string]any{"seconds": 30}); err != nil {
		t.Fatal(err)
	}
	if r, err := act(h, "talkback", map[string]any{"bus": "A"}); err != nil || r["on"] != true {
		t.Fatal(r, err)
	}
	if item(state(h), "talkButtons", "A")["active"] != true {
		t.Fatal("talkback state")
	}

	// solo clear
	f.desk("/ch/2/$solo", int32(1))
	f.desk("/bus/3/$solo", int32(1))
	waitFor(t, "solo shown", time.Second, func() bool { return item(state(h), "soloButtons", "clear")["tone"] == "pgm" })
	if r, err := act(h, "solo_clear", nil); err != nil || r["cleared"] != 2.0 {
		t.Fatal(r, err)
	}
	if f.get("/$stat/solo").i != 0 {
		t.Fatal("solo still on")
	}

	// errors the console sends are reported
	f.mu.Lock()
	f.failNext = "VALUE ERROR"
	f.mu.Unlock()
	if _, err := act(h, "set_fader", map[string]any{"strip": "ch:1", "db": 3}); err == nil || !strings.Contains(err.Error(), "VALUE ERROR") {
		t.Fatal(err)
	}
	// a write the console ignores is reported
	f.mu.Lock()
	f.dropSets = true
	f.mu.Unlock()
	if _, err := act(h, "mute", map[string]any{"strip": "ch:7", "mode": "on"}); err == nil || !strings.Contains(err.Error(), "didn't take") {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.dropSets = false
	f.mu.Unlock()
	// the OSC lock: refused with the reason
	f.desk("/$ctl/osc/ronly", int32(1))
	waitFor(t, "lock seen", 6*time.Second, func() bool { return strings.Contains(state(h)["statusBody"].(string), "OSC lock") })
	if _, err := act(h, "mute", map[string]any{"strip": "ch:7", "mode": "on"}); err == nil || !strings.Contains(err.Error(), "OSC lock") {
		t.Fatal(err)
	}
	if it := item(state(h), "mutes", "ch:7"); it["disabled"] != true {
		t.Fatal("buttons should be disabled under the lock")
	}
	f.desk("/$ctl/osc/ronly", int32(0))

	// unknown strip / action
	if _, err := act(h, "mute", map[string]any{"strip": "ch:41"}); err == nil {
		t.Fatal("ch:41 accepted")
	}
	if _, err := act(h, "nope", nil); err == nil {
		t.Fatal("unknown action")
	}
}

func TestWingMetersAndResources(t *testing.T) {
	f := newFakeWing(t)
	h := startWing(t, f, nil)
	waitFor(t, "native link", 5*time.Second, func() bool { return state(h)["transport"] == "native" })
	if req, _, _ := f.meterSub(); req != 0 {
		t.Fatal("meters asked for before any widget showed them")
	}
	var live liveData
	poll := func() {
		r, err := h.ResourceResult("w", "live?ids=ch:1,dca:2,main:1&meters=1&tap=post")
		if err != nil {
			t.Fatal(err)
		}
		if r.MaxAge == nil || *r.MaxAge != 0 || r.ContentType != "application/json" {
			t.Fatalf("maxAge %v %s", r.MaxAge, r.ContentType)
		}
		live = liveData{}
		if err := json.Unmarshal(r.Data, &live); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "meter levels", 5*time.Second, func() bool {
		poll()
		return len(live.Strips) == 3 && live.Strips[0].L != nil && live.Strips[1].L != nil && live.Meters.Running
	})
	if *live.Strips[0].L != -20 || live.Strips[0].Tag != "CH 1" || live.Strips[0].Name != "Pulpit" || *live.Strips[0].Gate != true {
		t.Fatalf("%+v", live.Strips[0])
	}
	if live.Strips[1].Kind != "dca" || live.Strips[1].Gate != nil {
		t.Fatalf("dca: %+v", live.Strips[1])
	}
	_, strips, on := f.meterSub()
	if !on || len(strips) != 3 {
		t.Fatalf("subscription %v %v", strips, on)
	}
	// binary form
	_, data, err := h.Resource("w", "meters?ids=ch:1,ch:2&fmt=bin")
	if err != nil || len(data) != 8 {
		t.Fatal(len(data), err)
	}
	// renewed while polled, beyond the console's 5 s
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) {
		poll()
		time.Sleep(300 * time.Millisecond)
	}
	if _, _, on := f.meterSub(); !on {
		t.Fatal("meter stream lapsed while a widget was polling")
	}
	// options for widget fields come from this connection
	opts := h.Options("wing.faders", "w")
	if len(opts) != len(faderStrips()) || opts[0].Value != "ch:1" || opts[0].Label != "CH 1 · Pulpit" {
		t.Fatalf("%v", opts[:2])
	}
	// state for one widget's settings
	st := h.State("w", map[string]any{"strips": []any{"ch:2", "ch:1"}})
	if l := st["mutes"].([]any); len(l) != 2 || l[0].(map[string]any)["id"] != "ch:2" {
		t.Fatalf("%v", st["mutes"])
	}
}

func TestWingMetersStop(t *testing.T) {
	old := meterIdle
	meterIdle = 500 * time.Millisecond
	defer func() { meterIdle = old }()
	f := newFakeWing(t)
	h := startWing(t, f, nil)
	waitFor(t, "native link", 5*time.Second, func() bool { return state(h)["transport"] == "native" })
	h.Resource("w", "live?ids=main:1&meters=1")
	waitFor(t, "subscribed", 3*time.Second, func() bool { _, _, on := f.meterSub(); return on })
	// no more polls: the add-on stops renewing and the console's 5 s lapse ends it
	waitFor(t, "stopped", 8*time.Second, func() bool { _, _, on := f.meterSub(); return !on })
}

func TestWingPollingFallback(t *testing.T) {
	f := newFakeWing(t)
	f.tcp.Close() // native port closed: polling over OSC
	h := startWing(t, f, nil)
	if tr := state(h)["transport"]; tr != "polling" {
		t.Fatal(tr)
	}
	f.desk("/ch/4/mute", int32(1))
	waitFor(t, "polled change", 5*time.Second, func() bool { return item(state(h), "mutes", "ch:4")["active"] == true })
	if !strings.Contains(state(h)["statusBody"].(string), "every 2 s") {
		t.Fatal(state(h)["statusBody"])
	}
}

func TestWingReadOnlyAndProbe(t *testing.T) {
	f := newFakeWing(t)
	h := startWing(t, f, map[string]any{"readOnly": true})
	for _, a := range []struct {
		name string
		p    map[string]any
	}{
		{"mute", map[string]any{"strip": "ch:1"}}, {"set_fader", map[string]any{"strip": "ch:1", "db": 0}},
		{"scene_go_next", nil}, {"rec_start", map[string]any{"confirmed": true}}, {"talkback", map[string]any{"mode": "on"}}, {"solo_clear", nil},
	} {
		if _, err := act(h, a.name, a.p); err == nil || !strings.Contains(err.Error(), "read only") {
			t.Errorf("%s: %v", a.name, err)
		}
	}
	if err := h.TryCall("w", "mixer.setMute", map[string]any{"channel": 1, "muted": true}, nil); err == nil {
		t.Fatal("setMute in read only")
	}
	r, err := act(h, "probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := r["report"].(string)
	for _, want := range []string{"[1] identity", ",sfi \"0\" 0 0", "f50f69f8", "$scenes: type 5, 5 items", "NODE NOT FOUND", "datagrams="} {
		if !strings.Contains(rep, want) {
			t.Errorf("probe report lacks %q:\n%s", want, rep)
		}
	}
	if n := f.setsCount(); n != 0 {
		t.Fatalf("probe or read-only wrote %d times", n)
	}
	if !strings.Contains(state(h)["probeReport"].(string), "[1] identity") {
		t.Fatal("probe report not in state")
	}
}

func TestWingOffline(t *testing.T) {
	ctx := context.Background()
	w, err := openWing(wingConfig{Host: "127.0.0.1", Port: 9, NativePort: 9}) // nothing answers
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	waitFor(t, "offline", 5*time.Second, func() bool {
		st, _ := w.AddonState(ctx)
		e, _ := st["error"].(string)
		return strings.Contains(e, "isn't answering")
	})
	if _, err := w.setMute(ctx, stripRef{"ch", 1}, "on"); err == nil {
		t.Fatal("write while offline")
	}
	if _, err := openWing(wingConfig{Host: " "}); err == nil {
		t.Fatal("blank host")
	}
	if _, err := openWing(wingConfig{Host: "h", MuteStrips: "ch:99"}); err == nil {
		t.Fatal("bad strip list")
	}
}

func TestManifest(t *testing.T) {
	m := spec().Manifest.LowestAPIVersion()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.APIVersion != 2 || m.Version != "2.0.0" {
		t.Fatal(m.APIVersion, m.Version)
	}
	var b bytes.Buffer
	json.NewEncoder(&b).Encode(m)
	for _, w := range m.Widgets {
		if w.Kind == addon.KindFrame && w.Entry == "" {
			t.Fatal(w.Type)
		}
	}
}

func TestWingChannelDetailAndFX(t *testing.T) {
	f := newFakeWing(t)
	h := startWing(t, f, nil)
	waitFor(t, "native link", 5*time.Second, func() bool { return state(h)["transport"] == "native" })
	get := func(path string, out any) {
		t.Helper()
		_, data, err := h.Resource("w", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	var d struct {
		Strip    stripView     `json:"strip"`
		Sections []sectionView `json:"sections"`
	}
	get("detail?strip=ch:3", &d)
	byPath := map[string]paramView{}
	var titles []string
	for _, s := range d.Sections {
		titles = append(titles, s.Title)
		for _, p := range s.Params {
			byPath[p.Path] = p
		}
	}
	if strings.Join(titles, ",") != "Input,Gate,EQ,Comp,Sends,Pan · inserts" {
		t.Fatal(titles)
	}
	for _, want := range []string{"/ch/3/in/set/trim", "/ch/3/in/set/$g", "/ch/3/flt/lcf", "/ch/3/eq/1g", "/ch/3/gate/thr", "/ch/3/dyn/ratio", "/ch/3/send/5/lvl", "/ch/3/send/MX2/on", "/ch/3/main/1/lvl", "/ch/3/pan", "/ch/3/postins/on"} {
		if !byPath[want].Writable {
			t.Errorf("%s missing or not writable: %+v", want, byPath[want])
		}
	}
	for _, never := range []string{"/ch/3/in/set/$vph", "/ch/3/in/conn/grp", "/ch/3/in/conn/in"} {
		if _, ok := byPath[never]; ok {
			t.Errorf("%s must never be shown", never)
		}
	}
	if p := byPath["/ch/3/eq/mdl"]; p.Writable || p.Text != "STD" {
		t.Errorf("model should be shown read only: %+v", p)
	}
	if p := byPath["/ch/3/flt/lcf"]; p.Kind != "float" || !p.Log || p.Unit != "" && p.Unit != "Hz" {
		t.Errorf("%+v", p)
	}
	if g := byPath["/ch/3/send/5/lvl"].Group; g != "BUS 5" {
		t.Errorf("send group %q", g)
	}
	// writes: read back, allowlist, ranges, enums
	var pv paramView
	if err := h.TryCall("w", "addon.action", map[string]any{"name": "set_param", "params": map[string]any{"path": "/ch/3/eq/1g", "value": 2.5}}, &pv); err != nil || pv.Value != 2.5 {
		t.Fatal(pv, err)
	}
	if f.get("/ch/3/eq/1g").f != 2.5 {
		t.Fatal("eq gain not set")
	}
	if _, err := act(h, "set_param", map[string]any{"path": "/ch/3/dyn/ratio", "value": "4.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(h, "set_param", map[string]any{"path": "/ch/3/eq/1g", "value": 40}); err == nil {
		t.Fatal("out of range accepted")
	}
	if _, err := act(h, "set_param", map[string]any{"path": "/ch/3/dyn/ratio", "value": "7"}); err == nil {
		t.Fatal("bad enum accepted")
	}
	sets := f.setsCount()
	for _, bad := range []string{"/ch/3/in/set/$vph", "/ch/3/in/conn/grp", "/ch/3/eq/mdl", "/ch/3/preins/ins", "/io/out/LCL/1/grp", "/$syscfg/tcplock", "/ch/41/eq/1g", "/fx/1/mdl"} {
		if _, err := act(h, "set_param", map[string]any{"path": bad, "value": 1}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if f.setsCount() != sets {
		t.Fatal("a refused path reached the console")
	}
	// gain shows on the strip
	var live liveData
	get("live?ids=ch:3,ch:9,bus:1,bus:2,main:1,mtx:1,dca:1", &live)
	st := map[string]stripView{}
	for _, s := range live.Strips {
		st[s.ID] = s
	}
	if st["ch:3"].Gain == nil || *st["ch:3"].Gain != 20 || st["bus:1"].Gain != nil {
		t.Fatalf("gain %+v %+v", st["ch:3"].Gain, st["bus:1"].Gain)
	}
	// stereo from the console: ch 9 has a stereo source, bus 2 is mono
	for id, want := range map[string]bool{"ch:3": false, "ch:9": true, "bus:1": true, "bus:2": false, "main:1": true, "mtx:1": true, "dca:1": false} {
		if st[id].Stereo != want {
			t.Errorf("%s stereo %v", id, st[id].Stereo)
		}
	}
	// a mute by DCA names it
	f.desk("/dca/1/name", "Band")
	if _, err := act(h, "mute", map[string]any{"strip": "dca:1", "mode": "on"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "mute reason", 2*time.Second, func() bool {
		get("live?ids=ch:7", &live)
		return live.Strips[0].EMuted && !live.Strips[0].Muted && live.Strips[0].MuteBy == "DCA 1 · Band"
	})
	// solo
	if r, err := act(h, "solo", map[string]any{"strip": "ch:2"}); err != nil || r["solo"] != true {
		t.Fatal(r, err)
	}
	// FX: model read only, parameters by the console's description, mix first
	var fx struct {
		Slots []struct {
			Slot   int         `json:"slot"`
			Model  string      `json:"model"`
			Params []paramView `json:"params"`
		} `json:"slots"`
	}
	get("fx?slots=1,2", &fx)
	if len(fx.Slots) != 2 || fx.Slots[0].Model != "HALL" || fx.Slots[1].Model != "ST-DL" {
		t.Fatalf("%+v", fx)
	}
	if fx.Slots[0].Params[0].Name != "fxmix" || fx.Slots[0].Params[1].Name != "dcy" || fx.Slots[1].Params[1].Name != "time" {
		t.Fatalf("order %v %v", fx.Slots[0].Params[:2], fx.Slots[1].Params[:2])
	}
	if _, err := act(h, "set_param", map[string]any{"path": "/fx/1/dcy", "value": 2.4}); err != nil {
		t.Fatal(err)
	}
	// a model changed at the desk: the new parameters are learned
	f.desk("/fx/1/mdl", "ST-DL")
	waitFor(t, "new model", 3*time.Second, func() bool {
		get("fx?slots=1", &fx)
		return fx.Slots[0].Model == "ST-DL" && len(fx.Slots[0].Params) > 1 && fx.Slots[0].Params[1].Name == "time"
	})
	f.desk("/fx/3/mdl", "EXT")
	get("fx?slots=3", &fx)
	for _, p := range fx.Slots[0].Params {
		if p.Name == "egrp" || p.Name == "ein" {
			t.Fatal("EXT routing shown")
		}
	}
	// options list each strip once
	opts := h.Options("wing.faders", "w")
	seen := map[string]bool{}
	for _, o := range opts {
		if seen[o.Value] {
			t.Fatal("duplicate", o.Value)
		}
		seen[o.Value] = true
	}
}

func TestWingNativeFull(t *testing.T) {
	f := newFakeWing(t)
	f.mu.Lock()
	f.maxClient = 0 // 24 clients already connected
	f.mu.Unlock()
	h := startWing(t, f, nil)
	time.Sleep(500 * time.Millisecond)
	if tr := state(h)["transport"]; tr != "polling" {
		t.Fatal(tr)
	}
	if _, err := act(h, "mute", map[string]any{"strip": "ch:1", "mode": "on"}); err != nil {
		t.Fatal("writes go over OSC and still work:", err)
	}
}
