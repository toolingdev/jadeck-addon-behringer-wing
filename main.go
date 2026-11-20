// Behringer WING: a PTZ controller add-on. Channel mutes over OSC, every
// change read back from the console. X32 / M32 experimental.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// Version is set by the release build.
var Version = "1.0.0"

func main() { addon.Serve(spec()) }

var sample = map[string]any{"/api/mixer/status": map[string]any{
	"label": "Behringer WING", "configured": true, "connected": true, "console": "WING",
	"channels": []map[string]any{
		{"slot": 1, "label": "Main", "channel": 1, "muted": false},
		{"slot": 2, "label": "Lectern", "channel": 2, "muted": false},
		{"slot": 3, "label": "Wireless 1", "channel": 3, "muted": true},
		{"slot": 4, "label": "Wireless 2", "channel": 4, "muted": false},
	},
}}

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{
			ID: "behringer-wing", Name: "Behringer WING", Version: Version, Publisher: "PTZ Controller", APIVersion: addon.APIVersion, Repo: "toolingdev/ptz-addon-behringer-wing",
			Description: "Mute and unmute WING channels, every change read back. X32/M32 experimental.",
			Reaches:     "The consoles you add as connections (OSC on the local network)",
			ConnectionTypes: []addon.ConnectionType{{
				ID: "osc", Label: "Behringer WING", Capabilities: []string{addon.CapMixer},
				Description: "Pick your console and enter its IP address (WING: Setup → Network). Advanced fields are only for other OSC desks.",
				Fields: []addon.Field{
					{Key: "preset", Label: "Console", Type: "select", Default: "wing", Required: true, Options: []addon.Option{
						{Value: "wing", Label: "Behringer WING"}, {Value: "x32", Label: "Behringer X32 / Midas M32 (experimental)"}, {Value: "", Label: "Other OSC desk (fill in advanced)"}}},
					{Key: "host", Label: "Console IP address", Type: "text", Placeholder: "10.1.10.50", Required: true,
						Help: "On the WING: Setup → Network shows the address."},
					{Key: "port", Label: "OSC port", Type: "number", Advanced: true, Help: "Blank = the console's default."},
					{Key: "muteAddress", Label: "Mute address", Type: "text", Advanced: true, Placeholder: "/ch/{n}/mute", Help: "{n} = channel, {nn} = two digits."},
					{Key: "mutedValue", Label: "Value meaning muted", Type: "number", Advanced: true},
					{Key: "probe", Label: "Probe address", Type: "text", Advanced: true, Placeholder: "/?"},
					{Key: "maxChannel", Label: "Channels", Type: "number", Advanced: true},
				},
				Sample:     map[string]any{"preset": "wing", "host": "192.168.1.40"},
				SampleTest: "Console answered — WING",
			}},
			Widgets: []addon.WidgetDef{
				{Kind: "core", Type: "audio.mute", Label: "Mute button", Sample: sample, Settings: map[string]any{"slot": "2"}},
				{Kind: "core", Type: "audio.favorites", Label: "Channel favorites", Sample: sample},
			},
			Changelog: "First release as its own add-on (was built into the controller).",
		},
		Open: map[string]addon.OpenFunc{"osc": openMixer},
	}
}

func openMixer(raw json.RawMessage) (any, error) {
	var c struct {
		Name string `json:"preset"`
		Host string `json:"host"`
		Preset
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	p := Preset{MaxChannel: 64}
	if c.Name != "" {
		pp, ok := Presets[c.Name]
		if !ok {
			return nil, fmt.Errorf("unknown OSC preset %q", c.Name)
		}
		p = pp
	}
	// Explicit fields override the preset.
	if c.Port != 0 {
		p.Port = c.Port
	}
	if c.MuteAddress != "" {
		p.MuteAddress = c.MuteAddress
	}
	if c.Probe != "" {
		p.Probe = c.Probe
	}
	if c.MaxChannel != 0 {
		p.MaxChannel = c.MaxChannel
	}
	if strings.Contains(string(raw), `"mutedValue"`) {
		p.MutedValue = c.MutedValue
	}
	if c.Host == "" {
		return nil, fmt.Errorf("enter the console's IP address: %w", addon.ErrNotConfigured)
	}
	if p.MuteAddress == "" || p.Port == 0 {
		return nil, fmt.Errorf("osc mixer needs a preset or port + muteAddress")
	}
	return New(net.JoinHostPort(c.Host, strconv.Itoa(p.Port)), p), nil
}
