// Behringer WING: a jadeck add-on. Mutes, faders, DCAs, mute groups,
// scenes, the USB recorder and player, talkback, solo and meters, every
// change read back from the console. X32 / M32 mutes (experimental) and
// other OSC desks through the same connection type.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// Version is set by the release build.
var Version = "2.0.0"

func main() { addon.Serve(spec()) }

const changelog = "2.0: a full WING control surface. Mutes with the console's names and colours (effective mute shown), " +
	"faders and DCAs with live LED meters, a meter bridge, mute groups, scenes (list, go, next, previous, by tag), " +
	"USB recorder (record, stop and new file are press-and-hold, 5 s by default) and player, talkback, clear solo and a console status tile. Widgets pick their own strips and order. " +
	"Live updates over the WING's native port, consoles on the network listed when adding a connection, locks and " +
	"console errors reported instead of silently ignored, a read-only option and a read-only probe. " +
	"Fixes: channels 41-48 are AUX 1-8 (the WING has 40 input channels); WING replies (text, float, int) are read correctly."

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{
			ID: "behringer-wing", Name: "Behringer WING", Version: Version, Publisher: "jadeck", APIVersion: addon.APIVersion,
			Repo:        "toolingdev/jadeck-addon-behringer-wing",
			Description: "Mutes, faders, DCAs, scenes, USB recorder, talkback and meters for the WING. X32/M32 mutes too.",
			Reaches:     "The consoles you add as connections (WING: OSC UDP 2223, native TCP 2222, meters over UDP)",
			ConnectionTypes: []addon.ConnectionType{{
				ID: "osc", Label: "Behringer WING", Capabilities: []string{addon.CapMixer},
				Description: "Pick your console from the list (same network) or type its IP address (WING: Setup → Network). " +
					"Advanced fields are only for other OSC desks.",
				Fields:     connectionFields(),
				Sample:     map[string]any{"preset": "wing", "host": "192.168.1.40", "muteStrips": "ch:1-16", "faderStrips": "ch:1-8, main:1"},
				SampleTest: "Console answered — WING · Main WING (WING (full size), firmware 3.1.2) · live updates",
			}},
			Actions:   actionDefs(),
			Widgets:   widgetDefs(),
			Changelog: changelog,
		},
		Open: map[string]addon.OpenFunc{"osc": openMixer},
		Options: map[string]func() []addon.Option{
			"wing.consoles": consoleOptions,
			"wing.strips":   func() []addon.Option { return stripOptions(nil, true) },
			"wing.faders":   func() []addon.Option { return stripOptions(nil, false) },
		},
	}
}

func connectionFields() []addon.Field {
	return []addon.Field{
		{Key: "preset", Label: "Console", Type: addon.FieldSelect, Default: "wing", Required: true, Options: []addon.Option{
			{Value: "wing", Label: "Behringer WING"}, {Value: "x32", Label: "Behringer X32 / Midas M32 (mutes only, experimental)"},
			{Value: "", Label: "Other OSC desk (mutes only, fill in advanced)"}}},
		{Key: "host", Label: "Console IP address", Type: addon.FieldText, Placeholder: "10.1.10.50", Required: true, OptionsFrom: "wing.consoles",
			Help: "Consoles on this network are listed. On the WING: Setup → Network shows the address."},
		{Key: "readOnly", Label: "Read only", Type: addon.FieldBool, Default: false,
			Help: "Show the console but refuse every change (mutes, faders, scenes, recorder…)."},
		{Key: "muteStrips", Label: "Mute grid strips", Type: addon.FieldText, Placeholder: "ch:1-16, aux:1-2, main:1",
			Help: "What a Mute grid shows when the widget doesn't pick its own. " + stripHelp + "."},
		{Key: "faderStrips", Label: "Fader strips", Type: addon.FieldText, Placeholder: "ch:1-8, main:1",
			Help: "What a Faders widget shows when it doesn't pick its own."},
		{Key: "dcaStrips", Label: "DCA strips", Type: addon.FieldText, Placeholder: "dca:1-8",
			Help: "What DCA widgets show when they don't pick their own."},
		{Key: "meterStrips", Label: "Meter strips", Type: addon.FieldText, Placeholder: "ch:1-16, main:1",
			Help: "What a Meter bridge shows when it doesn't pick its own."},
		{Key: "meters", Label: "Meters", Type: addon.FieldSelect, Default: "auto", Options: []addon.Option{
			{Value: "auto", Label: "While a widget shows them"}, {Value: "off", Label: "Off"}},
			Help: "Meters come back from the console as UDP: this computer's firewall must let them in."},
		{Key: "labels", Label: "Fallback names", Type: addon.FieldText, Placeholder: "ch:3=Pulpit, ch:4=Choir",
			Help: "Used only for strips the console leaves unnamed."},
		{Key: "port", Label: "OSC port", Type: addon.FieldNumber, Advanced: true, Help: "Blank = the console's default (WING 2223)."},
		{Key: "nativePort", Label: "Native port (WING)", Type: addon.FieldNumber, Advanced: true, Help: "Blank = 2222."},
		{Key: "meterPort", Label: "Meter UDP port (WING)", Type: addon.FieldNumber, Advanced: true,
			Help: "Blank = any free port. Set one to open it in a firewall."},
		{Key: "muteAddress", Label: "Mute address", Type: addon.FieldText, Advanced: true, Placeholder: "/ch/{n}/mute", Help: "Other OSC desks: {n} = channel, {nn} = two digits."},
		{Key: "mutedValue", Label: "Value meaning muted", Type: addon.FieldNumber, Advanced: true},
		{Key: "probe", Label: "Probe address", Type: addon.FieldText, Advanced: true, Placeholder: "/?"},
		{Key: "maxChannel", Label: "Channels", Type: addon.FieldNumber, Advanced: true},
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
	if c.Name == "wing" {
		var wc wingConfig
		if err := json.Unmarshal(raw, &wc); err != nil {
			return nil, err
		}
		return openWing(wc)
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
	c.Host = strings.TrimSpace(c.Host)
	if c.Host == "" {
		return nil, fmt.Errorf("enter the console's IP address: %w", addon.ErrNotConfigured)
	}
	if p.MuteAddress == "" || p.Port == 0 {
		return nil, fmt.Errorf("osc mixer needs a preset or port + muteAddress")
	}
	return New(net.JoinHostPort(c.Host, strconv.Itoa(p.Port)), p), nil
}
