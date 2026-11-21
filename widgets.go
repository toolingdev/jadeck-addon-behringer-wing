package main

// The add-on's widgets (design/WIDGETS-V2.md). Shapes are drawn by jadeck
// from AddonState; the faders, DCA faders and meter bridge are frames
// (ui/) that poll the "live" resource for levels and meters.

import (
	"strconv"

	"github.com/toolingdev/jadeck/sdk/addon"
)

var legacySample = map[string]any{"/api/mixer/status": map[string]any{
	"label": "Behringer WING", "configured": true, "connected": true, "console": "WING",
	"channels": []map[string]any{
		{"slot": 1, "label": "Main", "channel": 1, "muted": false},
		{"slot": 2, "label": "Lectern", "channel": 2, "muted": false},
		{"slot": 3, "label": "Wireless 1", "channel": 3, "muted": true},
		{"slot": 4, "label": "Wireless 2", "channel": 4, "muted": false},
	},
}}

func muteBtn(id, label, color string, muted, emuted bool) map[string]any {
	tone := ""
	if emuted {
		tone = "pgm"
	}
	if emuted && !muted {
		label += " (group)"
	}
	return map[string]any{"id": id, "label": label, "tone": tone, "active": muted, "color": color,
		"params": map[string]any{"strip": id, "mode": "toggle"}}
}

// sampleLive is what the frames draw in the Settings preview (no
// connection there, so no resource either).
func sampleLive() map[string]any {
	s := func(id, kind string, n int, tag, name, color string, db, pos, l float64, muted, stereo bool, gain float64) map[string]any {
		m := map[string]any{"id": id, "kind": kind, "n": n, "tag": tag, "name": name, "label": name, "color": color,
			"muted": muted, "emuted": muted, "db": db, "edb": db, "pos": pos, "l": l, "r": l - 1.5, "stereo": stereo}
		if gain > -100 {
			m["gain"] = gain
		}
		return m
	}
	return map[string]any{"ok": true, "connected": true, "transport": "native", "meters": map[string]any{"running": true, "hz": 20},
		"strips": []map[string]any{
			s("ch:5", "ch", 5, "CH 5", "Lead vox", "#e02040", 1.5, 0.79, -8, false, false, 32),
			s("ch:9", "ch", 9, "CH 9", "Keys", "#01b23e", -3, 0.69, -14, false, true, 18),
			s("ch:7", "ch", 7, "CH 7", "BGV 2", "#e02040", -5, 0.66, -60, true, false, 30),
			s("bus:5", "bus", 5, "BUS 5", "IEM Band", "#a533ff", -2, 0.73, -20, false, true, -999),
			s("main:1", "main", 1, "MAIN 1", "Main", "#e0e0e0", 0, 0.75, -4, false, true, -999),
		}}
}

// sampleDetail / sampleFx are what the Channel strip and FX frames draw in
// the Settings preview.
func sampleDetail() map[string]any {
	f := func(path, name string, min, max, v float64, unit string, log bool) paramView {
		return paramView{Path: path, Name: name, Label: name, Kind: "float", Min: min, Max: max, Value: v, Unit: unit, Log: log, Writable: true}
	}
	b := func(path, name string, on bool) paramView {
		v := 0.0
		if on {
			v = 1
		}
		return paramView{Path: path, Name: name, Label: name, Kind: "bool", Min: 0, Max: 1, Value: v, Writable: true}
	}
	return map[string]any{"ok": true, "connected": true,
		"strip": map[string]any{"id": "ch:5", "tag": "CH 5", "name": "Lead vox", "color": "#e02040", "db": 1.5, "gain": 32.5, "l": -12.0},
		"sections": []sectionView{
			{ID: "input", Title: "Input", Params: []paramView{
				f("/ch/5/in/set/$g", "$g", -2.5, 45, 32.5, "dB", false), f("/ch/5/flt/lcf", "lcf", 20, 2000, 80, "Hz", true),
				b("/ch/5/flt/lc", "lc", true), f("/ch/5/in/set/trim", "trim", -18, 18, 0, "dB", false), b("/ch/5/in/set/inv", "inv", false)}},
			{ID: "eq", Title: "EQ", Params: []paramView{
				b("/ch/5/eq/on", "on", true),
				f("/ch/5/eq/lg", "lg", -15, 15, 2, "dB", false), f("/ch/5/eq/1g", "1g", -15, 15, -3.5, "dB", false),
				f("/ch/5/eq/2g", "2g", -15, 15, 1.5, "dB", false), f("/ch/5/eq/hg", "hg", -15, 15, 2.5, "dB", false),
				f("/ch/5/eq/lf", "lf", 20, 2000, 120, "Hz", true), f("/ch/5/eq/1f", "1f", 20, 20000, 350, "Hz", true),
				f("/ch/5/eq/2f", "2f", 20, 20000, 2800, "Hz", true), f("/ch/5/eq/hf", "hf", 50, 20000, 10000, "Hz", true)}},
			{ID: "dyn", Title: "Comp", Params: []paramView{b("/ch/5/dyn/on", "on", true), f("/ch/5/dyn/thr", "thr", -60, 0, -18, "dB", false)}},
		}}
}

func sampleFx() map[string]any {
	return map[string]any{"ok": true, "connected": true, "slots": []map[string]any{
		{"slot": 1, "model": "HALL", "params": []paramView{
			{Path: "/fx/1/fxmix", Name: "fxmix", Label: "MIX", Kind: "float", Min: 0, Max: 100, Value: 100.0, Unit: "%", Writable: true},
			{Path: "/fx/1/dcy", Name: "dcy", Label: "DECAY", Kind: "float", Min: 0.2, Max: 5, Log: true, Value: 1.8, Unit: "s", Writable: true},
			{Path: "/fx/1/pdel", Name: "pdel", Label: "PRE-DELAY", Kind: "int", Min: 0, Max: 200, Value: 20.0, Unit: "ms", Writable: true}}},
		{"slot": 2, "model": "ST-DL", "params": []paramView{
			{Path: "/fx/2/time", Name: "time", Label: "TIME", Kind: "float", Min: 1, Max: 3000, Value: 400.0, Unit: "ms", Writable: true}}},
	}}
}

var stateSample = map[string]any{
	"previewDetail": sampleDetail(), "previewFx": sampleFx(),
	"stripOptions": []map[string]any{{"value": "ch:5", "label": "CH 5 · Lead vox"}, {"value": "ch:9", "label": "CH 9 · Keys"}},
	"connected":    true, "transport": "native", "consoleName": "Main WING", "model": "WING (full size)", "firmware": "3.1.2",
	"defaultFaders": []string{"ch:5", "ch:9", "ch:7", "bus:5", "main:1"},
	"defaultMeters": []string{"ch:5", "ch:9", "ch:7", "bus:5", "main:1"},
	"defaultDcas":   []string{"dca:1", "dca:2", "dca:3", "dca:4"},
	"previewLive":   sampleLive(),
	"mutes": []map[string]any{
		muteBtn("ch:1", "Pulpit", "#0180ff", false, false), muteBtn("ch:2", "Lectern", "#25c3ff", false, false),
		muteBtn("ch:3", "Wireless 1", "#01b23e", true, true), muteBtn("ch:4", "Wireless 2", "#01b23e", false, false),
		muteBtn("ch:5", "Choir L", "#a533ff", false, true), muteBtn("ch:6", "Choir R", "#a533ff", false, true),
		muteBtn("ch:7", "Keys", "#f2dd00", false, false), muteBtn("ch:8", "CH 8", "#707070", false, false),
	},
	"dcaMutes": []map[string]any{
		muteBtn("dca:1", "Band", "#e02040", false, false), muteBtn("dca:2", "Vocals", "#ffb81a", false, false),
		muteBtn("dca:3", "Choir", "#a533ff", true, true), muteBtn("dca:4", "Speech", "#0180ff", false, false),
	},
	"muteGroups": []map[string]any{
		muteBtn("mgrp:1", "Band", "#707070", false, false), muteBtn("mgrp:2", "Choir", "#707070", true, true),
		muteBtn("mgrp:3", "Pulpit", "#707070", false, false), muteBtn("mgrp:4", "FX", "#707070", false, false),
	},
	"mainPos": 0.75, "mainLabel": "Main LR · 0.0 dB",
	"scenes": []map[string]any{{"id": "show", "title": "SUNDAY", "meta": "5 scenes", "items": []map[string]any{
		{"id": "1", "title": "1 · Walk-in"}, {"id": "2", "title": "2 · Worship", "sub": "Band + choir", "active": true},
		{"id": "3", "title": "3 · Sermon"}, {"id": "4", "title": "4 · Special music"}, {"id": "5", "title": "5 · Walk-out"},
	}}},
	"sceneCurrent": "2", "sceneText": "Scene 2: Worship",
	"sceneButtons": []map[string]any{{"id": "prev", "label": "◀ Previous", "action": "scene_go_prev"}, {"id": "next", "label": "Next ▶", "action": "scene_go_next"}},
	"recButtons": []map[string]any{
		{"id": "rec", "label": "● 12:34", "action": "rec_start", "tone": "pgm", "active": true, "hold": 5, "params": map[string]any{"confirmed": true}},
		{"id": "pause", "label": "Pause", "action": "rec_pause"},
		{"id": "stop", "label": "Stop", "action": "rec_stop", "hold": 5, "holdLabel": "Keep holding to stop…", "params": map[string]any{"confirmed": true}},
		{"id": "newfile", "label": "New file", "action": "rec_new_file", "hold": 5, "params": map[string]any{"confirmed": true}},
	},
	"recText": "<b>Recording</b> · 12:34<br>R_20261121-100212.wav<br>USB: BUSY (SERMONS)",
	"playButtons": []map[string]any{
		{"id": "prev", "label": "⏮", "action": "play_prev"}, {"id": "play", "label": "▶ Play", "action": "play_play", "tone": "pvw", "active": true},
		{"id": "pause", "label": "Pause", "action": "play_pause"}, {"id": "stop", "label": "Stop", "action": "play_stop"},
		{"id": "next", "label": "⏭", "action": "play_next"},
	},
	"playText": "<b>Playing</b> · 1:12 / 3:40<br>Walk-in music · Instrumental",
	"talkButtons": []map[string]any{
		{"id": "A", "label": "Talkback A", "action": "talkback", "active": true, "params": map[string]any{"bus": "A", "mode": "toggle"}},
		{"id": "B", "label": "Talkback B", "action": "talkback", "params": map[string]any{"bus": "B", "mode": "toggle"}},
	},
	"soloButtons": []map[string]any{{"id": "clear", "label": "Clear solo", "action": "solo_clear", "tone": "pgm"}},
	"statusBody":  "<b>Main WING</b> · WING (full size) · firmware 3.1.2<br>Live updates from the console<br>No locks<br>USB: IDLE<br>Meters: on (20 Hz)",
	"probeReport": "Not run yet. Press “Run probe” (it only reads).",
}

func stripsField(label, from, help string) addon.Field {
	return addon.Field{Key: "strips", Label: label, Type: addon.FieldOrder, OptionsFrom: from, Help: help}
}

var tapField = addon.Field{Key: "tap", Label: "Meter point", Type: addon.FieldSelect, Default: "post", Options: []addon.Option{
	{Value: "post", Label: "Post fader"}, {Value: "pre", Label: "Pre fader (input)"}},
	Help: "Which level the meters show."}

func faderFields(what, help string) []addon.Field {
	return []addon.Field{
		stripsField(what, "wing.faders", "Pick and order. "+help),
		{Key: "meters", Label: "Meters", Type: addon.FieldBool, Default: true},
		tapField,
		{Key: "mute", Label: "Mute and solo buttons", Type: addon.FieldBool, Default: true},
		{Key: "gain", Label: "Gain knob", Type: addon.FieldBool, Default: true, Help: "Shows the preamp gain of channels that have one."},
		{Key: "gainEdit", Label: "Edit gain here", Type: addon.FieldBool, Default: false, Help: "Off: the gain knob only shows the value."},
		{Key: "readout", Label: "Level in dB", Type: addon.FieldBool, Default: true},
		{Key: "scale", Label: "Fader scale", Type: addon.FieldSelect, Default: "marks", Options: []addon.Option{
			{Value: "marks", Label: "dB marks"}, {Value: "none", Label: "None"}}},
	}
}

func columnsField(def int) addon.Field {
	return addon.Field{Key: "columns", Label: "Columns", Type: addon.FieldNumber, Default: def, Min: addon.Num(1), Max: addon.Num(8)}
}

func fxSlotOptions() []addon.Option {
	var out []addon.Option
	for n := 1; n <= 16; n++ {
		out = append(out, addon.Option{Value: strconv.Itoa(n), Label: "FX " + strconv.Itoa(n)})
	}
	return out
}

func widgetDefs() []addon.WidgetDef {
	return []addon.WidgetDef{
		{Kind: addon.KindCore, Type: "audio.mute", Label: "Mute button", Sample: legacySample, Settings: map[string]any{"slot": "2"}},
		{Kind: addon.KindCore, Type: "audio.favorites", Label: "Channel favorites", Sample: legacySample},

		{Kind: addon.KindButtons, Type: "mute_grid", Label: "Mutes", Description: "Mute buttons with the console's names; red = muted.",
			Bind: map[string]any{"items": "mutes", "action": "mute"}, Size: &addon.Size{W: 6, H: 4}, Sample: stateSample,
			Fields: []addon.Field{stripsField("Strips", "wing.strips", "Blank = the connection's Mute grid strips.")}},
		{Kind: addon.KindButtons, Type: "dca_mutes", Label: "DCA mutes", Description: "DCA mute buttons; red = muted.",
			Bind: map[string]any{"items": "dcaMutes", "action": "mute"}, Size: &addon.Size{W: 4, H: 3}, Sample: stateSample,
			Fields: []addon.Field{stripsField("DCAs", "wing.faders", "Blank = the connection's DCA strips. Only DCAs are shown.")}},
		{Kind: addon.KindButtons, Type: "mute_groups", Label: "Mute groups", Description: "The 8 mute groups; red = muted.",
			Bind: map[string]any{"items": "muteGroups", "action": "mute"}, Size: &addon.Size{W: 4, H: 3}, Sample: stateSample},

		{Kind: addon.KindFrame, Type: "faders", Label: "Faders", Entry: "ui/faders.html",
			Description: "Any number of faders, in your order, with LED meters, mutes and the console's names and colours.",
			Size:        &addon.Size{W: 8, H: 9}, MinPx: &addon.Size{W: 120, H: 220}, Sample: stateSample, Fields: faderFields("Strips", "Blank = the connection's Fader strips.")},
		{Kind: addon.KindFrame, Type: "dca_faders", Label: "DCA faders", Entry: "ui/dca.html",
			Description: "DCA faders with meters and mutes.",
			Size:        &addon.Size{W: 6, H: 9}, MinPx: &addon.Size{W: 120, H: 220}, Sample: stateSample, Fields: faderFields("DCAs", "Blank = the connection's DCA strips. Only DCAs are shown.")},
		{Kind: addon.KindFrame, Type: "channel", Label: "Channel strip", Entry: "ui/channel.html",
			Description: "One channel's input, filters, gate, EQ, compressor, sends, pan and inserts. Locked until you tap the lock.",
			Size:        &addon.Size{W: 6, H: 12}, MinPx: &addon.Size{W: 260, H: 320}, Sample: stateSample,
			Fields: []addon.Field{{Key: "strip", Label: "Channel", Type: addon.FieldSelect, OptionsFrom: "wing.faders", Default: "ch:1"}, tapField}},
		{Kind: addon.KindFrame, Type: "fx", Label: "FX", Entry: "ui/fx.html",
			Description: "FX slots: model, mix and the model's parameters (as the console lists them). Locked until you tap the lock.",
			Size:        &addon.Size{W: 6, H: 10}, MinPx: &addon.Size{W: 240, H: 240}, Sample: stateSample,
			Fields: []addon.Field{{Key: "slots", Label: "FX slots", Type: addon.FieldSelect, Multiple: true, Options: fxSlotOptions(), Default: []string{"1", "2"}}}},
		{Kind: addon.KindSlider, Type: "main_fader", Label: "Main fader", Description: "MAIN 1's fader.",
			Bind: map[string]any{"value": "mainPos", "action": "set_fader", "param": "main:1", "label": "mainLabel"}, Sample: stateSample},
		{Kind: addon.KindFrame, Type: "meter_bridge", Label: "Meter bridge", Entry: "ui/meters.html",
			Description: "LED meters for the strips you pick, with gate and compressor lights.",
			Size:        &addon.Size{W: 8, H: 6}, MinPx: &addon.Size{W: 160, H: 140}, Sample: stateSample,
			Fields: []addon.Field{stripsField("Strips", "wing.faders", "Blank = the connection's Meter strips."), tapField}},

		{Kind: addon.KindSections, Type: "scenes", Label: "Scenes", Description: "The open show's scenes; tap one to load it.",
			Bind: map[string]any{"sections": "scenes", "action": "scene_go", "param": "index", "current": "sceneCurrent"}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "scene_control", Label: "Scene control", Description: "Previous / next scene.",
			Bind: map[string]any{"items": "sceneButtons", "columns": 2}, Size: &addon.Size{W: 4, H: 2}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "recorder", Label: "USB recorder", Description: "Record, pause, stop, new file.",
			Bind: map[string]any{"items": "recButtons", "columns": "$settings.columns"}, Size: &addon.Size{W: 6, H: 2}, Sample: stateSample,
			Fields: []addon.Field{{Key: "buttons", Label: "Buttons", Type: addon.FieldSelect, Multiple: true, Default: []string{"rec", "pause", "stop", "newfile"},
				Options: []addon.Option{{Value: "rec", Label: "Record"}, {Value: "pause", Label: "Pause"}, {Value: "stop", Label: "Stop"}, {Value: "newfile", Label: "New file"}}},
				columnsField(4),
				{Key: "holdSeconds", Label: "Hold to record / stop (s)", Type: addon.FieldNumber, Default: 5, Min: addon.Num(3), Max: addon.Num(10),
					Help: "Record, Stop and New file run only after being held this long."}}},
		{Kind: addon.KindText, Type: "recorder_status", Label: "Recorder", Description: "Recorder state, time and file.",
			Bind: map[string]any{"body": "recText"}, Size: &addon.Size{W: 4, H: 3}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "player", Label: "USB player", Description: "Previous, play, pause, stop, next.",
			Bind: map[string]any{"items": "playButtons", "columns": "$settings.columns"}, Size: &addon.Size{W: 6, H: 2}, Sample: stateSample,
			Fields: []addon.Field{{Key: "buttons", Label: "Buttons", Type: addon.FieldSelect, Multiple: true, Default: []string{"prev", "play", "pause", "stop", "next"},
				Options: []addon.Option{{Value: "prev", Label: "Previous"}, {Value: "play", Label: "Play"}, {Value: "pause", Label: "Pause"}, {Value: "stop", Label: "Stop"}, {Value: "next", Label: "Next"}}},
				columnsField(5)}},
		{Kind: addon.KindText, Type: "player_status", Label: "Player", Description: "Song, position and length.",
			Bind: map[string]any{"body": "playText"}, Size: &addon.Size{W: 4, H: 3}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "talkback", Label: "Talkback", Description: "Talkback A / B on and off.",
			Bind: map[string]any{"items": "talkButtons", "columns": 2}, Size: &addon.Size{W: 4, H: 2}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "solo", Label: "Solo", Description: "Red when anything is soloed; tap to clear.",
			Bind: map[string]any{"items": "soloButtons", "columns": 1}, Size: &addon.Size{W: 3, H: 2}, Sample: stateSample},
		{Kind: addon.KindText, Type: "console_status", Label: "Console status", Description: "Name, model, firmware, link, locks, USB.",
			Bind: map[string]any{"body": "statusBody"}, Size: &addon.Size{W: 4, H: 4}, Sample: stateSample},
		{Kind: addon.KindButtons, Type: "console_tools", Label: "Console tools", Description: "Re-read the console, or run the read-only probe.",
			Bind: map[string]any{"buttons": []map[string]any{{"id": "refresh", "label": "Re-read console", "action": "refresh"}, {"id": "probe", "label": "Run probe", "action": "probe"}}, "columns": 2},
			Size: &addon.Size{W: 4, H: 2}, Sample: stateSample},
		{Kind: addon.KindText, Type: "probe_report", Label: "Probe report", Description: "What the last probe read (to confirm details on a real console).",
			Bind: map[string]any{"body": "probeReport"}, Size: &addon.Size{W: 8, H: 12}, Sample: stateSample},
	}
}
