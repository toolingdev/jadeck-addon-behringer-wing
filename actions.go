package main

// The add-on's actions (buttons, macro steps and agent tools at once).
// Every write is read back from the console; see Wing.write.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// confirmField: risky actions (starting / stopping a recording) run only
// with confirmed: true, which the held button sends (WIDGETS-V2.md "Hold
// to confirm"); a tap or a stray call does nothing.
var confirmField = addon.Field{Key: "confirmed", Label: "Confirmed", Type: "bool", Required: true, Default: false,
	Help: "Must be on: this starts or stops a recording."}

// errNotConfirmed: a risky action without confirmed: true.
var errNotConfirmed = errors.New("hold the button to confirm (or pass confirmed: true): this starts or stops a recording")

func (p params) confirmed() bool {
	switch v := p["confirmed"].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	case float64:
		return v == 1
	}
	return false
}

var stripHelp = "ch:1-40, aux:1-8, bus:1-16, main:1-4, mtx:1-8, dca:1-16, mgrp:1-8"

func actionDefs() []addon.ActionDef {
	strip := addon.Field{Key: "strip", Label: "Strip", Type: "text", Required: true, Placeholder: "ch:3", Help: stripHelp, OptionsFrom: "wing.strips"}
	mode := addon.Field{Key: "mode", Label: "Mute", Type: "select", Default: "toggle", Options: []addon.Option{
		{Value: "toggle", Label: "Toggle"}, {Value: "on", Label: "Mute"}, {Value: "off", Label: "Unmute"}}}
	onOff := addon.Field{Key: "mode", Label: "Turn", Type: "select", Default: "toggle", Options: []addon.Option{
		{Value: "toggle", Label: "Toggle"}, {Value: "on", Label: "On"}, {Value: "off", Label: "Off"}}}
	return []addon.ActionDef{
		{ID: "mute", Label: "Mute / unmute a strip", MacroStep: true,
			Description: "Mute, unmute or toggle a channel, aux, bus, main, matrix, DCA or mute group (its own mute button).",
			Params:      []addon.Field{strip, mode}},
		{ID: "set_fader", Label: "Set a fader", MacroStep: true,
			Description: "Set a strip's fader in dB (-144 = -inf, up to +10), or as a 0-1 position.",
			Params: []addon.Field{strip,
				{Key: "db", Label: "Level (dB)", Type: "number", Min: addon.Num(-144), Max: addon.Num(10)},
				{Key: "pos", Label: "or position (0-1)", Type: "number", Min: addon.Num(0), Max: addon.Num(1), Advanced: true}}},
		{ID: "solo", Label: "Solo a strip", MacroStep: true, Description: "Solo on, off or toggle (monitoring only).",
			Params: []addon.Field{strip, onOff}},
		{ID: "mute_group", Label: "Mute group", MacroStep: true, Description: "Mute, unmute or toggle a mute group.",
			Params: []addon.Field{{Key: "group", Label: "Group (1-8)", Type: "number", Required: true, Min: addon.Num(1), Max: addon.Num(8)}, mode}},
		{ID: "scene_go", Label: "Go to scene…", MacroStep: true,
			Description: "Load a scene of the open show by its number in the list (1 = first). Scenes can change layers and custom controls unless they're safed.",
			Params:      []addon.Field{{Key: "index", Label: "Scene number", Type: "number", Required: true, Min: addon.Num(1)}}},
		{ID: "scene_go_tag", Label: "Go to scene by tag…", MacroStep: true,
			Description: "Load the scene tagged #n (doesn't depend on the list's order).",
			Params:      []addon.Field{{Key: "tag", Label: "Tag number", Type: "number", Required: true, Min: addon.Num(1), Max: addon.Num(128)}}},
		{ID: "scene_go_next", Label: "Next scene", MacroStep: true, Description: "Load the next scene of the show."},
		{ID: "scene_go_prev", Label: "Previous scene", MacroStep: true, Description: "Load the previous scene of the show."},
		{ID: "rec_start", Label: "Record (USB)", MacroStep: true, Description: "Start the USB recorder. The button is held to confirm; macros and the agent pass confirmed.",
			Params: []addon.Field{confirmField}},
		{ID: "rec_pause", Label: "Pause recording", MacroStep: true},
		{ID: "rec_stop", Label: "Stop recording", MacroStep: true, Description: "Stop the USB recorder (held to confirm).", Params: []addon.Field{confirmField}},
		{ID: "rec_new_file", Label: "Recorder: new file", MacroStep: true, Description: "Continue recording into a new file (held to confirm).",
			Params: []addon.Field{confirmField}},
		{ID: "play_play", Label: "Player: play", MacroStep: true},
		{ID: "play_pause", Label: "Player: pause", MacroStep: true},
		{ID: "play_stop", Label: "Player: stop", MacroStep: true},
		{ID: "play_next", Label: "Player: next song", MacroStep: true},
		{ID: "play_prev", Label: "Player: previous song", MacroStep: true},
		{ID: "play_seek", Label: "Player: go to time…", MacroStep: true,
			Params: []addon.Field{{Key: "seconds", Label: "Seconds", Type: "number", Required: true, Min: addon.Num(0)}}},
		{ID: "talkback", Label: "Talkback", MacroStep: true, Description: "Turn talkback A or B on or off.",
			Params: []addon.Field{{Key: "bus", Label: "Talkback", Type: "select", Default: "A", Options: []addon.Option{{Value: "A", Label: "A"}, {Value: "B", Label: "B"}}}, onOff}},
		{ID: "set_param", Label: "Set a channel or FX parameter",
			Description: "Set one processing parameter by its console path, e.g. /ch/3/eq/1g to 2.5 or /ch/3/eq/on to 1. " +
				"Only levels, EQ, gate, compressor, filters, trim/gain, polarity, pan, sends and FX parameters; never phantom power or patching.",
			Params: []addon.Field{{Key: "path", Label: "Path", Type: "text", Required: true, Placeholder: "/ch/3/eq/1g"},
				{Key: "value", Label: "Value", Type: "text", Required: true, Help: "A number, 0/1 for switches, or a choice's name."}}},
		{ID: "solo_clear", Label: "Clear solo", MacroStep: true, Description: "Turn off every solo on the console."},
		{ID: "refresh", Label: "Re-read the console", Description: "Read every name, colour, level and the scene list again."},
		{ID: "probe", Label: "Probe the console (read only)",
			Description: "Reads (never writes) what this add-on depends on and reports it, to confirm details on a real console."},
	}
}

// params is an action's parameters, loosely typed (widgets send numbers
// as text and the other way round).
type params map[string]any

func (p params) str(k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return trimFloat(v)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (p params) num(k string) (float64, bool) {
	switch v := p[k].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func (p params) strip() (stripRef, error) {
	s := p.str("strip")
	if s == "" {
		s = p.str("id")
	}
	if s == "" {
		return stripRef{}, errors.New("which strip? (strip: e.g. ch:3)")
	}
	return parseStrip(s)
}

// stripValue finds a slider's value sent under the strip's own name
// ({"main:1": 0.7}): a slider widget can only name its param.
func (p params) stripValue() (stripRef, float64, bool) {
	for k := range p {
		if k == "strip" || k == "db" || k == "pos" || k == "mode" {
			continue
		}
		s, err := parseStrip(k)
		if err != nil {
			continue
		}
		if v, ok := p.num(k); ok {
			return s, v, true
		}
	}
	return stripRef{}, 0, false
}

// AddonAction runs one of the add-on's actions.
func (w *Wing) AddonAction(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	p := params{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("bad parameters: %w", err)
		}
	}
	switch name {
	case "mute":
		s, err := p.strip()
		if err != nil {
			return nil, err
		}
		muted, err := w.setMute(ctx, s, p.str("mode"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"strip": s.ID(), "muted": muted}, nil
	case "solo":
		s, err := p.strip()
		if err != nil {
			return nil, err
		}
		if s.Kind == "mgrp" {
			return nil, errors.New("mute groups have no solo")
		}
		path := s.Path() + "/$solo"
		want := true
		switch strings.ToLower(p.str("mode")) {
		case "", "toggle":
			cur, err := w.osc.get(ctx, path)
			if err != nil {
				return nil, err
			}
			want = cur.f < 1
		case "on", "1", "true":
		case "off", "0", "false":
			want = false
		default:
			return nil, errors.New("mode must be on, off or toggle")
		}
		iv := int32(0)
		if want {
			iv = 1
		}
		if _, err := w.write(ctx, path, iv, "", 0, func(v value) bool { return (v.f >= 1) == want }); err != nil {
			return nil, err
		}
		w.refresh(ctx, "/$stat/solo")
		return map[string]any{"strip": s.ID(), "solo": want}, nil
	case "mute_group":
		g, ok := p.num("group")
		if !ok || g < 1 || g > 8 || g != math.Trunc(g) {
			return nil, errors.New("group must be 1-8")
		}
		s := stripRef{"mgrp", int(g)}
		muted, err := w.setMute(ctx, s, p.str("mode"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"strip": s.ID(), "muted": muted}, nil
	case "set_fader":
		var s stripRef
		var db float64
		if sv, v, ok := p.stripValue(); ok && p["strip"] == nil {
			s, db = sv, posToDB(v) // slider widgets send a 0-1 position
		} else {
			var err error
			if s, err = p.strip(); err != nil {
				return nil, err
			}
			if d, ok := p.num("db"); ok {
				db = d
			} else if pos, ok := p.num("pos"); ok {
				db = posToDB(pos)
			} else {
				return nil, errors.New("give db (-144..10) or pos (0..1)")
			}
		}
		got, err := w.setFader(ctx, s, db)
		if err != nil {
			return nil, err
		}
		return map[string]any{"strip": s.ID(), "db": got, "text": dbText(got)}, nil
	case "scene_go":
		i, ok := p.num("index")
		if !ok || i < 1 || i != math.Trunc(i) {
			return nil, errors.New("index must be a scene number (1 = first)")
		}
		return w.sceneAction(ctx, int(i), "GO")
	case "scene_go_tag":
		t, ok := p.num("tag")
		if !ok || t < 1 || t > 128 || t != math.Trunc(t) {
			return nil, errors.New("tag must be 1-128")
		}
		return w.sceneAction(ctx, int(t), "GOTAG")
	case "scene_go_next":
		return w.sceneAction(ctx, 0, "GONEXT")
	case "scene_go_prev":
		return w.sceneAction(ctx, 0, "GOPREV")
	case "rec_start", "rec_stop", "rec_new_file":
		if !p.confirmed() {
			return nil, errNotConfirmed
		}
	}
	switch name {
	case "rec_start":
		return w.transport(ctx, "/rec", "REC", "REC")
	case "rec_pause":
		return w.transport(ctx, "/rec", "PAUSE", "PAUSE")
	case "rec_stop":
		return w.transport(ctx, "/rec", "STOP", "STOP")
	case "rec_new_file":
		return w.transport(ctx, "/rec", "NEWFILE", "")
	case "play_play":
		return w.transport(ctx, "/play", "PLAY", "PLAY")
	case "play_pause":
		return w.transport(ctx, "/play", "PAUSE", "PAUSE")
	case "play_stop":
		return w.transport(ctx, "/play", "STOP", "STOP")
	case "play_next":
		return w.transport(ctx, "/play", "NEXT", "")
	case "play_prev":
		return w.transport(ctx, "/play", "PREV", "")
	case "play_seek":
		sec, ok := p.num("seconds")
		if !ok || sec < 0 || sec > 35999 {
			return nil, errors.New("seconds must be 0-35999")
		}
		v, err := w.write(ctx, "/play/$pos", float32(sec), "", 50*time.Millisecond, func(v value) bool { return math.Abs(v.f-sec) < 2.5 })
		if err != nil {
			return nil, err
		}
		return map[string]any{"pos": v.f}, nil
	case "talkback":
		bus := strings.ToUpper(p.str("bus"))
		if bus == "" {
			bus = "A"
		}
		if bus != "A" && bus != "B" {
			return nil, errors.New("bus must be A or B")
		}
		path := "/cfg/talk/" + bus + "/$on"
		want := true
		switch strings.ToLower(p.str("mode")) {
		case "", "toggle":
			cur, err := w.osc.get(ctx, path)
			if err != nil {
				return nil, err
			}
			want = cur.f < 1
		case "on", "1", "true":
		case "off", "0", "false":
			want = false
		default:
			return nil, errors.New("mode must be on, off or toggle")
		}
		iv := int32(0)
		if want {
			iv = 1
		}
		if _, err := w.write(ctx, path, iv, "", 0, func(v value) bool { return (v.f >= 1) == want }); err != nil {
			return nil, err
		}
		return map[string]any{"talkback": bus, "on": want}, nil
	case "solo_clear":
		return w.soloClear(ctx)
	case "set_param":
		path := p.str("path")
		v := p["value"]
		if v == nil {
			return nil, errors.New("no value")
		}
		pv, err := w.setParam(ctx, path, v)
		if err != nil {
			return nil, err
		}
		return pv, nil
	case "refresh":
		if !w.isOnline() {
			return nil, errOffline
		}
		if err := w.fullSync(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "probe":
		rep := w.probe(ctx)
		w.mu.Lock()
		w.probeReport = rep
		w.dirty = true
		w.mu.Unlock()
		return map[string]any{"report": rep}, nil
	}
	return nil, addon.ErrUnknownAction
}

// sceneAction drives show control (/$ctl/lib, p.90): select with
// $actionidx, then $action. NEXT/PREV want $actionidx 0 first.
func (w *Wing) sceneAction(ctx context.Context, idx int, action string) (any, error) {
	if w.cfg.ReadOnly {
		return nil, errReadOnly
	}
	if _, err := w.write(ctx, "/$ctl/lib/$actionidx", int32(idx), "", 0, func(v value) bool { return int(v.f) == idx }); err != nil {
		return nil, err
	}
	if _, err := w.write(ctx, "/$ctl/lib/$action", action, "/$ctl/lib/$actidx", 400*time.Millisecond, nil); err != nil {
		return nil, err
	}
	w.refresh(ctx, "/$ctl/lib/$active", "/$ctl/lib/$activeid", "/$ctl/lib/$actshow")
	cur, _ := w.num("/$ctl/lib/$actidx")
	if action == "GO" && int(cur) != idx {
		return nil, fmt.Errorf("asked for scene %d, the console shows scene %d (is a show open with that many scenes?)", idx, int(cur))
	}
	return map[string]any{"scene": int(cur), "name": w.sceneName(int(cur))}, nil
}

func (w *Wing) sceneName(i int) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if i >= 1 && i <= len(w.scenes) {
		return w.scenes[i-1]
	}
	return ""
}

// transport sends a recorder / player action and checks $actstate.
func (w *Wing) transport(ctx context.Context, node, action, expect string) (any, error) {
	if node == "/rec" && (action == "REC" || action == "NEWFILE") {
		w.refresh(ctx, "/$stat/usbstate")
		if st := strings.ToUpper(w.str("/$stat/usbstate")); st == "-" || st == "ERR" {
			return nil, fmt.Errorf("no USB stick ready on the console (USB: %s)", w.str("/$stat/usbstate"))
		}
	}
	var ok func(value) bool
	if expect != "" {
		ok = func(v value) bool { return strings.EqualFold(v.s, expect) }
	}
	v, err := w.write(ctx, node+"/$action", action, node+"/$actstate", 300*time.Millisecond, ok)
	if err != nil {
		if expect != "" && v.s != "" && !errors.Is(err, errOSCLock) && !errors.Is(err, errReadOnly) {
			return nil, fmt.Errorf("asked for %s, the %s is %s", action, map[string]string{"/rec": "recorder", "/play": "player"}[node], v.s)
		}
		return nil, err
	}
	paths := []string{node + "/$actfile"}
	if node == "/rec" {
		paths = append(paths, "/rec/$time")
	} else {
		paths = append(paths, "/play/$song", "/play/$artist", "/play/$pos", "/play/$total")
	}
	w.refresh(ctx, paths...)
	return map[string]any{"state": v.s}, nil
}

// soloClear turns off every strip's solo (there is no single "clear
// solo" parameter, p.35).
func (w *Wing) soloClear(ctx context.Context) (any, error) {
	if w.cfg.ReadOnly {
		return nil, errReadOnly
	}
	var paths []string
	for _, s := range faderStrips() {
		paths = append(paths, s.Path()+"/$solo")
	}
	w.refresh(ctx, paths...) // be sure, solos change at the desk
	n := 0
	for _, p := range paths {
		if v, ok := w.val(p); ok && v.f >= 1 {
			if _, err := w.write(ctx, p, int32(0), "", 0, func(v value) bool { return v.f < 1 }); err != nil {
				return nil, err
			}
			n++
		}
	}
	w.refresh(ctx, "/$stat/solo")
	return map[string]any{"cleared": n}, nil
}
