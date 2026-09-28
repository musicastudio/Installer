package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.4.9", "0.4.8", true}, {"v0.4.8", "0.4.8", false}, {"v0.4.10", "0.4.9", true},
		{"v0.5.0", "0.4.12", true}, {"v0.4.7", "0.4.8", false}, {"v1.0.0-rc1", "0.9.9", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// TestInstall runs a small payload through install under a temp root, twice, as an update would.
func TestInstall(t *testing.T) {
	man = loadManifest()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"VST3/FSVR.vst3/Contents/bin/FSVR", "VST3/FS1R.emu.vst3/stale", "CLAP/FSVR.clap"} {
		w, _ := z.Create(name)
		w.Write([]byte(name))
	}
	z.Close()
	pay, _ = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	root = t.TempDir()
	defer func() { root, pay = "", nil }()

	if n := len(man.available()); n != 2 {
		t.Fatalf("available: %d components, want vst3 and clap", n)
	}
	vst3 := filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "plugins", "vst3") // a folder at the top of the drive, which root moves under the temp dir
	st := state{Dirs: map[string]string{"vst3": vst3}}
	for range 2 {
		if err := install(st, nil); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, "plugins", "vst3")
	if b, err := os.ReadFile(filepath.Join(dir, "FSVR.vst3", "Contents", "bin", "FSVR")); err != nil || string(b) != "VST3/FSVR.vst3/Contents/bin/FSVR" {
		t.Fatalf("installed file: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "FS1R.emu.vst3")); err == nil {
		t.Error("installed a folder outside the target's from")
	}
	var saved state
	b, _ := os.ReadFile(filepath.Join(dataDir(), "install.json"))
	if json.Unmarshal(b, &saved); saved.Version != man.Product.Version || saved.Dirs["vst3"] != vst3 {
		t.Errorf("saved state: %s", b)
	}

	// Uninstall takes FSVR's bundle and the install folder, and nothing else in the plug-in folder.
	other := filepath.Join(dir, "Other.vst3")
	os.WriteFile(other, []byte("someone else's"), 0o644)
	if err := uninstall(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "FSVR.vst3")); err == nil {
		t.Error("FSVR.vst3 is still there")
	}
	if _, err := os.Stat(dataDir()); err == nil {
		t.Error("the install folder is still there")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("uninstall took another plug-in: %v", err)
	}
}

// TestDownload checks the updater's file fetch refuses a file whose sha256 is not the manifest's.
func TestDownload(t *testing.T) {
	man = loadManifest()
	body := []byte("setup")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	sum := sha256.Sum256(body)
	dst := filepath.Join(t.TempDir(), "setup")
	if err := download(srv.Client(), srv.URL, hex.EncodeToString(sum[:]), dst); err != nil {
		t.Fatal(err)
	}
	if err := download(srv.Client(), srv.URL, hex.EncodeToString(make([]byte, 32)), dst); err == nil {
		t.Error("a file with the wrong hash was kept")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("the mismatched download is still there")
	}
	for _, v := range []string{"0.5.0", "../0.5.0", "0.5", "0.5.0/x"} {
		if plainVersion.MatchString(v) != (v == "0.5.0") {
			t.Errorf("plainVersion(%q)", v)
		}
	}
}
