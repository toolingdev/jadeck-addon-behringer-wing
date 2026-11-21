package main

// Channel detail and FX: the deeper controls (input trim/gain, polarity,
// filters, gate, EQ, compressor, sends, pan, inserts, FX parameters).
//
// The controls are built from the console's own node definitions (,b dd):
// names, long names, types, ranges and enum lists come from the console,
// so model-specific parameters (a different gate or EQ model, any FX
// model) show up as the console describes them (WING Remote Protocols
// 3.1 p.47-55, p.67, FX appendix p.150-180).
//
// Safety: only an allowlist can be written (writable reports it), never
// phantom power, source patching, insert slot assignment, model changes
// or anything outside a strip / FX slot. Every write is read back and the
// console's value is what the widget shows.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// detailSection is one tab of the channel widget: nodes under the strip.
type detailSection struct {
	id, title string
	nodes     []string // relative to the strip
}

func detailSections(s stripRef) []detailSection {
	var sends []string
	switch s.Kind {
	case "ch", "aux", "bus":
		for n := 1; n <= 16; n++ {
			if s.Kind == "bus" && n == s.N {
				continue
			}
			sends = append(sends, "send/"+strconv.Itoa(n))
		}
	}
	if s.Kind != "mtx" {
		for n := 1; n <= 8; n++ {
			sends = append(sends, "send/MX"+strconv.Itoa(n))
		}
	}
	var mains []string
	if s.Kind == "ch" || s.Kind == "aux" || s.Kind == "bus" {
		for n := 1; n <= 4; n++ {
			mains = append(mains, "main/"+strconv.Itoa(n))
		}
	}
	out := []detailSection{}
	if s.Kind == "ch" || s.Kind == "aux" {
		out = append(out, detailSection{"input", "Input", []string{"in/set", "flt"}})
	}
	if s.Kind == "ch" {
		out = append(out, detailSection{"gate", "Gate", []string{"gate"}})
	}
	out = append(out,
		detailSection{"eq", "EQ", []string{"eq"}},
		detailSection{"dyn", "Comp", []string{"dyn"}},
	)
	if len(sends)+len(mains) > 0 {
		out = append(out, detailSection{"sends", "Sends", append(mains, sends...)})
	}
	out = append(out, detailSection{"pan", "Pan · inserts", []string{"", "preins", "postins"}})
	return out
}

// Writable parameters, relative to a strip (ch aux bus main mtx).
var stripWritable = regexp.MustCompile(`^(in/set/(trim|bal|inv|\$g)|flt/(lc|lcf|lcs|hc|hcf|hcs)|(gate|eq|dyn)/[a-z0-9]+|main/[1-4]/(on|lvl|pre)|send/([0-9]+|MX[1-8])/(on|lvl|mode)|pan|wid|(preins|postins)/on|mute|fdr)$`)

// Shown read-only (context next to the controls).
var stripShown = regexp.MustCompile(`^((gate|eq|dyn)/mdl|(preins|postins)/ins)$`)

// Never shown nor written, even if a pattern above would match.
var neverTouch = regexp.MustCompile(`vph|/conn|altsrc|srcauto|/mdl$|/ins$|/io/|egrp|ein$|emode`)

// fxWritable: FX mix and the model's own parameters (not the model, not
// the read-only $ values, not an EXT model's routing).
func fxWritable(rel string) bool {
	return rel != "mdl" && !strings.HasPrefix(rel, "$") && !strings.Contains(rel, "/") && rel != "egrp" && rel != "ein" && rel != "emode"
}

var stripPathRe = regexp.MustCompile(`^/(ch|aux|bus|main|mtx)/([0-9]+)/(.+)$`)
var fxPathRe = regexp.MustCompile(`^/fx/([0-9]+)/([^/]+)$`)

// writablePath reports whether set_param may write path.
func writablePath(path string) bool {
	if neverTouch.MatchString(path) {
		return false
	}
	if m := stripPathRe.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[2])
		if n < 1 || n > kinds[m[1]].count {
			return false
		}
		return stripWritable.MatchString(m[3])
	}
	if m := fxPathRe.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n >= 1 && n <= 16 && fxWritable(m[2])
	}
	return false
}

// paramView is one control.
type paramView struct {
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Group    string   `json:"group,omitempty"` // e.g. "Bus 3 · Monitors" for a send's three params
	Kind     string   `json:"kind"`            // bool, int, float, fader, enum, text
	Min      float64  `json:"min"`
	Max      float64  `json:"max"`
	Steps    int      `json:"steps,omitempty"`
	Log      bool     `json:"log,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Items    []string `json:"items,omitempty"`
	Value    any      `json:"value"`
	Text     string   `json:"text"`
	Writable bool     `json:"writable"`
}

type sectionView struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Params []paramView `json:"params"`
}

var units = []string{"", "dB", "%", "ms", "Hz", "m", "s", "oct"}

// learnNode asks for a node's definitions once (and again after a model
// change), adding their hashes so native events keep the values live.
func (w *Wing) learnNode(ctx context.Context, node string) ([]string, error) {
	w.mu.Lock()
	kids, ok := w.learned[node]
	w.mu.Unlock()
	if ok {
		return kids, nil
	}
	ds, err := w.osc.dd(ctx, node)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	kids = kids[:0]
	for _, d := range ds {
		p := node + "/" + d.Name
		w.hashes[d.Hash] = p
		w.defs[p] = d
		kids = append(kids, d.Name)
	}
	w.learned[node] = kids
	return kids, nil
}

// forget drops what was learned under node (a model changed).
func (w *Wing) forget(node string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for n := range w.learned {
		if n == node || strings.HasPrefix(n, node+"/") {
			delete(w.learned, n)
		}
	}
}

func (w *Wing) paramView(path, name string, writable bool) (paramView, bool) {
	w.mu.Lock()
	d, ok := w.defs[path]
	v, has := w.vals[path]
	w.mu.Unlock()
	if !ok || d.Type() == typeNode {
		return paramView{}, false
	}
	p := paramView{Path: path, Name: name, Label: nz(d.Long, name), Min: d.Min, Max: d.Max, Steps: d.Steps,
		Writable: writable && !d.ReadOnly() && !w.cfg.ReadOnly}
	if u := d.Unit(); u < len(units) {
		p.Unit = units[u]
	}
	switch d.Type() {
	case typeInt:
		p.Kind = "int"
		if d.Min == 0 && d.Max == 1 {
			p.Kind = "bool"
		}
	case typeLinF:
		p.Kind = "float"
	case typeLogF:
		p.Kind, p.Log = "float", true
	case typeFader:
		p.Kind, p.Min, p.Max, p.Unit = "fader", -144, 10, "dB"
	case typeEnum, typeFEnum:
		p.Kind, p.Items = "enum", d.Items
	default:
		p.Kind = "text"
	}
	if has {
		p.Text = v.s
		switch p.Kind {
		case "enum", "text":
			p.Value = v.s
		default:
			p.Value = v.f
		}
	}
	return p, true
}

// detail is the "detail?strip=ch:3" resource: the strip, and every
// section's controls with the console's current values.
func (w *Wing) detail(ctx context.Context, s stripRef) (map[string]any, error) {
	if s.Kind == "dca" || s.Kind == "mgrp" {
		return nil, fmt.Errorf("%s has no channel processing", s.Label())
	}
	w.markDetail(s)
	base := s.Path()
	var sections []sectionView
	var toRead []string
	type pending struct {
		sec   int
		path  string
		name  string
		group string
		write bool
	}
	var rows []pending
	for si, sec := range detailSections(s) {
		sections = append(sections, sectionView{ID: sec.id, Title: sec.title, Params: []paramView{}})
		for _, rel := range sec.nodes {
			node := base
			if rel != "" {
				node = base + "/" + rel
			}
			kids, err := w.learnNode(ctx, node)
			if err != nil {
				continue // the console has no such node for this strip
			}
			group := w.groupLabel(rel)
			for _, k := range kids {
				r := strings.TrimPrefix(strings.TrimPrefix(node+"/"+k, base), "/")
				full := base + "/" + r
				if neverTouch.MatchString(full) && !stripShown.MatchString(r) {
					continue
				}
				if rel == "" && r != "pan" && r != "wid" {
					continue // the strip itself: only pan and width here
				}
				writable := writablePath(full)
				if !writable && !stripShown.MatchString(r) {
					continue
				}
				rows = append(rows, pending{si, full, k, group, writable})
				toRead = append(toRead, full)
			}
		}
	}
	w.mu.Lock()
	age := time.Since(w.detailRead[s])
	stale := age > 30*time.Second || (!w.nativeLiveLocked() && age > 2*time.Second) || w.detailRead[s].IsZero()
	if stale {
		w.detailRead[s] = time.Now()
	}
	w.mu.Unlock()
	if stale {
		w.readAll(ctx, toRead)
	}
	for _, r := range rows {
		if p, ok := w.paramView(r.path, r.name, r.write); ok {
			p.Group = r.group
			sections[r.sec].Params = append(sections[r.sec].Params, p)
		}
	}
	return map[string]any{"ok": true, "connected": w.isOnline(), "readOnly": w.cfg.ReadOnly, "locked": w.lockText(),
		"strip": w.view(s), "sections": sections}, nil
}

// groupLabel names a send node: "Bus 3 · Monitors", "Matrix 2", "Main 1".
func (w *Wing) groupLabel(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) != 2 {
		return ""
	}
	var t stripRef
	switch {
	case parts[0] == "send" && strings.HasPrefix(parts[1], "MX"):
		n, _ := strconv.Atoi(parts[1][2:])
		t = stripRef{"mtx", n}
	case parts[0] == "send":
		n, _ := strconv.Atoi(parts[1])
		t = stripRef{"bus", n}
	case parts[0] == "main":
		n, _ := strconv.Atoi(parts[1])
		t = stripRef{"main", n}
	default:
		return ""
	}
	v := w.view(t)
	if v.Name != "" {
		return v.Tag + " · " + v.Name
	}
	return v.Tag
}

func (w *Wing) markDetail(s stripRef) {
	w.mu.Lock()
	w.detailHot[s] = time.Now()
	w.mu.Unlock()
}

func (w *Wing) nativeLiveLocked() bool {
	return w.native.connected && len(w.hashes) > 0 && time.Since(w.native.lastRx) < 12*time.Second
}

// fxSlots is the "fx?slots=1,2" resource.
func (w *Wing) fxSlots(ctx context.Context, slots []int) map[string]any {
	out := []map[string]any{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	res := make([]map[string]any, len(slots))
	for i, n := range slots {
		wg.Add(1)
		go func(i, n int) {
			defer wg.Done()
			node := "/fx/" + strconv.Itoa(n)
			mdl, err := w.osc.get(ctx, node+"/mdl")
			if err != nil {
				mu.Lock()
				res[i] = map[string]any{"slot": n, "model": "", "error": err.Error(), "params": []paramView{}}
				mu.Unlock()
				return
			}
			w.put(node+"/mdl", mdl)
			w.mu.Lock()
			prev := w.fxModel[n]
			w.fxModel[n] = mdl.s
			w.mu.Unlock()
			if prev != mdl.s {
				w.forget(node)
			}
			kids, err := w.learnNode(ctx, node)
			params := []paramView{}
			if err == nil {
				var paths []string
				for _, k := range kids {
					if fxWritable(k) {
						paths = append(paths, node+"/"+k)
					}
				}
				if prev != mdl.s || !w.nativeLive() {
					w.readAll(ctx, paths)
				}
				for _, p := range paths {
					if v, ok := w.paramView(p, p[len(node)+1:], true); ok {
						params = append(params, v)
					}
				}
				sort.SliceStable(params, func(a, b int) bool { return fxRank(params[a].Name) < fxRank(params[b].Name) })
			}
			mu.Lock()
			res[i] = map[string]any{"slot": n, "model": mdl.s, "params": params}
			mu.Unlock()
		}(i, n)
	}
	wg.Wait()
	out = append(out, res...)
	return map[string]any{"ok": true, "connected": w.isOnline(), "readOnly": w.cfg.ReadOnly, "locked": w.lockText(), "slots": out}
}

// fxRank puts the parameters people reach for first: mix, decay,
// pre-delay, size, delay time, feedback.
func fxRank(name string) int {
	for i, k := range []string{"fxmix", "dcy", "pdel", "size", "time", "feed", "sust", "fb", "rate", "depth"} {
		if name == k {
			return i
		}
	}
	return 100
}

// setParam writes one allowlisted parameter and returns the console's
// value (read back).
func (w *Wing) setParam(ctx context.Context, path string, raw any) (paramView, error) {
	if !writablePath(path) {
		return paramView{}, fmt.Errorf("%s can't be changed from jadeck (only processing, sends and levels; never phantom power or patching)", path)
	}
	if w.cfg.ReadOnly {
		return paramView{}, errReadOnly
	}
	w.mu.Lock()
	d, ok := w.defs[path]
	w.mu.Unlock()
	if !ok {
		// learn the parent node first (a widget may write before reading)
		i := strings.LastIndex(path, "/")
		if _, err := w.learnNode(ctx, path[:i]); err != nil {
			return paramView{}, err
		}
		w.mu.Lock()
		d, ok = w.defs[path]
		w.mu.Unlock()
		if !ok {
			return paramView{}, fmt.Errorf("the console has no %s", path)
		}
	}
	if d.ReadOnly() {
		return paramView{}, fmt.Errorf("%s is read only on the console", path)
	}
	var arg any
	var check func(value) bool
	num := func() (float64, error) {
		switch x := raw.(type) {
		case float64:
			return x, nil
		case bool:
			if x {
				return 1, nil
			}
			return 0, nil
		case string:
			return strconv.ParseFloat(strings.TrimSpace(x), 64)
		}
		return 0, errors.New("value must be a number")
	}
	switch d.Type() {
	case typeInt:
		f, err := num()
		if err != nil {
			return paramView{}, err
		}
		if f != math.Trunc(f) || f < d.Min || f > d.Max {
			return paramView{}, fmt.Errorf("%s takes %g..%g", path, d.Min, d.Max)
		}
		arg = int32(f)
		check = func(v value) bool { return v.f == f }
	case typeLinF, typeLogF, typeFader:
		f, err := num()
		if err != nil {
			return paramView{}, err
		}
		lo, hi := d.Min, d.Max
		if d.Type() == typeFader {
			lo, hi = -144, 10
		}
		if f < lo || f > hi || math.IsNaN(f) {
			return paramView{}, fmt.Errorf("%s takes %g..%g", path, lo, hi)
		}
		arg = float32(f)
		tol := 0.6
		if d.Steps > 1 && d.Type() == typeLinF {
			tol = (hi-lo)/float64(d.Steps-1)*0.6 + 1e-6
		}
		check = func(v value) bool {
			if d.Type() == typeLogF {
				return math.Abs(v.f-f) <= math.Max(0.06*math.Abs(f), 0.01)
			}
			if d.Type() == typeFader && f <= -143.5 {
				return v.f <= -143.5
			}
			return math.Abs(v.f-f) <= tol
		}
	case typeEnum, typeFEnum:
		s := fmt.Sprint(raw)
		if f, ok := raw.(float64); ok {
			s = trimFloat(f)
		}
		found := false
		for _, it := range d.Items {
			if strings.EqualFold(it, s) {
				s, found = it, true
			}
		}
		if !found {
			return paramView{}, fmt.Errorf("%s takes one of %s", path, strings.Join(d.Items, ", "))
		}
		arg = s
		check = func(v value) bool { return strings.EqualFold(v.s, s) }
	default:
		return paramView{}, fmt.Errorf("%s isn't a level, switch or choice", path)
	}
	if _, err := w.write(ctx, path, arg, "", 0, check); err != nil {
		return paramView{}, err
	}
	i := strings.LastIndex(path, "/")
	p, _ := w.paramView(path, path[i+1:], true)
	return p, nil
}
