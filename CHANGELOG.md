# Changelog

## Unreleased

- **Widgets use jadeck's shared frame kit** (`/api/kit/v1`, needs jadeck
  0.6 or newer): the fader strip, knob, LED meter, buttons, tabs, lock
  and header are the kit's, so they match jadeck and the other add-ons.
  `ui/knob.js` and the copies in `ui/common.js` / `ui/wing.css` are gone.
  Same look; a fader cap at +10 or −∞ no longer overlaps the name or the
  level pill.

## 2.0.0

Needs jadeck 0.5.0 or newer (widgets v2, add-on SDK v0.3.0).

- **Full WING control surface:** mutes, faders, DCAs, mute groups,
  scenes, the USB recorder and player, talkback, solo, channel processing
  (gain, filters, gate, EQ, compressor, sends, pan, inserts), FX and
  meters. Every change is read back from the console.
- **Live updates** over the native protocol (TCP 2222), learned from the
  console's own node definitions; falls back to polling over OSC every 2 s
  when the native link can't be used. Meters over UDP while a widget shows
  them.
- **Widgets**, each with its own settings: Mutes, DCA mutes, Mute groups,
  Faders, DCA faders, Channel strip, FX, Meter bridge, Main fader, Scenes,
  Scene control, USB recorder / Recorder, USB player / Player, Talkback,
  Solo, Console status, Console tools and Probe report.
- **Hold to confirm:** Record, Stop and New file on the USB recorder run
  only after a press-and-hold (3-10 s, default 5); the add-on also refuses
  them without `confirmed: true`.
- **Discovery** of consoles on the same network; a read-only connection
  option; respects the console's OSC lock.
- **Safe by default:** nothing is written when a connection opens; phantom
  power, patching, routing, locks and network settings are never changed.
- X32 / M32 and other OSC desks keep channel mutes only, as before.
- Verified against a protocol-accurate fake console; run the read-only
  probe on a real WING before relying on it in a live event.

## 1.0.1

- Renamed to jadeck (SDK v0.2.0).

## 1.0.0

- Channel mutes with read-back. X32 / M32 experimental.
