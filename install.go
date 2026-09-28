package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// root, when set, puts every install path under it and registers nothing with the OS (testing).
var root string

func rooted(p string) string {
	if root == "" {
		return p
	}
	return filepath.Join(root, strings.TrimPrefix(p, filepath.VolumeName(p)))
}

// expand turns a manifest "to" into a folder: ${VAR} from the environment, a leading ~ as home.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = home + p[1:]
	}
	return filepath.Clean(os.Expand(p, os.Getenv))
}

// dataDir holds the install state, the updater and its log. On Windows it is in Program Files:
// the updater runs elevated, so its folder must not be writable without elevation.
func dataDir() string {
	base := os.Getenv("ProgramFiles")
	if runtime.GOOS != "windows" {
		base, _ = os.UserConfigDir()
	}
	return rooted(filepath.Join(base, man.Product.Publisher, man.Product.Name))
}

// state is what the last install chose, so a silent update can repeat it.
type state struct {
	Version string            `json:"version"`
	Dirs    map[string]string `json:"dirs"` // component id -> folder
	Updater bool              `json:"updater"`
}

func loadState() (st state, err error) {
	b, err := os.ReadFile(filepath.Join(dataDir(), "install.json"))
	if err == nil {
		err = json.Unmarshal(b, &st)
	}
	return st, err
}

// initialState is the last install's choices, or the manifest's defaults on a first install.
func initialState() state {
	if st, err := loadState(); err == nil && st.Dirs != nil {
		return st
	}
	st := state{Dirs: map[string]string{}, Updater: man.option("updater").Default}
	for _, c := range man.available() {
		if c.Default {
			st.Dirs[c.ID] = expand(c.target().To)
		}
	}
	return st
}

func openPayload() *zip.Reader {
	b, err := assets.ReadFile("payload/payload.zip")
	if err != nil {
		return nil
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		log.Printf("payload: %v", err)
		return nil
	}
	return z
}

// entries are the payload files at or under from.
func entries(from string) []*zip.File {
	var out []*zip.File
	if pay == nil {
		return out
	}
	for _, f := range pay.File {
		if n := strings.TrimSuffix(f.Name, "/"); n == from || strings.HasPrefix(n, from+"/") {
			out = append(out, f)
		}
	}
	return out
}

// progress is shared between the install goroutine and the window.
type progress struct {
	mu          sync.Mutex
	done, total int64
	what        string
	err         error
	finished    bool
}

func (p *progress) set(done, total int64, what string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.done, p.total, p.what = done, total, what
	p.mu.Unlock()
}

func (p *progress) finish(err error) {
	p.mu.Lock()
	p.err, p.finished = err, true
	p.mu.Unlock()
}

func (p *progress) get() (done, total int64, what string, finished bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done, p.total, p.what, p.finished, p.err
}

// install copies each chosen component into its folder, then saves the choices and sets up the updater.
func install(st state, p *progress) error {
	if pay == nil {
		return errors.New("this build has no payload")
	}
	type job struct {
		c     *component
		t     *target
		dir   string
		files []*zip.File
	}
	var jobs []job
	var total int64
	for _, c := range man.Components {
		dir, ok := st.Dirs[c.ID]
		t := c.target()
		if !ok || t == nil {
			continue
		}
		files := entries(t.From)
		if len(files) == 0 {
			log.Printf("%s: %s is not in this installer, skipped", c.Name, t.From)
			continue
		}
		for _, f := range files {
			total += int64(f.UncompressedSize64)
		}
		jobs = append(jobs, job{c, t, rooted(dir), files})
	}

	var done int64
	for _, j := range jobs {
		what := j.c.Name + "  " + filepath.Join(j.dir, path.Base(j.t.From))
		log.Printf("installing %s", what)
		parent := j.t.From[:strings.LastIndex(j.t.From, "/")+1] // "VST3/" of "VST3/FSVR.vst3"
		for _, f := range j.files {
			rel := strings.TrimSuffix(f.Name[len(parent):], "/")
			if !filepath.IsLocal(rel) {
				return fmt.Errorf("bad path in payload: %s", f.Name)
			}
			if err := extract(f, filepath.Join(j.dir, filepath.FromSlash(rel))); err != nil {
				return err
			}
			done += int64(f.UncompressedSize64)
			p.set(done, total, j.c.Name)
		}
		installed := filepath.Join(j.dir, path.Base(j.t.From))
		if root != "" {
			continue
		}
		if j.t.Register && runtime.GOOS == "windows" {
			if err := run("regsvr32", "/s", installed); err != nil {
				return err
			}
		}
		if j.t.Shortcut != "" {
			if err := shortcut(j.t.Shortcut, installed); err != nil {
				return err
			}
		}
	}

	st.Version = man.Product.Version
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := writeFile(filepath.Join(dataDir(), "install.json"), b, 0o644); err != nil {
		return err
	}
	if err := keepSetup(); err != nil {
		return err
	}
	if err := setUpdater(st.Updater); err != nil {
		return err
	}
	if root == "" {
		if err := addUninstallEntry(setupPath(), total); err != nil {
			return err
		}
	}
	log.Printf("installed %s %s", man.Product.Name, man.Product.Version)
	return nil
}

// uninstall removes what the last install put down, then the updater, the Apps entry and the
// install folder. Only what the payload installed goes; FSVR's library and settings are the user's.
// It goes by today's manifest, so a component a later version renames would be left behind.
func uninstall(p *progress) error {
	st, err := loadState()
	if err != nil {
		return fmt.Errorf("nothing to uninstall: %w", err)
	}
	items := installedItems(st)
	var failed []error
	for i, it := range items {
		p.set(int64(i), int64(len(items)), it.c.Name)
		log.Printf("removing %s", it.path)
		if root == "" {
			if it.t.Register && runtime.GOOS == "windows" {
				if err := run("regsvr32", "/u", "/s", it.path); err != nil {
					log.Print(err) // already gone, which is fine
				}
			}
			if it.t.Shortcut != "" {
				os.Remove(shortcutFile(it.t.Shortcut))
			}
		}
		if err := os.RemoveAll(it.path); err != nil {
			failed = append(failed, err)
			continue
		}
		os.Remove(it.path + ".old")
	}
	if len(failed) > 0 { // keep the record so uninstalling again can finish the job
		return fmt.Errorf("close anything using %s, then uninstall again. %w", man.Product.Name, errors.Join(failed...))
	}

	setUpdater(false)
	if root == "" {
		removeUninstallEntry()
	}
	dir := dataDir()
	moveAside(dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	os.Remove(filepath.Dir(dir)) // the publisher folder, when nothing else is in it
	p.set(1, 1, "")
	log.Printf("uninstalled %s", man.Product.Name)
	return nil
}

type installedItem struct {
	c    *component
	t    *target
	path string
}

// installedItems is what the last install put down: each chosen component's file or bundle.
func installedItems(st state) []installedItem {
	var out []installedItem
	for _, c := range man.Components {
		dir, ok := st.Dirs[c.ID]
		t := c.target()
		if !ok || t == nil || !filepath.IsAbs(dir) {
			continue
		}
		if name := path.Base(t.From); name != "." && name != "/" { // never the folder itself
			out = append(out, installedItem{c, t, filepath.Join(rooted(dir), name)})
		}
	}
	return out
}

// moveAside moves this program out of dir when it runs from there. Windows won't delete a running
// exe but will move one, and deletes it from temp at the next restart.
func moveAside(dir string) {
	self, err := os.Executable()
	if err != nil || runtime.GOOS != "windows" || !strings.HasPrefix(strings.ToLower(self), strings.ToLower(dir+string(filepath.Separator))) {
		return
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("%s-uninstall-%d.exe", man.Product.Name, os.Getpid()))
	if os.Rename(self, tmp) == nil {
		deleteOnReboot(tmp)
	}
}

func extract(f *zip.File, dst string) error {
	if f.Mode().IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	if f.Mode()&fs.ModeSymlink != 0 { // macOS bundles keep their symlinks
		link, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		os.Remove(dst)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(string(link), dst)
	}
	perm := f.Mode().Perm()
	if perm == 0 { // zips made on Windows carry no Unix mode
		perm = 0o644
	}
	return writeFrom(dst, r, perm)
}

// writeFrom writes dst beside itself, then swaps it in. A running app or a plug-in a host has
// loaded keeps its old file: on Windows it is renamed to .old, which is allowed while in use.
func writeFrom(dst string, r io.Reader, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil && os.Rename(tmp, dst) != nil {
		os.Remove(dst + ".old")
		os.Rename(dst, dst+".old")
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func writeFile(dst string, b []byte, perm fs.FileMode) error {
	return writeFrom(dst, bytes.NewReader(b), perm)
}

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	hide(c)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v %s", name, err, bytes.TrimSpace(out))
	}
	return nil
}

// shortcutFile is an installed app's menu entry: in the Start menu (Windows) or the applications
// menu (Linux). macOS needs none, the .app in /Applications is the shortcut.
func shortcutFile(name string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`, name+".lnk")
	case "linux":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "applications", man.Product.ID+".desktop")
	}
	return ""
}

func shortcut(name, target string) error {
	switch file := shortcutFile(name); runtime.GOOS {
	case "windows":
		q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		return run("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"$s = (New-Object -ComObject WScript.Shell).CreateShortcut("+q(file)+"); $s.TargetPath = "+q(target)+"; $s.Save()")
	case "linux":
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=\"%s\"\nTerminal=false\n", name, target)
		return writeFile(file, []byte(entry), 0o644)
	}
	return nil
}

// setupPath is the copy of the setup kept in the install folder, which the updater and uninstall run.
func setupPath() string {
	p := filepath.Join(dataDir(), man.Product.Name+"-Setup")
	if runtime.GOOS == "windows" {
		p += ".exe"
	}
	return p
}

func keepSetup() error {
	self, err := os.Executable()
	if err != nil || filepath.Clean(self) == filepath.Clean(setupPath()) {
		return err
	}
	src, err := os.Open(self)
	if err != nil {
		return err
	}
	defer src.Close()
	return writeFrom(setupPath(), src, 0o755)
}

// setUpdater has the OS run the kept setup with -update at each login: an elevated scheduled task
// on Windows, a LaunchAgent on macOS, an autostart entry on Linux.
// Checks at login only (plus daily on macOS); a machine that never logs out waits.
func setUpdater(on bool) error {
	exe := setupPath()
	task := man.Product.ID + ".updater"
	home, _ := os.UserHomeDir()
	config, _ := os.UserConfigDir()
	var file string
	switch runtime.GOOS {
	case "darwin":
		file = rooted(filepath.Join(home, "Library", "LaunchAgents", task+".plist"))
	case "linux":
		file = rooted(filepath.Join(config, "autostart", task+".desktop"))
	}

	if !on {
		if runtime.GOOS == "windows" && root == "" {
			run("schtasks", "/Delete", "/F", "/TN", task) // fails when there is none, which is fine
		}
		os.Remove(file)
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		if root != "" {
			return nil
		}
		return run("schtasks", "/Create", "/F", "/TN", task, "/SC", "ONLOGON", "/RL", "HIGHEST", "/TR", `"`+exe+`" -update`)
	case "darwin":
		// AbandonProcessGroup: the updater exits while the setup it started keeps running.
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>-update</string></array>
  <key>RunAtLoad</key><true/>
  <key>StartInterval</key><integer>86400</integer>
  <key>AbandonProcessGroup</key><true/>
</dict></plist>
`, task, html.EscapeString(exe))
		return writeFile(file, []byte(plist), 0o644)
	default:
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s updater\nExec=\"%s\" -update\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n", man.Product.Name, exe)
		return writeFile(file, []byte(entry), 0o644)
	}
}

// checkUpdate reads the product's update manifest on musica.studio and, when it names a newer
// version, downloads this OS's setup from beside it and starts it with -silent. GitHub is never
// asked: a release there reaches users only once musica.studio's mirror has taken it.
//
// The manifest (updates url) is {"version": "0.5.0", "sha256": {"FSVR-Windows-Installer.exe": "…"}},
// and the file is <folder of the manifest>/<version>/<asset name>. A file with no hash is refused.
func checkUpdate() error {
	st, err := loadState()
	if err != nil {
		return fmt.Errorf("nothing installed to update: %w", err)
	}
	want := man.asset()
	dst := filepath.Join(dataDir(), "download-"+want)
	os.Remove(dst) // the last update's setup, done by now

	client := &http.Client{Timeout: 5 * time.Minute}
	res, err := get(client, man.Updates.URL)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var latest struct {
		Version string            `json:"version"`
		SHA256  map[string]string `json:"sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&latest); err != nil {
		return err
	}
	if !newer(latest.Version, st.Version) {
		log.Printf("%s is current (latest %s)", st.Version, latest.Version)
		return nil
	}
	sum := latest.SHA256[want]
	if !plainVersion.MatchString(latest.Version) || len(sum) != 64 {
		return fmt.Errorf("update %q has no hash for %s", latest.Version, want)
	}
	u, err := url.Parse(man.Updates.URL)
	if err != nil {
		return err
	}
	u = u.ResolveReference(&url.URL{Path: latest.Version + "/" + want})
	if err := download(client, u.String(), sum, dst); err != nil {
		return err
	}
	log.Printf("updating %s to %s", st.Version, latest.Version)
	c := exec.Command(dst, "-silent")
	hide(c)
	return c.Start() // no Wait: the setup replaces this updater's own file
}

// plainVersion keeps the manifest's version to a folder name: no "..", no slashes.
var plainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func get(c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", man.Product.Name+"-updater")
	res, err := c.Do(req)
	if err == nil && res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	return res, err
}

// download saves url to dst when its sha256 is sum, and removes it otherwise.
func download(c *http.Client, url, sum, dst string) error {
	res, err := get(c, url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	h := sha256.New()
	if err := writeFrom(dst, io.TeeReader(res.Body, h), 0o755); err != nil {
		return err
	}
	if !strings.EqualFold(sum, hex.EncodeToString(h.Sum(nil))) {
		os.Remove(dst)
		return errors.New("download does not match the update's sha256")
	}
	return nil
}

// newer reports whether version a is after b, comparing major.minor.patch as numbers.
func newer(a, b string) bool {
	x, y := semver(a), semver(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func semver(s string) (v [3]int) {
	for i, part := range strings.SplitN(strings.TrimPrefix(s, "v"), ".", 3) {
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			v[i] = v[i]*10 + int(r-'0')
		}
	}
	return v
}

// pack zips goos's targets out of a build folder (FSVR's bin/) into the payload layout.
func pack(goos, from, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	for _, c := range man.Components {
		for _, t := range c.Targets {
			if t.OS != goos {
				continue
			}
			src := filepath.Join(from, filepath.FromSlash(t.From))
			if _, err := os.Lstat(src); err != nil {
				log.Printf("pack: no %s, skipped", t.From)
				continue
			}
			err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				h, err := zip.FileInfoHeader(info)
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(from, p)
				h.Name, h.Method = filepath.ToSlash(rel), zip.Deflate
				w, err := z.CreateHeader(h)
				if err != nil {
					return err
				}
				if info.Mode()&fs.ModeSymlink != 0 {
					link, err := os.Readlink(p)
					if err == nil {
						_, err = w.Write([]byte(link))
					}
					return err
				}
				r, err := os.Open(p)
				if err != nil {
					return err
				}
				defer r.Close()
				_, err = io.Copy(w, r)
				return err
			})
			if err != nil {
				return err
			}
		}
	}
	if err := z.Close(); err != nil {
		return err
	}
	return f.Close()
}
