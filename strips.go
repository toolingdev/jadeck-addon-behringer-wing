package main

// Strips: how the add-on names every fader on a WING, and where each one
// lives in the console's tree (WING Remote Protocols 3.1 p.47-66).

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// stripRef is one strip: Kind is ch, aux, bus, main, mtx, dca or mgrp;
// N is 1-based. Its id is "ch:3".
type stripRef struct {
	Kind string
	N    int
}

func (s stripRef) ID() string   { return s.Kind + ":" + strconv.Itoa(s.N) }
func (s stripRef) Path() string { return "/" + s.Kind + "/" + strconv.Itoa(s.N) }

// Label is how the console's own screens say it: "CH 3", "MAIN 1".
func (s stripRef) Label() string { return kinds[s.Kind].label + " " + strconv.Itoa(s.N) }

type kindInfo struct {
	label string
	count int
	// strip parameters read and followed: ch-like strips have the RO
	// "effective" twins ($name, $col, $mute, $fdr); DCAs and mute groups
	// don't.
	chLike bool
	order  int
}

var kinds = map[string]kindInfo{
	"ch":   {"CH", 40, true, 0},
	"aux":  {"AUX", 8, true, 1},
	"bus":  {"BUS", 16, true, 2},
	"main": {"MAIN", 4, true, 3},
	"mtx":  {"MTX", 8, true, 4},
	"dca":  {"DCA", 16, false, 5},
	"mgrp": {"MGRP", 8, false, 6},
}

// kindOrder lists the kinds in the console's order.
var kindOrder = []string{"ch", "aux", "bus", "main", "mtx", "dca", "mgrp"}

// allStrips is every strip on the console (mute groups included).
func allStrips() []stripRef {
	var out []stripRef
	for _, k := range kindOrder {
		for n := 1; n <= kinds[k].count; n++ {
			out = append(out, stripRef{k, n})
		}
	}
	return out
}

// faderStrips is every strip with a fader (no mute groups).
func faderStrips() []stripRef {
	var out []stripRef
	for _, s := range allStrips() {
		if s.Kind != "mgrp" {
			out = append(out, s)
		}
	}
	return out
}

var kindAliases = map[string]string{
	"ch": "ch", "chan": "ch", "channel": "ch", "in": "ch", "input": "ch",
	"aux": "aux", "bus": "bus", "main": "main", "mains": "main", "lr": "main",
	"mtx": "mtx", "matrix": "mtx", "dca": "dca", "mgrp": "mgrp", "mg": "mgrp", "mutegroup": "mgrp",
}

// parseStrip reads "ch:3", "ch3", "CH 3", "aux:2", or a bare "3" (a
// channel; 41-48 are aux 1-8, as the core mute widgets number them).
func parseStrip(s string) (stripRef, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.NewReplacer(" ", "", "_", "", ".", ":", "/", ":").Replace(t)
	i := strings.IndexFunc(t, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return stripRef{}, fmt.Errorf("%q is not a strip (try ch:3, aux:1, bus:2, main:1, mtx:1, dca:1)", s)
	}
	k := strings.TrimSuffix(t[:i], ":")
	n, err := strconv.Atoi(t[i:])
	if err != nil {
		return stripRef{}, fmt.Errorf("%q is not a strip", s)
	}
	if k == "" {
		if n >= 41 && n <= 48 {
			return stripRef{"aux", n - 40}, nil
		}
		k = "ch"
	}
	kind, ok := kindAliases[k]
	if !ok {
		return stripRef{}, fmt.Errorf("%q: unknown strip kind %q", s, k)
	}
	if n < 1 || n > kinds[kind].count {
		return stripRef{}, fmt.Errorf("%s has %d strips (1-%d); %q is outside", kinds[kind].label, kinds[kind].count, kinds[kind].count, s)
	}
	return stripRef{kind, n}, nil
}

// parseStripList reads "ch:1-8, aux:1, main:1" (ranges allowed: "ch:1-8"
// or "ch1-ch8"). Duplicates are dropped, order kept.
func parseStripList(s string) ([]stripRef, error) {
	var out []stripRef
	seen := map[stripRef]bool{}
	add := func(r stripRef) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, err := parseStrip(a)
			if err != nil {
				return nil, err
			}
			bt := strings.TrimSpace(b)
			if _, err := strconv.Atoi(bt); err == nil {
				bt = from.Kind + ":" + bt
			}
			to, err := parseStrip(bt)
			if err != nil {
				return nil, err
			}
			if to.Kind != from.Kind || to.N < from.N {
				return nil, fmt.Errorf("%q is not a range of one kind of strip", part)
			}
			for n := from.N; n <= to.N; n++ {
				add(stripRef{from.Kind, n})
			}
			continue
		}
		r, err := parseStrip(part)
		if err != nil {
			return nil, err
		}
		add(r)
	}
	return out, nil
}

// stripListOf reads a widget setting: a list of ids (["ch:3", ...]) or
// one text list. Bad entries are skipped.
func stripListOf(v any) []stripRef {
	var out []stripRef
	seen := map[stripRef]bool{}
	add := func(s string) {
		rs, err := parseStripList(s)
		if err != nil {
			return
		}
		for _, r := range rs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	switch x := v.(type) {
	case string:
		add(x)
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range x {
			add(s)
		}
	}
	return out
}

// channelStrip maps a core mute-widget channel number (1-48) to a strip:
// 1-40 are /ch, 41-48 are /aux 1-8 (the console has 40 input channels).
func channelStrip(ch int) (stripRef, error) {
	switch {
	case ch >= 1 && ch <= 40:
		return stripRef{"ch", ch}, nil
	case ch >= 41 && ch <= 48:
		return stripRef{"aux", ch - 40}, nil
	}
	return stripRef{}, fmt.Errorf("channel %d is outside 1..48 (1-40 = CH, 41-48 = AUX 1-8)", ch)
}

// ------------------------------------------------------------ colours

// wingColors is the console's colour index 1..18 as RGB, sampled from the
// swatch table "Appendix: WING Colors" (WING Remote Protocols 3.1 p.249).
// Approximate: the doc shows swatches without names or values.
var wingColors = [...]string{
	"", "#3e63cc", "#0180ff", "#5a33ff", "#00ced1", "#01b23e", "#96cc00", "#f2dd00", "#c06a1f", "#e02040",
	"#ff7a7a", "#ff33f6", "#a533ff", "#ffb81a", "#25c3ff", "#ff5a30", "#33e6a5", "#707070", "#e0e0e0",
}

// colorHex is the RGB of a WING colour index; grey when unknown.
func colorHex(i int) string {
	if i >= 1 && i < len(wingColors) {
		return wingColors[i]
	}
	return "#707070"
}

// ---------------------------------------------------------- fader law

// The WING fader runs -144 (-inf) .. +10 dB. The doc's worked examples give
// the normalized position above about -10 dB: 0.75 = 0 dB, 0.025 per dB,
// 1.0 = +10 dB. Below -10 dB the law isn't documented; the add-on uses the
// X32's segments (-10 at 0.5, -30 at 0.25, -60 at 0.0625, -inf at 0),
// which is close enough for drawing a fader. Exact dB always comes from the
// console; the add-on writes dB, never positions.
var faderLaw = [][2]float64{ // {position, dB}
	{0, -144}, {0.0625, -60}, {0.25, -30}, {0.5, -10}, {0.75, 0}, {1, 10},
}

// dbToPos turns dB into a 0..1 fader position.
func dbToPos(db float64) float64 {
	if math.IsNaN(db) || db <= -144 {
		return 0
	}
	if db >= 10 {
		return 1
	}
	for i := 1; i < len(faderLaw); i++ {
		p0, d0 := faderLaw[i-1][0], faderLaw[i-1][1]
		p1, d1 := faderLaw[i][0], faderLaw[i][1]
		if db <= d1 {
			if i == 1 { // -inf .. -60: keep the bottom short
				return p1 * (db - d0) / (d1 - d0)
			}
			return p0 + (p1-p0)*(db-d0)/(d1-d0)
		}
	}
	return 1
}

// posToDB is dbToPos's inverse, rounded to 0.1 dB.
func posToDB(p float64) float64 {
	if math.IsNaN(p) || p <= 0 {
		return -144
	}
	if p >= 1 {
		return 10
	}
	for i := 1; i < len(faderLaw); i++ {
		p0, d0 := faderLaw[i-1][0], faderLaw[i-1][1]
		p1, d1 := faderLaw[i][0], faderLaw[i][1]
		if p <= p1 {
			return math.Round((d0+(d1-d0)*(p-p0)/(p1-p0))*10) / 10
		}
	}
	return 10
}

// dbText shows a fader level: "-inf", "-3.0 dB", "+2.5 dB".
func dbText(db float64) string {
	if db <= -143.9 {
		return "-inf"
	}
	if db > 0.04 {
		return fmt.Sprintf("+%.1f dB", db)
	}
	return fmt.Sprintf("%.1f dB", db)
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// sortStrips sorts in console order (for lists built from maps).
func sortStrips(s []stripRef) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Kind != s[j].Kind {
			return kinds[s[i].Kind].order < kinds[s[j].Kind].order
		}
		return s[i].N < s[j].N
	})
}
