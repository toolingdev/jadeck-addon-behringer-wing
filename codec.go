// A small OSC 1.0 codec, plus (mixer.go) a generic OSC mute driver used
// for the X32/M32 and "other OSC desk" presets. Consoles differ only in
// addresses and polarity, so each is a preset of one driver:
//
//	x32   Behringer X32/M32, Midas   UDP 10023 /ch/{nn}/mix/on  0 = muted   probe /info
//	(other) port + muteAddress + mutedValue + probe from the advanced fields
//
// The WING itself has its own client (wing.go): OSC on UDP 2223 for reads
// and writes, the native protocol on TCP 2222 for live changes and meters.
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

// Encode builds one OSC message. args: bool, int, int32, float32, float64,
// string, []byte (blob).
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
		case []byte:
			tags += "b"
			body = binary.BigEndian.AppendUint32(body, uint32(len(v)))
			body = pad4(append(body, v...))
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
// int32, float32, string or []byte (blob). A garbled reply is an error,
// never trusted.
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
		case 'b':
			if off+4 > len(b) {
				return "", nil, errMalformed
			}
			n := int(binary.BigEndian.Uint32(b[off:]))
			off += 4
			if n < 0 || off+n > len(b) {
				return "", nil, errMalformed
			}
			vals = append(vals, append([]byte(nil), b[off:off+n]...))
			off += (n + 3) / 4 * 4
		default:
			return "", nil, fmt.Errorf("osc: unsupported typetag %q", t)
		}
	}
	return addr, vals, nil
}
