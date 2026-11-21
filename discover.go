package main

// Discovery (WING Remote Protocols 3.1 p.16): send the 5 bytes "WING?" to
// UDP 2222 (broadcast on the LAN); each console answers
// "WING,<ip>,<name>,<model>,<serial>,<firmware>". Broadcasts don't cross
// routers or Tailscale; type the address in those cases.

import (
	"context"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/toolingdev/jadeck/sdk/addon"
)

const discoveryPort = 2222

// discoverTargets are where "WING?" goes: the limited broadcast and each
// IPv4 interface's own broadcast address.
func discoverTargets() []string {
	out := []string{net.JoinHostPort("255.255.255.255", strconv.Itoa(discoveryPort))}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			mask := n.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			if len(mask) != 4 {
				continue
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, net.JoinHostPort(b.String(), strconv.Itoa(discoveryPort)))
		}
	}
	return out
}

// discover asks every target and collects answers until timeout.
func discover(targets []string, timeout time.Duration) []identity {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil
	}
	defer pc.Close()
	for _, t := range targets {
		if ua, err := net.ResolveUDPAddr("udp4", t); err == nil {
			pc.WriteTo([]byte("WING?"), ua)
		}
	}
	pc.SetReadDeadline(time.Now().Add(timeout))
	seen := map[string]identity{}
	buf := make([]byte, 1024)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		id, ok := parseIdentity(string(buf[:n]))
		if !ok {
			continue
		}
		if id.IP == "" || id.IP == "0.0.0.0" {
			if ua, ok := from.(*net.UDPAddr); ok {
				id.IP = ua.IP.String()
			}
		}
		seen[id.IP] = id
	}
	out := make([]identity, 0, len(seen))
	for _, id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out
}

// consoleOptions is the "wing.consoles" option source for the host field.
func consoleOptions() []addon.Option {
	out := []addon.Option{}
	for _, id := range discover(discoverTargets(), 800*time.Millisecond) {
		out = append(out, addon.Option{Value: id.IP, Label: id.IP + " · " + nz(id.Name, "WING") + " (" + id.ModelName() + ", fw " + nz(id.Firmware, "?") + ")"})
	}
	return out
}

// stripOptions lists strips for the "wing.strips" (with mute groups) and
// "wing.faders" option sources: value "ch:3", label "CH 3 · <name>" when
// w (the widget's connection) knows the name.
func stripOptions(w *Wing, withGroups bool) []addon.Option {
	list := faderStrips()
	if withGroups {
		list = allStrips()
	}
	out := []addon.Option{}
	for _, s := range list {
		lbl := s.Label()
		if w != nil {
			if v := w.view(s); v.Name != "" {
				lbl += " · " + v.Name
			}
		}
		out = append(out, addon.Option{Value: s.ID(), Label: lbl})
	}
	return out
}

// AddonOptions answers option lists for this connection (widget fields).
func (w *Wing) AddonOptions(ctx context.Context, name string) ([]addon.Option, error) {
	switch name {
	case "wing.strips":
		return stripOptions(w, true), nil
	case "wing.faders":
		return stripOptions(w, false), nil
	}
	return nil, nil
}
