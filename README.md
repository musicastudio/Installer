# Installer

The musica.studio installer: one Go program that draws its own window (Ebitengine) and installs a product's plug-in formats on Windows, macOS and Linux, keeps a copy of itself to uninstall and update with, and lists the product in Settings > Apps on Windows. Its product today is [FSVR](https://github.com/musicastudio/FSVR), whose CI builds the installers from this repository on every push.

`product/installer.xml` is the whole product: its name and version, the steps the window walks through, each format's files and where they go per OS, and the update feed. `payload/payload.zip` carries the files and is packed at build time from a folder in the layout of FSVR's `bin/`.

## Building

Needs Go (the version in `go.mod`). Windows and macOS cross-compile from any host; Linux needs cgo and the X11 and GL headers, so it builds on Linux, or in Docker from anywhere else.

```bash
./build.sh
```

Beside a local FSVR build (`../FSVR/bin`) that makes `dist/FSVR-Windows-Installer.exe`. `BIN_MACOS` and `BIN_LINUX` name folders for the other two, and `VERSION` overrides the product version; `build.sh` says the rest. `go test .` runs an install and uninstall under a temporary root, and checks that the updater refuses a download whose hash does not match.

`-root <folder>` installs under that folder instead of the real locations and registers nothing, for trying a build without touching the machine.

## Updates

The updater never asks GitHub. It reads `https://musica.studio/updates/<product>/latest.json`, `{"version": "0.5.0", "sha256": {"<setup>": "<hash>"}}`, downloads the setup for its OS from `<version>/<setup>` beside it, and refuses it unless its SHA-256 is the one the manifest names. The server fills that folder from the product's GitHub releases, checking each file against the digest GitHub records, so a release reaches users only once musica.studio has taken it, and a review step can sit in between.

## Font

The window draws in Analog Whispers, a licensed font that stays out of this repository. `build.sh` cuts it to ASCII and scrambles it into `payload/font.bin` when `Analog Whispers.ttf` is present; without it the installer draws in Go Mono.

## Licence

GPLv3 ([LICENSE](LICENSE)). `product/LICENSE` is the product's own licence, shown on the licence step.
