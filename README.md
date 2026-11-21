# Behringer WING — jadeck add-on

Mutes, faders, DCAs, mute groups, scenes, the USB recorder and player,
talkback, solo, channel processing, FX and meters for the Behringer WING,
with every change read back from the console. X32 / M32 (and other OSC
desks) get channel mutes only, as before.

Version 2.0.0. Everything below was checked against a **fake WING** built
from the protocol document (see "What is verified"); run the probe once on
the real console before relying on it in a service.

## Setup

1. Put this computer on the console's network (the WING: Setup → Network
   shows its address).
2. jadeck: Settings → Connections → Add → Behringer WING. Consoles on the
   same network are listed under "Console IP address" (UDP discovery); on
   Tailscale or across routers, type the address.
3. Leave **Read only** off to control the console; turn it on for a
   display-only screen (every change is refused, with that reason).
4. Add widgets. Most pick their own strips in their settings (order
   matters); the connection's strip lists are the defaults.

Nothing is written to the console when a connection opens. The add-on
never changes phantom power, source patching, insert slot assignments,
processing models, routing, locks or network settings.

### Ports and firewall

| Port | Direction | What |
|---|---|---|
| UDP 2223 | jadeck → console, replies back | OSC: every read and write |
| TCP 2222 | jadeck → console | Native protocol: live changes pushed by the console, meter requests |
| UDP 2222 | broadcast → console | Discovery ("WING?") |
| UDP (any, or the "Meter UDP port" field) | console → jadeck | Meter data |

Meters arrive as UDP datagrams the console sends *to* jadeck, so the
computer running jadeck must accept inbound UDP from the console (Windows
asks the first time; allow it on Private networks). Set "Meter UDP port"
to a fixed number to write a firewall rule for it. Meters only run while a
widget shows them and stop about 10 s after the last one closes.

The console has no passwords: anything that can reach these ports can
control it. Keep it on a private network.

## How it talks to the console (transport choice)

- **Reads and writes: OSC on UDP 2223.** Best documented (exact reply
  shapes, error replies), addressed by path. Every write is followed by a
  read-back; a `/*` error reply is reported as the console's message, and a
  value that didn't change is reported instead of silently ignored.
- **Live changes: the native protocol on TCP 2222.** The console pushes
  every change to every connected client (24 allowed on firmware 3.1+), so
  nothing has to be claimed. The events carry 32-bit node hashes; the hash
  function isn't documented, so the add-on learns each parameter's hash
  from the console's own node definitions (`<node> ,b dd` over OSC) and
  maps events back to paths. It never takes the console's single OSC
  subscription (`/*s`), which another OSC tool might be using.
- **Meters: native channel 3 request, UDP data**, renewed every 3 s
  (the console drops a request after 5 s).
- **Fallback:** when TCP 2222 can't be reached (blocked, 24 clients
  already) or the hashes can't be learned, the add-on polls what is on
  screen over OSC every 2 s. The console status tile says which mode is on.
  Meters need the native link.

Risks: the hash learning and the native event stream are verified only
against the fake console; if the real console's definitions differ, live
updates fall back to 2 s polling (still correct, slower). Behringer's own
apps (WING Edit, Co-Pilot) are other native clients and don't interfere.

## Locks

| Lock | Where on the console | What the add-on does |
|---|---|---|
| OSC lock (`/$ctl/osc/ronly`) | Setup → Remote | Refuses writes with "the console's OSC lock is on"; buttons show disabled |
| TCP lock (`/$syscfg/tcplock`) | Setup → Remote | Shown on the status tile (writes go over OSC) |
| Surface lock (`/$ctl/$stat/cnslock`) | the desk | Shown on the status tile |

## Widgets

| Widget | Kind | Settings |
|---|---|---|
| Mutes | buttons | strips, in order; red = muted, "(group)" = muted by a DCA / mute group only |
| DCA mutes, Mute groups | buttons | DCAs |
| Faders | frame | strips + order, meters, pre/post, mute + solo buttons, gain knob, edit gain here, dB readout, scale |
| DCA faders | frame | same, DCAs only |
| Channel strip | frame | the channel; input gain/trim/polarity, filters, gate, EQ, compressor, sends, mains, pan, inserts as knobs, switches and lists from the console's own definitions; locked until tapped, locks again after 30 s |
| FX | frame | FX slots; model (read only), mix and the model's parameters |
| Meter bridge | frame | strips + order, pre/post |
| Main fader | slider | MAIN 1 |
| Scenes / Scene control | sections / buttons | list + current; previous / next |
| USB recorder / Recorder | buttons / text | which buttons, columns, hold time (3–10 s, default 5) |
| USB player / Player | buttons / text | which buttons, columns |
| Talkback, Solo | buttons | |
| Console status, Console tools, Probe report | text / buttons / text | |
| Mute button, Channel favorites | jadeck's own | channels 1-40 = CH, 41-48 = AUX 1-8 |

Recording is held to confirm: **Record, Stop and New file** run only after
the button has been pressed and held (5 s by default, the widget's "Hold to
record / stop" setting), and the add-on itself refuses `rec_start`,
`rec_stop` and `rec_new_file` unless the call carries `confirmed: true`
(the held button sends it; macros and the agent must pass it). Pause is a
tap. No frame widget has a recorder button, so the frames need no hold
helper.

Faders: drag the cap or anywhere on the track (relative, no jump), Shift
or a second finger for fine moves, double-tap for 0 dB. Knobs: drag up and
down or use the wheel, double-tap resets bipolar knobs to the centre.

Stereo comes from the console: an input strip is stereo when its source is
(`in/set/$mode` ST or M/S), a bus, main or matrix unless `busmono` is on.
Stereo strips show an ST chip and L/R meters; their pan is labelled
Balance. The WING has no linked channel pairs (a stereo source uses one
strip), so pickers list every strip once.

## Probe (read only) — run once at the console

1. Connect jadeck to the console (Read only on is fine).
2. Add the **Console tools** and **Probe report** widgets, press **Run
   probe**. Before probing: mute one channel only through a DCA or mute
   group (effective-mute values), move a fader at the desk (live events),
   have one stereo source and one mono bus (stereo detection).
3. Copy the report (or run the `probe` action from a macro / the agent)
   and keep it with these notes.

The probe only reads; the one request it makes is two seconds of meters.

## What is verified, and what isn't

Verified against the fake console in `fakewing_test.go`, which follows WING
Remote Protocols 3.1 (P.-G. Maillot) and the firmware 3.1–3.1.2 notes: OSC
triplet replies (`,sfi` / `,sff`), `/*` errors, silent drops under the OSC
lock, `,b dd` definitions, native framing (the document's byte examples),
pushed `D7` events, keep-alive, 24-client limit, meter requests and V2
meter datagrams, discovery replies, scene show control, recorder / player
states, channel processing nodes, model-specific FX parameters, stereo
mains / buses / matrices and a stereo input.

To confirm on a real console (the probe prints each):

1. `,sfi` / `,sff` reply shapes on 3.1.2, and the `/?` reply address.
2. `$mute` values (0 live / 1 own / 2 by DCA or group is assumed; any
   non-zero is shown as muted).
3. Native: hashes learned from `,b dd` match the pushed events; hashes
   stable across reboots (`/ch/1/mute` = f50f69f8 in the doc).
4. Enum values in native events (index or text; both handled).
5. `#Mn` mute-group tags (from firmware strings, not the doc) for the
   "muted by" reason; `#Dn` is documented.
6. Meter rate, DCA meter count (8 vs 16), V2 word order, `in` = pre-fader
   and `out` = post-fader, gain-reduction scaling.
7. `/$ctl/osc/ronly` vs `/$ctl/OSC/ronly`.
8. `in/set/$g` writable (the doc lists it under the source's gain); the
   add-on refuses to write it if the console's definition says read only.
9. Stereo: `in/set/$mode` and `busmono`.
10. Talkback `$on` under PUSH / LATCH / AUTO; scene list = `$scenes`
    enum items; `GO` loads `$actionidx`.
11. Fader position below −10 dB (the widgets draw their own scale; dB
    values always come from the console).
12. CPU / temperature: not in the document, so not shown.

## Build

```bash
go run github.com/toolingdev/jadeck/sdk/addon/cmd/ptz-addon build
go vet ./... && go test -race ./...
```

This version needs the add-on SDK with widget settings, per-connection
options and resource cache control (apiVersion 2 plus "v2.1" in jadeck's
`design/WIDGETS-V2.md`). Until that SDK is tagged, build in the
`ptz-addons` workspace (its `go.work` points at the SDK source); the
release workflow needs `go.mod` bumped to the tagged SDK first.

Try it on a controller: Settings → Add-ons → Developer → Load a folder
(this folder), or install `dist/behringer-wing.ptzaddon`.

Release: push a tag `v2.0.0`; the workflow builds, signs (secret
`PTZ_SIGNING_KEY`) and attaches `behringer-wing.ptzaddon` to the release.
