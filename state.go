package main

// What the widgets show (addon.state), the live data frames poll
// (addon.resource "live"), and jadeck's built-in mixer capability.

import (
	"context"
	"encoding/binary"
	"fmt"
	"html"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// ------------------------------------------------- mixer capability

// MaxChannel: the core mute widgets number channels 1-48: CH 1-40, then
// AUX 1-8 as 41-48.
func (w *Wing) MaxChannel() int { return 48 }

func (w *Wing) Probe(ctx context.Context) (string, error) {
	id, err := w.hello(ctx)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("WING · %s (%s, firmware %s)", id.Name, id.ModelName(), id.Firmware)
	switch {
	case w.nativeLive():
		out += " · live updates"
	case w.isOnline():
		out += " · polling"
	}
	if lv, ok := w.val("lock.osc"); ok && lv.f >= 1 {
		out += " · OSC lock on (read only)"
	}
	if w.cfg.ReadOnly {
		out += " · read only"
	}
	return out, nil
}

// GetMute is the strip's own mute (what SetMute changes).
func (w *Wing) GetMute(ctx context.Context, ch int) (bool, error) {
	s, err := channelStrip(ch)
	if err != nil {
		return false, err
	}
	return w.ownMute(ctx, s)
}

func (w *Wing) SetMute(ctx context.Context, ch int, muted bool) error {
	s, err := channelStrip(ch)
	if err != nil {
		return err
	}
	mode := "off"
	if muted {
		mode = "on"
	}
	_, err = w.setMute(ctx, s, mode)
	return err
}

// ------------------------------------------------------------- state

func (w *Wing) AddonState(ctx context.Context) (map[string]any, error) {
	w.mu.Lock()
	online, ever, lastErr, id := w.online, w.everOnline, w.lastErr, w.ident
	scenes := append([]string(nil), w.scenes...)
	probe := w.probeReport
	w.mu.Unlock()
	out := map[string]any{
		"connected": online, "readOnly": w.cfg.ReadOnly, "transport": w.transportName(),
		"consoleName": id.Name, "model": id.ModelName(), "firmware": id.Firmware,
		"defaultFaders": ids(w.faderL), "defaultMeters": ids(w.meterL), "defaultDcas": ids(w.dcaL),
		"probeReport":  probe,
		"stripOptions": stripOptions(w, false),
	}
	if probe == "" {
		out["probeReport"] = "Not run yet. Press “Run probe” (it only reads)."
	}
	if !online {
		msg := "Connecting to the WING at " + w.cfg.Host + "…"
		if lastErr != "" {
			msg = "WING at " + w.cfg.Host + ": " + lastErr + ". Retrying…"
		}
		if ever || lastErr != "" {
			out["error"] = msg
		}
		out["statusBody"] = html.EscapeString(msg)
		return out, nil
	}
	locked := w.lockText()
	disabled := w.cfg.ReadOnly || locked != ""
	muteItems := w.muteItems

	// Mute grid, DCA mutes, mute groups.
	out["mutes"] = muteItems(w.muteL)
	out["dcaMutes"] = muteItems(w.dcaL)
	var mg []stripRef
	for n := 1; n <= 8; n++ {
		mg = append(mg, stripRef{"mgrp", n})
	}
	out["muteGroups"] = muteItems(mg)

	// Main fader (slider widget: value 0..1, param "main:1").
	mv := w.view(stripRef{"main", 1})
	if mv.Pos != nil {
		out["mainPos"] = *mv.Pos
		out["mainLabel"] = mv.Label + " · " + dbText(*mv.DB)
	} else {
		out["mainPos"], out["mainLabel"] = 0.0, mv.Label
	}

	// Scenes.
	cur := 0
	if f, ok := w.num("/$ctl/lib/$actidx"); ok {
		cur = int(f)
	}
	show := w.str("/$ctl/lib/$actshow")
	items := []map[string]any{}
	for i, n := range scenes {
		big, small, _ := strings.Cut(n, "~")
		it := map[string]any{"id": fmt.Sprint(i + 1), "title": fmt.Sprintf("%d · %s", i+1, big), "active": i+1 == cur}
		if small != "" {
			it["sub"] = small
		}
		items = append(items, it)
	}
	showTitle := strings.TrimSpace(show)
	if i := strings.LastIndexAny(showTitle, "/:\\"); i >= 0 {
		showTitle = showTitle[i+1:]
	}
	if showTitle == "" {
		showTitle = "Show"
	}
	sections := []map[string]any{}
	if len(items) > 0 {
		sections = append(sections, map[string]any{"id": "show", "title": showTitle, "meta": fmt.Sprintf("%d scenes", len(items)), "items": items})
	}
	out["scenes"] = sections
	out["sceneCurrent"] = fmt.Sprint(cur)
	curName := w.sceneNameOf(scenes, cur)
	out["sceneText"] = map[bool]string{true: "Scene " + fmt.Sprint(cur) + ": " + curName, false: "No scene loaded"}[cur > 0]
	out["sceneButtons"] = []map[string]any{
		{"id": "prev", "label": "◀ Previous", "action": "scene_go_prev", "disabled": disabled || len(scenes) == 0},
		{"id": "next", "label": "Next ▶", "action": "scene_go_next", "disabled": disabled || len(scenes) == 0},
	}

	// USB recorder.
	usb := strings.ToUpper(w.str("/$stat/usbstate"))
	usbOK := usb == "IDLE" || usb == "BUSY" || usb == ""
	rs := strings.ToUpper(w.str("/rec/$actstate"))
	rt, _ := w.num("/rec/$time")
	recLabel := "● Rec"
	if rs == "REC" {
		recLabel = "● " + clock(rt)
	}
	hold := map[string]any{"confirmed": true}
	out["recButtons"] = []map[string]any{
		{"id": "rec", "label": recLabel, "action": "rec_start", "tone": map[bool]string{true: "pgm"}[rs == "REC"], "active": rs == "REC", "disabled": disabled || !usbOK,
			"hold": defaultHold, "holdLabel": "Keep holding to record…", "params": hold},
		{"id": "pause", "label": "Pause", "action": "rec_pause", "active": rs == "PAUSE", "disabled": disabled || rs == "STOP" || rs == ""},
		{"id": "stop", "label": "Stop", "action": "rec_stop", "disabled": disabled || rs == "STOP" || rs == "",
			"hold": defaultHold, "holdLabel": "Keep holding to stop…", "params": hold},
		{"id": "newfile", "label": "New file", "action": "rec_new_file", "disabled": disabled || rs != "REC",
			"hold": defaultHold, "holdLabel": "Keep holding for a new file…", "params": hold},
	}
	recWord := map[string]string{"REC": "Recording", "PAUSE": "Paused", "STOP": "Stopped", "ERROR": "Error", "": "Stopped"}[rs]
	if recWord == "" {
		recWord = rs
	}
	recBody := fmt.Sprintf("<b>%s</b> · %s", html.EscapeString(recWord), clock(rt))
	if f := w.str("/rec/$actfile"); f != "" {
		recBody += "<br>" + html.EscapeString(f)
	}
	recBody += "<br>USB: " + html.EscapeString(nz(w.str("/$stat/usbstate"), "?"))
	if v := w.str("/$stat/usbvolname"); v != "" {
		recBody += " (" + html.EscapeString(v) + ")"
	}
	out["recText"] = recBody

	// USB player.
	ps := strings.ToUpper(w.str("/play/$actstate"))
	pos, _ := w.num("/play/$pos")
	tot, _ := w.num("/play/$total")
	out["playButtons"] = []map[string]any{
		{"id": "prev", "label": "⏮", "action": "play_prev", "disabled": disabled},
		{"id": "play", "label": "▶ Play", "action": "play_play", "active": ps == "PLAY", "tone": map[bool]string{true: "pvw"}[ps == "PLAY"], "disabled": disabled},
		{"id": "pause", "label": "Pause", "action": "play_pause", "active": ps == "PAUSE", "disabled": disabled || ps != "PLAY"},
		{"id": "stop", "label": "Stop", "action": "play_stop", "disabled": disabled || ps == "STOP" || ps == ""},
		{"id": "next", "label": "⏭", "action": "play_next", "disabled": disabled},
	}
	song := nz(w.str("/play/$song"), w.str("/play/$actfile"))
	pb := fmt.Sprintf("<b>%s</b> · %s / %s", html.EscapeString(nz(map[string]string{"PLAY": "Playing", "PAUSE": "Paused", "STOP": "Stopped"}[ps], nz(ps, "Stopped"))), clock(pos), clock(tot))
	if song != "" {
		pb += "<br>" + html.EscapeString(song)
		if a := w.str("/play/$artist"); a != "" {
			pb += " · " + html.EscapeString(a)
		}
	}
	out["playText"] = pb

	// Talkback.
	tb := func(bus string) map[string]any {
		on, _ := w.num("/cfg/talk/" + bus + "/$on")
		return map[string]any{"id": bus, "label": "Talkback " + bus, "action": "talkback", "active": on >= 1,
			"disabled": disabled, "params": map[string]any{"bus": bus, "mode": "toggle"}}
	}
	out["talkButtons"] = []map[string]any{tb("A"), tb("B")}

	// Solo.
	soloOn, _ := w.num("/$stat/solo")
	out["soloButtons"] = []map[string]any{{"id": "clear", "label": map[bool]string{true: "Clear solo", false: "No solo"}[soloOn >= 1],
		"action": "solo_clear", "tone": map[bool]string{true: "pgm"}[soloOn >= 1], "disabled": disabled || soloOn < 1}}

	// Console status.
	out["statusBody"] = w.statusHTML(id, locked)
	return out, nil
}

// AddonStateFor is AddonState for one widget: a widget's own "strips"
// setting replaces the connection's default list for the lists it shows.
func (w *Wing) AddonStateFor(ctx context.Context, settings map[string]any) (map[string]any, error) {
	out, err := w.AddonState(ctx)
	if err != nil || settings == nil || !w.isOnline() {
		return out, err
	}
	if hs, ok := settings["holdSeconds"].(float64); ok {
		secs := int(math.Round(math.Max(3, math.Min(10, hs))))
		for _, b := range out["recButtons"].([]map[string]any) {
			if _, held := b["hold"]; held {
				b["hold"] = secs
			}
		}
	}
	if pick, ok := settings["buttons"].([]any); ok && len(pick) > 0 {
		keep := map[string]bool{}
		for _, x := range pick {
			if s, ok := x.(string); ok {
				keep[s] = true
			}
		}
		for _, key := range []string{"recButtons", "playButtons"} {
			l, _ := out[key].([]map[string]any)
			var f []map[string]any
			for _, b := range l {
				if keep[b["id"].(string)] {
					f = append(f, b)
				}
			}
			if f != nil {
				out[key] = f
			}
		}
	}
	if l := stripListOf(settings["strips"]); len(l) > 0 {
		out["mutes"] = w.muteItems(l)
		var dcas []stripRef
		for _, s := range l {
			if s.Kind == "dca" {
				dcas = append(dcas, s)
			}
		}
		if len(dcas) > 0 {
			out["dcaMutes"] = w.muteItems(dcas)
		}
	}
	return out, nil
}

func (w *Wing) lockText() string {
	if lv, ok := w.val("lock.osc"); ok && lv.f >= 1 {
		return "OSC lock on"
	}
	return ""
}

// muteItems are mute buttons: red when the strip is effectively muted
// (own, DCA or mute group), "(group)" when muted only by a DCA or group.
func (w *Wing) muteItems(list []stripRef) []map[string]any {
	disabled := w.cfg.ReadOnly || w.lockText() != ""
	out := []map[string]any{}
	for _, s := range list {
		v := w.view(s)
		lbl := v.Label
		if v.EMuted && !v.Muted {
			lbl += " (group)"
		}
		tone := ""
		if v.EMuted {
			tone = "pgm"
		}
		out = append(out, map[string]any{"id": s.ID(), "label": lbl, "tone": tone, "active": v.Muted,
			"disabled": disabled, "color": v.Color, "params": map[string]any{"strip": s.ID(), "mode": "toggle"}})
	}
	return out
}

// defaultHold is how long (seconds) record / stop / new file are held.
const defaultHold = 5

func (w *Wing) sceneNameOf(scenes []string, i int) string {
	if i >= 1 && i <= len(scenes) {
		big, _, _ := strings.Cut(scenes[i-1], "~")
		return big
	}
	return w.str("/$ctl/lib/$active")
}

func (w *Wing) transportName() string {
	switch {
	case !w.isOnline():
		return "offline"
	case w.nativeLive():
		return "native"
	}
	return "polling"
}

func (w *Wing) statusHTML(id identity, locked string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b> · %s · firmware %s<br>", html.EscapeString(nz(id.Name, "WING")), html.EscapeString(id.ModelName()), html.EscapeString(nz(id.Firmware, "?")))
	switch w.transportName() {
	case "native":
		b.WriteString("Live updates from the console")
	default:
		w.mu.Lock()
		e := w.native.lastErr
		w.mu.Unlock()
		b.WriteString("Checking every 2 s (native link: " + html.EscapeString(nz(e, "not connected")) + ")")
	}
	var locks []string
	if locked != "" {
		locks = append(locks, "OSC lock on: jadeck can't change anything")
	}
	if v, ok := w.num("/$syscfg/tcplock"); ok && v >= 1 {
		locks = append(locks, "TCP lock on")
	}
	if s := strings.TrimSpace(w.str("/$ctl/$stat/cnslock")); s != "" && s != "0" && !strings.EqualFold(s, "off") {
		locks = append(locks, "surface lock: "+s)
	}
	if w.cfg.ReadOnly {
		locks = append(locks, "this connection is read only")
	}
	if len(locks) > 0 {
		b.WriteString("<br><b>" + html.EscapeString(strings.Join(locks, " · ")) + "</b>")
	} else {
		b.WriteString("<br>No locks")
	}
	if u := w.str("/$stat/usbstate"); u != "" {
		b.WriteString("<br>USB: " + html.EscapeString(u))
	}
	if m := w.meters.running(); m {
		fmt.Fprintf(&b, "<br>Meters: on (%.0f Hz)", w.meters.rateHz())
	} else if w.cfg.Meters == "off" {
		b.WriteString("<br>Meters: off in settings")
	}
	return b.String()
}

func ids(l []stripRef) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = s.ID()
	}
	return out
}

func nz(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// clock shows seconds as m:ss or h:mm:ss.
func clock(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	t := int(sec)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// ---------------------------------------------------------- resources

// Resource serves the live data frames poll:
//
//	live?ids=ch:1,dca:2&meters=1&tap=post   JSON: strips + meters
//	meters?ids=ch:1,ch:3&fmt=bin            int16 BE pairs (L, R) in 1/256 dB
//	                                        (-32768 = no data), in id order
//
// Polling marks the strips "on screen": they're kept fresh (polled when
// the native link is down) and their meters run while asked for.
//
// Answers are never cached by the controller (maxAge 0); it still shares
// one call between screens asking within 50 ms.
func (w *Wing) ResourceResult(ctx context.Context, p string) (addon.ResourceResult, error) {
	ct, data, err := w.Resource(ctx, p)
	if err != nil {
		return addon.ResourceResult{}, err
	}
	return addon.ResourceResult{ContentType: ct, Data: data, MaxAge: addon.Age(0)}, nil
}

// Resource is ResourceResult for controllers before v2.1 (cached 2 s
// there, so frames add a time bucket "&t=…" to the path).
func (w *Wing) Resource(ctx context.Context, p string) (string, []byte, error) {
	name, query, _ := strings.Cut(p, "?")
	q, _ := url.ParseQuery(query)
	var list []stripRef
	for _, part := range strings.Split(q.Get("ids"), ",") {
		if s, err := parseStrip(part); err == nil && strings.TrimSpace(part) != "" {
			list = append(list, s)
		}
		if len(list) >= 96 {
			break
		}
	}
	tap := q.Get("tap")
	switch name {
	case "detail":
		s, err := parseStrip(q.Get("strip"))
		if err != nil {
			return "", nil, err
		}
		if !w.isOnline() {
			return "application/json", []byte(jsonOf(map[string]any{"ok": false, "connected": false, "error": errOffline.Error()})), nil
		}
		w.markHot([]stripRef{s})
		if q.Get("meters") == "1" {
			w.meters.want([]stripRef{s})
		}
		d, err := w.detail(ctx, s)
		if err != nil {
			return "", nil, err
		}
		if q.Get("meters") == "1" {
			d["strip"] = w.live([]stripRef{s}, true, tap).Strips[0]
		}
		return "application/json", []byte(jsonOf(d)), nil
	case "fx":
		var slots []int
		for _, part := range strings.Split(q.Get("slots"), ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n >= 1 && n <= 16 && len(slots) < 16 {
				slots = append(slots, n)
			}
		}
		if !w.isOnline() {
			return "application/json", []byte(jsonOf(map[string]any{"ok": false, "connected": false, "error": errOffline.Error()})), nil
		}
		return "application/json", []byte(jsonOf(w.fxSlots(ctx, slots))), nil
	case "live":
		withMeters := q.Get("meters") == "1"
		w.markHot(list)
		if withMeters {
			w.meters.want(list)
		}
		return "application/json", []byte(jsonOf(w.live(list, withMeters, tap))), nil
	case "meters":
		w.meters.want(list)
		if q.Get("fmt") == "bin" {
			b := make([]byte, 0, 4*len(list))
			for _, s := range list {
				l, r := int16(math.MinInt16), int16(math.MinInt16)
				if m, ok := w.meters.get(s); ok {
					ml, mr := tapOf(m, tap, s)
					l, r = int16(math.Max(-32767, math.Min(32767, ml*256))), int16(math.Max(-32767, math.Min(32767, mr*256)))
				}
				b = binary.BigEndian.AppendUint16(b, uint16(l))
				b = binary.BigEndian.AppendUint16(b, uint16(r))
			}
			return "application/octet-stream", b, nil
		}
		return "application/json", []byte(jsonOf(w.live(list, true, tap))), nil
	}
	return "", nil, addon.ErrNotFound
}

func (w *Wing) markHot(list []stripRef) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	for _, s := range list {
		w.hot[s] = now
	}
}

// tapOf picks pre- or post-fader levels: strips have in (pre) and out
// (post) pairs, DCAs pre and post.
func tapOf(m meterVal, tap string, s stripRef) (float64, float64) {
	if tap == "pre" {
		return m.inL, m.inR
	}
	return m.outL, m.outR
}

type liveData struct {
	OK        bool        `json:"ok"`
	Connected bool        `json:"connected"`
	Transport string      `json:"transport"`
	ReadOnly  bool        `json:"readOnly"`
	Locked    string      `json:"locked,omitempty"`
	Error     string      `json:"error,omitempty"`
	Meters    meterStatus `json:"meters"`
	Strips    []stripView `json:"strips"`
}

type meterStatus struct {
	Running bool    `json:"running"`
	Hz      float64 `json:"hz,omitempty"`
	Note    string  `json:"note,omitempty"`
}

func (w *Wing) live(list []stripRef, withMeters bool, tap string) liveData {
	d := liveData{OK: true, Connected: w.isOnline(), Transport: w.transportName(), ReadOnly: w.cfg.ReadOnly, Strips: []stripView{}}
	if !d.Connected {
		w.mu.Lock()
		d.Error = nz(w.lastErr, "connecting…")
		w.mu.Unlock()
	}
	if lv, ok := w.val("lock.osc"); ok && lv.f >= 1 {
		d.Locked = "OSC lock is on at the console"
	}
	if withMeters {
		d.Meters.Running = w.meters.running()
		d.Meters.Hz = math.Round(w.meters.rateHz()*10) / 10
		w.meters.mu.Lock()
		d.Meters.Note = w.meters.note
		w.meters.mu.Unlock()
		if w.cfg.Meters == "off" {
			d.Meters.Note = "Meters are off in the connection's settings"
		}
	}
	for _, s := range list {
		v := w.view(s)
		if withMeters {
			if m, ok := w.meters.get(s); ok {
				l, r := tapOf(m, tap, s)
				v.L, v.R = fp(math.Round(l*10)/10), fp(math.Round(r*10)/10)
				if _, isStrip := kinds[s.Kind]; isStrip && s.Kind != "dca" {
					g, dy := m.gate, m.dyn
					v.Gate, v.Dyn, v.GR = &g, &dy, fp(math.Round(m.gr*10)/10)
				}
			}
		}
		d.Strips = append(d.Strips, v)
	}
	return d
}
