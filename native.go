package main

// The WING native protocol ("NRP", TCP 2222), from WING Remote Protocols
// 3.1 p.94-99 (see the repo README). Only what the add-on needs:
//
//   - framing: one TCP stream multiplexes 14 channels; DF Dn selects
//     channel n, a literal DF that would look like a selector is sent as
//     DF DE (the doc's sample tx/rx routines, reproduced exactly);
//   - channel 1 (audio engine): tokens. The add-on only reads pushed
//     changes (D7 <hash> <value>) and sends a keep-alive (DA DD);
//   - channel 3 (meters): D3 <port>, D4 <id> DC <family index>... DE, and
//     D4 <id> again every < 5 s to keep the stream alive;
//   - node definitions (DF len ... ), which the add-on gets over OSC
//     (<path> ,b dd) to learn the hash of every parameter it shows.

import (
	"encoding/binary"
	"errors"
	"math"
)

const (
	nrpEsc      = 0xDF
	nrpChanBase = 0xD0
	nrpChans    = 14

	chanAudio  = 1 // "DF D1": audio engine & control (the parameter tree)
	chanMeters = 3 // "DF D3": meter requests
)

// nrpEncoder escapes payload bytes for one channel (the doc's data_tx).
type nrpEncoder struct{ ch int }

func newNRPEncoder() *nrpEncoder { return &nrpEncoder{ch: -1} }

// frame returns the bytes to send for data on channel ch.
func (e *nrpEncoder) frame(ch int, data []byte) []byte {
	var out []byte
	if e.ch != ch {
		e.ch = ch
		out = append(out, nrpEsc, byte(nrpChanBase+ch))
	}
	esc := false
	for i := 0; i < len(data); i++ {
		db := data[i]
		if db == nrpEsc {
			esc = true
		} else {
			if esc && db >= nrpChanBase && db <= nrpChanBase+nrpChans {
				out = append(out, nrpEsc-1) // DF DE stands for a literal DF here
			}
			esc = false
		}
		out = append(out, db)
	}
	if esc {
		out = append(out, nrpEsc-1)
	}
	return out
}

// nrpDecoder undoes the framing (the doc's nrpc_data_rx), handing each
// payload byte to emit with its channel.
type nrpDecoder struct {
	escf bool
	ch   int
}

func newNRPDecoder() *nrpDecoder { return &nrpDecoder{ch: -1} }

func (d *nrpDecoder) feed(b []byte, emit func(ch int, db byte)) {
	for _, db := range b {
		if db == nrpEsc && !d.escf {
			d.escf = true
			continue
		}
		if d.escf && db != nrpEsc {
			d.escf = false
			if db == nrpEsc-1 {
				db = nrpEsc
			} else if db >= nrpChanBase && db < nrpChanBase+nrpChans {
				d.ch = int(db - nrpChanBase)
				continue
			} else if d.ch >= 0 {
				emit(d.ch, nrpEsc)
			}
		}
		if d.ch >= 0 {
			emit(d.ch, db)
		}
	}
}

// ------------------------------------------------------------ values

// nval is one decoded value token.
type nval struct {
	kind byte // 'i' int, 'f' engineering float, 'n' normalized float, 's' string, 0 none
	i    int64
	f    float64
	s    string
}

// Token constants (Table 2, p.97).
const (
	tokFalse    = 0x00
	tokTrue     = 0x01
	tokIntMax   = 0x3F // 02..3F = int 2..63
	tokIdxLo    = 0x40 // 40..7F = node index 1..64
	tokStrLo    = 0x80 // 80..BF = string of 1..64 bytes
	tokNameLo   = 0xC0 // C0..CF = node name of 1..16 bytes
	tokEmptyStr = 0xD0
	tokStr      = 0xD1 // len.b + bytes, 1..256
	tokIdxW     = 0xD2
	tokI16      = 0xD3
	tokI32      = 0xD4
	tokF32      = 0xD5
	tokRawF     = 0xD6
	tokHash     = 0xD7
	tokClick    = 0xD8
	tokStep     = 0xD9
	tokRoot     = 0xDA
	tokUp       = 0xDB
	tokReq      = 0xDC
	tokDefReq   = 0xDD
	tokEnd      = 0xDE
	tokDef      = 0xDF
)

// nrpParser reads channel-1 tokens and reports D7 <hash> <value> pairs.
// Definitions (DF ...) are passed to onDef. Unknown bytes are skipped so
// one surprise doesn't stop the stream.
type nrpParser struct {
	buf     []byte
	cur     uint32
	haveCur bool
	onValue func(hash uint32, v nval)
	onDef   func(def []byte)
}

func (p *nrpParser) feed(b ...byte) {
	p.buf = append(p.buf, b...)
	for {
		n := p.step()
		if n == 0 {
			break
		}
		p.buf = p.buf[n:]
	}
	if len(p.buf) == 0 {
		p.buf = nil
	}
}

var errShort = errors.New("short")

// step consumes one token; 0 = need more bytes.
func (p *nrpParser) step() int {
	b := p.buf
	if len(b) == 0 {
		return 0
	}
	t := b[0]
	val := func(v nval, n int) int {
		if p.haveCur && p.onValue != nil {
			p.onValue(p.cur, v)
		}
		p.haveCur = false
		return n
	}
	need := func(n int) bool { return len(b) >= n }
	switch {
	case t == tokFalse || t == tokTrue || (t >= 0x02 && t <= tokIntMax):
		return val(nval{kind: 'i', i: int64(t)}, 1)
	case t >= tokIdxLo && t < tokStrLo: // navigation: node index
		p.haveCur = false
		return 1
	case t >= tokStrLo && t < tokNameLo:
		n := int(t&0x3F) + 1
		if !need(1 + n) {
			return 0
		}
		return val(nval{kind: 's', s: string(b[1 : 1+n])}, 1+n)
	case t >= tokNameLo && t < tokEmptyStr:
		n := int(t&0x0F) + 1
		if !need(1 + n) {
			return 0
		}
		p.haveCur = false
		return 1 + n
	}
	switch t {
	case tokEmptyStr:
		return val(nval{kind: 's'}, 1)
	case tokStr:
		if !need(2) {
			return 0
		}
		n := int(b[1]) + 1
		if !need(2 + n) {
			return 0
		}
		return val(nval{kind: 's', s: string(b[2 : 2+n])}, 2+n)
	case tokIdxW:
		if !need(3) {
			return 0
		}
		p.haveCur = false
		return 3
	case tokI16:
		if !need(3) {
			return 0
		}
		return val(nval{kind: 'i', i: int64(int16(binary.BigEndian.Uint16(b[1:])))}, 3)
	case tokI32:
		if !need(5) {
			return 0
		}
		return val(nval{kind: 'i', i: int64(int32(binary.BigEndian.Uint32(b[1:])))}, 5)
	case tokF32, tokRawF:
		if !need(5) {
			return 0
		}
		f := float64(math.Float32frombits(binary.BigEndian.Uint32(b[1:])))
		k := byte('f')
		if t == tokRawF {
			k = 'n'
		}
		return val(nval{kind: k, f: f}, 5)
	case tokHash:
		if !need(5) {
			return 0
		}
		p.cur = binary.BigEndian.Uint32(b[1:])
		p.haveCur = true
		return 5
	case tokStep:
		if !need(2) {
			return 0
		}
		p.haveCur = false
		return 2
	case tokClick, tokRoot, tokUp, tokReq, tokDefReq, tokEnd:
		p.haveCur = false
		return 1
	case tokDef:
		n, hdr, ok := defLen(b)
		if !ok {
			return 0
		}
		if !need(hdr + n) {
			return 0
		}
		if p.onDef != nil {
			p.onDef(b[hdr : hdr+n])
		}
		p.haveCur = false
		return hdr + n
	}
	p.haveCur = false
	return 1 // E0..FF: not used; skip
}

// defLen reads a DF record's length: DF len.w, or DF 0000 len.l.
func defLen(b []byte) (n, hdr int, ok bool) {
	if len(b) < 3 {
		return 0, 0, false
	}
	n = int(binary.BigEndian.Uint16(b[1:]))
	if n != 0 {
		return n, 3, true
	}
	if len(b) < 7 {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint32(b[3:])), 7, true
}

// ------------------------------------------------------- definitions

// nodeDef is one parsed node-definition record (p.97, p.29).
type nodeDef struct {
	Parent, Hash uint32
	Index        int
	Name, Long   string
	Flags        uint16
	Min, Max     float64
	Steps        int
	Items        []string // enum: item names
	ItemLong     []string // enum / fenum: long names
	MaxLen       int      // string
}

// Type is the node type from the flags (bits 4-7).
func (d nodeDef) Type() int { return int(d.Flags>>4) & 0x0F }

// Unit is the unit from the flags (bits 0-3).
func (d nodeDef) Unit() int { return int(d.Flags) & 0x0F }

// ReadOnly is flag bit 9.
func (d nodeDef) ReadOnly() bool { return d.Flags&(1<<9) != 0 }

const (
	typeNode = iota
	typeLinF
	typeLogF
	typeFader
	typeInt
	typeEnum
	typeFEnum
	typeString
)

// parseDef parses the body of one DF record (after the length).
func parseDef(b []byte) (nodeDef, error) {
	var d nodeDef
	r := reader{b: b}
	d.Parent = r.u32()
	d.Hash = r.u32()
	d.Index = int(r.u16())
	d.Name = r.str8()
	d.Long = r.str8()
	d.Flags = r.u16()
	switch d.Type() {
	case typeLinF, typeLogF:
		d.Min, d.Max, d.Steps = float64(r.f32()), float64(r.f32()), int(r.u32())
	case typeInt:
		d.Min, d.Max = float64(int32(r.u32())), float64(int32(r.u32()))
	case typeEnum:
		n := int(r.u16())
		for i := 0; i < n && r.err == nil; i++ {
			d.Items = append(d.Items, r.str8())
			d.ItemLong = append(d.ItemLong, r.str8())
		}
	case typeFEnum:
		n := int(r.u16())
		for i := 0; i < n && r.err == nil; i++ {
			v := r.f32()
			d.Items = append(d.Items, trimFloat(float64(v)))
			d.ItemLong = append(d.ItemLong, r.str8())
		}
	case typeString:
		d.MaxLen = int(r.u16())
	}
	return d, r.err
}

// parseDefs parses a dd reply: DF records until DE (or the end).
func parseDefs(b []byte) ([]nodeDef, error) {
	var out []nodeDef
	for len(b) > 0 {
		switch b[0] {
		case tokEnd:
			return out, nil
		case tokDef:
			n, hdr, ok := defLen(b)
			if !ok || len(b) < hdr+n {
				return out, errShort
			}
			d, err := parseDef(b[hdr : hdr+n])
			if err != nil {
				return out, err
			}
			out = append(out, d)
			b = b[hdr+n:]
		default:
			return out, errors.New("unexpected byte in a node definition")
		}
	}
	return out, nil
}

// encodeDef builds one DF record (the fake console uses it).
func encodeDef(d nodeDef) []byte {
	var body []byte
	body = binary.BigEndian.AppendUint32(body, d.Parent)
	body = binary.BigEndian.AppendUint32(body, d.Hash)
	body = binary.BigEndian.AppendUint16(body, uint16(d.Index))
	body = append(append(body, byte(len(d.Name))), d.Name...)
	body = append(append(body, byte(len(d.Long))), d.Long...)
	body = binary.BigEndian.AppendUint16(body, d.Flags)
	switch d.Type() {
	case typeLinF, typeLogF:
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(d.Min)))
		body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(d.Max)))
		body = binary.BigEndian.AppendUint32(body, uint32(d.Steps))
	case typeInt:
		body = binary.BigEndian.AppendUint32(body, uint32(int32(d.Min)))
		body = binary.BigEndian.AppendUint32(body, uint32(int32(d.Max)))
	case typeEnum:
		body = binary.BigEndian.AppendUint16(body, uint16(len(d.Items)))
		for i, it := range d.Items {
			long := ""
			if i < len(d.ItemLong) {
				long = d.ItemLong[i]
			}
			body = append(append(body, byte(len(it))), it...)
			body = append(append(body, byte(len(long))), long...)
		}
	case typeString:
		body = binary.BigEndian.AppendUint16(body, uint16(d.MaxLen))
	}
	out := []byte{tokDef}
	if len(body) <= 0xFFFF {
		out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
	} else {
		out = binary.BigEndian.AppendUint16(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	}
	return append(out, body...)
}

// encodeValue builds a value token (the fake console's events; the
// add-on itself writes over OSC).
func encodeValue(v nval) []byte {
	switch v.kind {
	case 'i':
		switch {
		case v.i >= 0 && v.i <= tokIntMax:
			return []byte{byte(v.i)}
		case v.i >= math.MinInt16 && v.i <= math.MaxInt16:
			return binary.BigEndian.AppendUint16([]byte{tokI16}, uint16(int16(v.i)))
		default:
			return binary.BigEndian.AppendUint32([]byte{tokI32}, uint32(int32(v.i)))
		}
	case 'f':
		return binary.BigEndian.AppendUint32([]byte{tokF32}, math.Float32bits(float32(v.f)))
	case 'n':
		return binary.BigEndian.AppendUint32([]byte{tokRawF}, math.Float32bits(float32(v.f)))
	case 's':
		n := len(v.s)
		switch {
		case n == 0:
			return []byte{tokEmptyStr}
		case n <= 64:
			return append([]byte{byte(tokStrLo + n - 1)}, v.s...)
		default:
			if n > 256 {
				n = 256
			}
			return append([]byte{tokStr, byte(n - 1)}, v.s[:n]...)
		}
	}
	return nil
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = errShort
		return make([]byte, n)
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}
func (r *reader) u16() uint16  { return binary.BigEndian.Uint16(r.take(2)) }
func (r *reader) u32() uint32  { return binary.BigEndian.Uint32(r.take(4)) }
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) str8() string {
	n := int(r.take(1)[0])
	return string(r.take(n))
}

// ------------------------------------------------------------ meters

// Meter families (p.98). V2 strip families carry 11 words per strip.
var meterFamily = map[string]struct {
	tok   byte
	words int
}{
	"ch": {0xAB, 11}, "aux": {0xAC, 11}, "bus": {0xAD, 11}, "main": {0xAE, 11}, "mtx": {0xAF, 11},
	"dca": {0xA5, 4},
}

// meterRequest builds the channel-3 payload that defines report id for
// the strips (in order) and sends it to udpPort.
func meterRequest(id uint32, udpPort int, strips []stripRef) []byte {
	b := binary.BigEndian.AppendUint16([]byte{tokI16}, uint16(udpPort))
	b = append(b, tokI32)
	b = binary.BigEndian.AppendUint32(b, id)
	b = append(b, tokReq)
	for _, s := range strips {
		f, ok := meterFamily[s.Kind]
		if !ok || s.N < 1 || s.N > 128 {
			continue
		}
		b = append(b, f.tok, byte(s.N-1))
	}
	return append(b, tokEnd)
}

// meterRenew keeps report id alive (resend every < 5 s).
func meterRenew(id uint32) []byte {
	return binary.BigEndian.AppendUint32([]byte{tokI32}, id)
}

// meterWords is how many int16 words a strip's meter data takes.
func meterWords(s stripRef) int {
	if f, ok := meterFamily[s.Kind]; ok {
		return f.words
	}
	return 0
}
