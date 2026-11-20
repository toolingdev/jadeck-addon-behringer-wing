# Behringer WING — PTZ controller add-on

Mute and unmute WING channels, every change read back. X32/M32 experimental.

Install: it ships with the controller (Settings → Add-ons); newer releases of this repository arrive as updates there.

Build every platform and the `.ptzaddon` package:

```bash
go run github.com/toolingdev/ptz-controller/sdk/addon/cmd/ptz-addon build
```

Try it on a controller: Settings → Add-ons → Developer → Load a folder (this folder),
or install `dist/behringer-wing.ptzaddon` with Install from file.

Release: push a tag `v1.2.3`; the workflow builds, signs (secret `PTZ_SIGNING_KEY`)
and attaches `behringer-wing.ptzaddon` to the GitHub release.
