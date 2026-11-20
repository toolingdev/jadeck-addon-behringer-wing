// Package osc is a generic OSC (Open Sound Control) mixer driver plus a
// small OSC 1.0 codec. Consoles differ only in addresses and polarity,
// so each console is a preset of one driver:
//
//	wing  Behringer WING   UDP 2223  /ch/{n}/mute       1 = muted   probe /?
//	x32   Behringer X32/M32, Midas   UDP 10023 /ch/{nn}/mix/on  0 = muted   probe /info
//
// addon.json:
//
//	{"driver": "osc", "preset": "wing", "host": "10.1.10.50"}
//	{"driver": "osc", "preset": "x32",  "host": "10.1.10.60"}
//	{"driver": "osc", "host": "...", "port": 9000, "muteAddress": "/strip/{n}/mute",
//	 "mutedValue": 1, "probe": "/info", "maxChannel": 32}
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func oscString(s string) []byte { return pad4(append([]byte(s), 0)) }

// Encode builds one OSC message. args: int, int32, float32, float64, string.
func Encode(address string, args ...any) ([]byte, error) {
	tags := ","
	var body []byte
	for _, a := range args {
		switch v := a.(type) {
		case bool:
			tags += "i"
			n := int32(0)
			if v {
				n = 1
			}
			body = binary.BigEndian.AppendUint32(body, uint32(n))
		case int:
			tags += "i"
			body = binary.BigEndian.AppendUint32(body, uint32(int32(v)))
		case int32:
			tags += "i"
			body = binary.BigEndian.AppendUint32(body, uint32(v))
		case float32:
			tags += "f"
			body = binary.BigEndian.AppendUint32(body, math.Float32bits(v))
		case float64:
			tags += "f"
			body = binary.BigEndian.AppendUint32(body, math.Float32bits(float32(v)))
		case string:
			tags += "s"
			body = append(body, oscString(v)...)
		default:
			return nil, fmt.Errorf("osc: cannot encode %T", a)
		}
	}
	out := oscString(address)
	out = append(out, oscString(tags)...)
	return append(out, body...), nil
}

var errMalformed = errors.New("osc: malformed message")

func readString(b []byte, off int) (string, int, error) {
	end := -1
	for i := off; i < len(b); i++ {
		if b[i] == 0 {
			end = i
			break
		}
	}
	if end < 0 {
		return "", 0, errMalformed
	}
	s := string(b[off:end])
	n := off + (len(s)+4)/4*4
	return s, n, nil
}

// Decode parses one OSC message into (address, values). Values are
// int32, float32 or string. A garbled reply is an error, never trusted.
func Decode(b []byte) (string, []any, error) {
	if len(b) == 0 || b[0] != '/' {
		return "", nil, errMalformed
	}
	addr, off, err := readString(b, 0)
	if err != nil {
		return "", nil, err
	}
	if off >= len(b) {
		return addr, nil, nil
	}
	tags, off, err := readString(b, off)
	if err != nil || !strings.HasPrefix(tags, ",") {
		return "", nil, errMalformed
	}
	var vals []any
	for _, t := range tags[1:] {
		switch t {
		case 'i', 'f':
			if off+4 > len(b) {
				return "", nil, errMalformed
			}
			u := binary.BigEndian.Uint32(b[off:])
			off += 4
			if t == 'i' {
				vals = append(vals, int32(u))
			} else {
				vals = append(vals, math.Float32frombits(u))
			}
		case 's':
			var s string
			s, off, err = readString(b, off)
			if err != nil {
				return "", nil, err
			}
			vals = append(vals, s)
		default:
			return "", nil, fmt.Errorf("osc: unsupported typetag %q", t)
		}
	}
	return addr, vals, nil
}
