#!/usr/bin/env bash
# Builds the installer into dist/: Windows, macOS (a universal .app, plus the bare binary the
# updater downloads) and Linux. Needs Go. Windows and macOS are pure Go and cross-compile from any
# host; Linux needs cgo and X11 headers, so it builds on Linux, or in Docker from anywhere else.
#
# Outputs, named as FM8.plus names its release assets: dist/FSVR-Windows-Installer.exe,
# dist/FSVR-MacOS-Installer.app, dist/FSVR-MacOS-Update (the bare binary the updater runs) and
# dist/FSVR-Linux-Installer. The updater's names are the <asset>s in product/installer.xml.
#
# Payloads are packed from a folder in the layout of FSVR's bin/, one per OS: $BIN_WINDOWS (default
# ../FSVR/bin, a local build), $BIN_MACOS and $BIN_LINUX. An OS with no folder is skipped, so a
# local run builds Windows alone; FSVR's CI unzips its plug-in artifacts and passes all three.
# $VERSION, when set, replaces the product version in installer.xml first (FSVR's CI passes its own).
#
# Font: the licensed "Analog Whispers.ttf" (kept out of the repo) is cut to ASCII with fontTools,
# in a venv under .cache, and scrambled into payload/font.bin. Without it the installer uses the
# payload/font.bin already there (CI writes one from a secret), and without that, Go Mono.
set -euo pipefail
cd "$(dirname "$0")"

xml=product/installer.xml
if [ -n "${VERSION:-}" ]; then
  sed -i "s/\(<product [^>]*version=\"\)[^\"]*/\1$VERSION/" "$xml"
fi
attr() { sed -n "s/.*<$1 [^>]*$2=\"\([^\"]*\)\".*/\1/p" "$xml" | head -1; }
name=$(attr product name)
version=$(attr product version)
BIN_WINDOWS=${BIN_WINDOWS:-../FSVR/bin}
out=dist
cache=.cache # the font venv and the patched Ebitengine, kept between builds
rm -rf "$out" && mkdir -p "$out" "$cache"
trap 'rm -f payload/payload.zip rsrc_windows_*.syso' EXIT
echo "$name $version"

font="Analog Whispers.ttf"
if [ -f "$font" ]; then
  echo "font: ASCII subset, scrambled"
  venv="$cache/venv"
  [ -d "$venv" ] || "$(command -v python3 || command -v python)" -m venv "$venv"
  pyv="$venv/bin/python"
  [ -x "$pyv" ] || pyv="$venv/Scripts/python.exe" # Windows venvs
  "$pyv" -c "import fontTools" 2>/dev/null || "$pyv" -m pip install -q --disable-pip-version-check fonttools
  "$pyv" -m fontTools.subset "$font" --unicodes=U+0020-007E --no-hinting --output-file="$cache/font.ttf"
  go run . -seal "$cache/font.ttf" payload/font.bin
  rm "$cache/font.ttf"
elif [ -f payload/font.bin ]; then
  echo "font: the payload/font.bin already there"
else
  echo "font: none, so the installer draws in Go Mono"
fi

go test .

stage() { go run . -pack "$1" "$2" payload/payload.zip; } # stage <goos> <bin folder>

if [ -d "$BIN_WINDOWS" ]; then
  echo "windows/amd64"
  stage windows "$BIN_WINDOWS"
  go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --manifest gui --admin --icon product/icon.png \
    --product-name "$name" --product-version "$version" --file-version "$version" --file-description "$name Setup"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o "$out/$name-Windows-Installer.exe" .
  rm rsrc_windows_*.syso
fi

if [ -d "${BIN_MACOS:-}" ]; then
  echo "darwin/amd64 + darwin/arm64"
  # Without Metal (a VM, a Hackintosh) Ebitengine asks macOS for a GPU-accelerated OpenGL renderer and
  # gets none. patches/ebiten-software-gl.patch lets it take Apple's software renderer instead; the Mac
  # builds use a patched copy of the module through a go.mod of their own.
  eb=$(go list -m -f '{{.Dir}}' github.com/hajimehoshi/ebiten/v2)
  command -v cygpath >/dev/null && eb=$(cygpath -u "$eb")
  patched="$cache/ebiten-$(go list -m -f '{{.Version}}' github.com/hajimehoshi/ebiten/v2)"
  if [ ! -d "$patched" ]; then
    rm -rf "$patched.tmp" && cp -r "$eb" "$patched.tmp" && chmod -R u+w "$patched.tmp"
    patch -d "$patched.tmp" -p1 --quiet <patches/ebiten-software-gl.patch
    mv "$patched.tmp" "$patched"
  fi
  mkdir -p "$cache/mac" && cp go.sum "$cache/mac/go.sum"
  { cat go.mod; echo "replace github.com/hajimehoshi/ebiten/v2 => \"$(pwd -W 2>/dev/null || pwd)/$patched\""; } >"$cache/mac/go.mod"
  stage darwin "$BIN_MACOS"
  for arch in amd64 arm64; do
    GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -modfile "$cache/mac/go.mod" -trimpath -ldflags "-s -w" -o "$cache/mac-$arch" .
  done
  if command -v lipo >/dev/null; then
    lipo -create -output "$out/$name-MacOS-Update" "$cache/mac-amd64" "$cache/mac-arm64"
  else
    go run github.com/randall77/makefat@v0.0.0-20260406194835-1b91746796b7 "$out/$name-MacOS-Update" "$cache/mac-amd64" "$cache/mac-arm64"
  fi
  rm "$cache"/mac-*
  app="$out/$name-MacOS-Installer.app/Contents"
  mkdir -p "$app/MacOS"
  cp "$out/$name-MacOS-Update" "$app/MacOS/$name-Setup"
  cat >"$app/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$name-Setup</string>
  <key>CFBundleIdentifier</key><string>$(attr product id).setup</string>
  <key>CFBundleName</key><string>$name Setup</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict></plist>
EOF
fi

if [ -d "${BIN_LINUX:-}" ]; then
  echo "linux/amd64"
  stage linux "$BIN_LINUX"
  linux="go build -trimpath -ldflags '-s -w' -o $out/$name-Linux-Installer ."
  if [ "$(uname -s)" = Linux ]; then
    CGO_ENABLED=1 sh -c "$linux"
  elif command -v docker >/dev/null; then
    src=$(pwd -W 2>/dev/null || pwd) # Git Bash on Windows needs the Windows path
    MSYS_NO_PATHCONV=1 docker run --rm -v "$src":/src -w /src -e CGO_ENABLED=1 -e DEBIAN_FRONTEND=noninteractive golang:1 sh -c \
      "apt-get update -qq && apt-get install -y -qq libgl1-mesa-dev xorg-dev >/dev/null && $linux"
  else
    echo "skipped: needs a Linux host or Docker"
  fi
fi

ls -l "$out"
