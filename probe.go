package main

// probe reads (never writes) what the add-on depends on and reports it,
// so the details the protocol document leaves open can be confirmed on a
// real console (README "Probe"). Meters are asked for briefly; that's a
// request for data, not a console setting.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (w *Wing) probe(ctx context.Context) string {
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	raw := func(path string, args ...any) string {
		r, err := w.osc.raw(ctx, path, args...)
		if err != nil {
			return "ERROR " + err.Error()
		}
		return fmt.Sprintf("%s %s", r.addr, describe(r.vals))
	}
	line("WING probe %s (add-on %s), read only", time.Now().Format("2006-01-02 15:04"), Version)
	line("")
	if _, err := w.hello(ctx); err != nil {
		line("The console didn't answer /? on OSC port %d: %v", w.cfg.Port, err)
		line("Check the address, that this computer is on the console's network, and that UDP %d isn't blocked.", w.cfg.Port)
		return b.String()
	}
	line("[1] identity  /?  -> %s", raw("/?"))
	for _, p := range []string{"/$syscfg/$firmware", "/$syscfg/$cnsmdl", "/$syscfg/consolename", "/cards/$type"} {
		line("    %s -> %s", p, raw(p))
	}
	line("")
	line("[2] reply shapes (expect ,sfi for ints, ,sff for floats)")
	for _, p := range []string{"/ch/1/mute", "/ch/1/$mute", "/ch/1/fdr", "/ch/1/$fdr", "/ch/1/$name", "/dca/1/fdr", "/mgrp/1/mute", "/aux/1/mute"} {
		line("    %s -> %s", p, raw(p))
	}
	line("    /ch/41/mute (must fail: 40 channels) -> %s", raw("/ch/41/mute"))
	line("")
	line("[2b] stereo (in/set/$mode: M, ST, M/S; busmono 0 = stereo) and preamp gain")
	for _, p := range []string{"/ch/1/in/set/$mode", "/aux/1/in/set/$mode", "/bus/1/busmono", "/main/1/busmono", "/mtx/1/busmono", "/ch/1/in/set/$g", "/ch/1/in/set/trim"} {
		line("    %s -> %s", p, raw(p))
	}
	if ds, err := w.osc.dd(ctx, "/ch/1/in/set"); err == nil {
		for _, d := range ds {
			if d.Name == "$g" || d.Name == "$vph" || d.Name == "$mode" {
				line("    def %s: type %d, %g..%g, read only %v", d.Name, d.Type(), d.Min, d.Max, d.ReadOnly())
			}
		}
	}
	for _, n := range []string{"/ch/1/eq", "/ch/1/dyn", "/ch/1/gate", "/fx/1"} {
		if ds, err := w.osc.dd(ctx, n); err == nil {
			line("    %s children: %s", n, defNames(ds))
		} else {
			line("    %s dd: ERROR %v", n, err)
		}
	}
	line("")
	line("[3] locks")
	for _, p := range []string{"/$ctl/osc/ronly", "/$ctl/OSC/ronly", "/$syscfg/tcplock", "/$ctl/$stat/cnslock"} {
		line("    %s -> %s", p, raw(p))
	}
	line("")
	line("[4] effective mute values ($mute 0/1/2): strips not 0")
	var odd []string
	for _, s := range faderStrips() {
		if s.Kind == "dca" {
			continue
		}
		v, err := w.osc.get(ctx, s.Path()+"/$mute")
		if err == nil && v.f != 0 {
			own, _ := w.osc.get(ctx, s.Path()+"/mute")
			odd = append(odd, fmt.Sprintf("%s $mute=%g mute=%g tags=%q", s.ID(), v.f, own.f, w.tagsOf(ctx, s)))
		}
	}
	if len(odd) == 0 {
		line("    (none: every strip is live; mute one by a DCA or mute group and probe again)")
	}
	for _, o := range odd {
		line("    %s", o)
	}
	line("")
	line("[5] mute-group / DCA tags (#Mn / #Dn) on ch 1-8")
	for n := 1; n <= 8; n++ {
		line("    ch:%d tags -> %s", n, raw(fmt.Sprintf("/ch/%d/tags", n)))
	}
	line("")
	line("[6] node definitions (hashes) over OSC: /ch/1 ,b dd")
	d1, err := w.osc.dd(ctx, "/ch/1")
	if err != nil {
		line("    ERROR %v (live updates will fall back to polling)", err)
	} else {
		line("    %d children; first: %s", len(d1), defSummary(d1, 6))
		d2, err := w.osc.dd(ctx, "/ch/1")
		same := err == nil && len(d1) == len(d2)
		for i := 0; same && i < len(d1); i++ {
			same = d1[i].Hash == d2[i].Hash
		}
		line("    asked twice, same hashes: %v (compare with a probe after a reboot)", same)
		for _, d := range d1 {
			if d.Name == "mute" {
				line("    /ch/1/mute hash = %08x (doc example: f50f69f8)", d.Hash)
			}
		}
	}
	if ds, err := w.osc.dd(ctx, "/$ctl/lib"); err == nil {
		for _, d := range ds {
			if d.Name == "$scenes" {
				line("    /$ctl/lib/$scenes: type %d, %d items: %s", d.Type(), len(d.Items), strings.Join(first(d.Items, 5), " | "))
			}
		}
	} else {
		line("    /$ctl/lib dd: ERROR %v", err)
	}
	line("    scenes GET -> %s ; show -> %s ; current -> %s", raw("/$ctl/lib/$scenes"), raw("/$ctl/lib/$actshow"), raw("/$ctl/lib/$actidx"))
	line("")
	line("[7] recorder / player / talkback")
	for _, p := range []string{"/$stat/usbstate", "/rec/$actstate", "/rec/$time", "/play/$actstate", "/play/$pos", "/play/$total", "/cfg/talk/A/$on", "/cfg/talk/A/mode", "/cfg/talk/B/$on", "/cfg/talk/B/mode", "/$stat/solo"} {
		line("    %s -> %s", p, raw(p))
	}
	line("")
	line("[8] native link (TCP %d)", w.cfg.NativePort)
	w.mu.Lock()
	n := &w.native
	connected, since, events, unknown, lastErr := n.connected, n.since, n.events, n.unknown, n.lastErr
	lastPaths := strings.Join(n.lastPaths, ", ")
	hashes := len(w.hashes)
	w.mu.Unlock()
	line("    connected=%v since=%s events=%d unmapped=%d learned hashes=%d last error=%q",
		connected, ago(since), events, unknown, hashes, lastErr)
	line("    last event paths: %s", lastPaths)
	line("    (move a fader at the desk, then probe again: events should grow and name it)")
	line("")
	line("[9] meters: main:1 and dca:1-16 for 2 s")
	if w.cfg.Meters == "off" {
		line("    skipped: meters are off in the connection's settings")
	} else {
		list := []stripRef{{"main", 1}}
		for i := 1; i <= 16; i++ {
			list = append(list, stripRef{"dca", i})
		}
		end := time.Now().Add(2500 * time.Millisecond)
		for time.Now().Before(end) && ctx.Err() == nil {
			w.meters.want(list)
			sleepCtx(ctx, 250*time.Millisecond)
		}
		w.meters.mu.Lock()
		pk, note := w.meters.packets, w.meters.note
		w.meters.mu.Unlock()
		line("    datagrams=%d rate=%.1f Hz note=%q", pk, w.meters.rateHz(), note)
		var got []string
		for _, s := range list {
			if m, ok := w.meters.get(s); ok {
				got = append(got, fmt.Sprintf("%s %.1f/%.1f", s.ID(), m.outL, m.outR))
			}
		}
		line("    levels: %s", strings.Join(got, ", "))
		line("    (dca 9-16 present = the console meters 16 DCAs)")
	}
	return b.String()
}

func (w *Wing) tagsOf(ctx context.Context, s stripRef) string {
	v, err := w.osc.get(ctx, s.Path()+"/tags")
	if err != nil {
		return ""
	}
	return v.s
}

func describe(vals []any) string {
	tags := ","
	var parts []string
	for _, v := range vals {
		switch x := v.(type) {
		case int32:
			tags += "i"
			parts = append(parts, fmt.Sprint(x))
		case float32:
			tags += "f"
			parts = append(parts, fmt.Sprint(x))
		case string:
			tags += "s"
			parts = append(parts, fmt.Sprintf("%q", x))
		case []byte:
			tags += "b"
			parts = append(parts, fmt.Sprintf("<%d bytes>", len(x)))
		}
	}
	return tags + " " + strings.Join(parts, " ")
}

func defNames(ds []nodeDef) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return strings.Join(out, " ")
}

func defSummary(ds []nodeDef, n int) string {
	var out []string
	for i, d := range ds {
		if i >= n {
			break
		}
		out = append(out, fmt.Sprintf("%s=%08x(t%d)", d.Name, d.Hash, d.Type()))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return time.Since(t).Round(time.Second).String() + " ago"
}
