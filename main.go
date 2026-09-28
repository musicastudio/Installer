// Musica Studio installer engine. product/installer.xml describes the product, its steps and what
// goes where; payload/payload.zip carries the files, staged per OS by build.sh.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"embed"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// payload/ also holds font.bin, the licensed font subset to ASCII and scrambled by build.sh.
//
//go:embed product icons/*.png payload
var assets embed.FS

type manifest struct {
	Product struct {
		Name      string `xml:"name,attr"`
		Version   string `xml:"version,attr"`
		Publisher string `xml:"publisher,attr"`
		ID        string `xml:"id,attr"`
		Map       string `xml:"map,attr"`
		Icon      string `xml:"icon,attr"`
	} `xml:"product"`
	Updates struct {
		URL    string `xml:"url,attr"` // the update manifest, see checkUpdate
		Assets []struct {
			OS   string `xml:"os,attr"`
			Name string `xml:",chardata"`
		} `xml:"asset"`
	} `xml:"updates"`
	Steps     []step `xml:"step"`
	Uninstall struct {
		Steps []step `xml:"step"`
	} `xml:"uninstall"`
	Components []*component `xml:"component"`
	Options    []option     `xml:"option"`
}

type step struct {
	Type   string `xml:"type,attr"`
	Title  string `xml:"title,attr"`
	Button string `xml:"button,attr"`
	File   string `xml:"file,attr"`
	Box    bool   `xml:"box,attr"` // show the box (finish)
	Text   string `xml:",chardata"`
}

type component struct {
	ID      string   `xml:"id,attr"`
	Name    string   `xml:"name,attr"`
	Icon    string   `xml:"icon,attr"`
	About   string   `xml:"about,attr"`
	Default bool     `xml:"default,attr"`
	Targets []target `xml:"target"`
}

type target struct {
	OS       string `xml:"os,attr"`
	From     string `xml:"from,attr"`
	To       string `xml:"to,attr"`
	Register bool   `xml:"register,attr"`
	Shortcut string `xml:"shortcut,attr"`
}

type option struct {
	ID      string `xml:"id,attr"`
	Name    string `xml:"name,attr"`
	Icon    string `xml:"icon,attr"`
	Note    string `xml:"note,attr"`
	Default bool   `xml:"default,attr"`
	Text    string `xml:",chardata"`
}

var (
	man *manifest
	pay *zip.Reader // nil in a build with no payload staged
)

func loadManifest() *manifest {
	b, err := assets.ReadFile("product/installer.xml")
	if err != nil {
		log.Fatal(err)
	}
	m := &manifest{}
	if err := xml.Unmarshal(b, m); err != nil {
		log.Fatal(err)
	}
	p := m.Product
	if p.Name == "" || p.Version == "" || p.Publisher == "" || p.ID == "" {
		log.Fatal("installer.xml: product needs a name, version, publisher and id") // uninstall deletes the folder they name
	}
	for _, steps := range [][]step{m.Steps, m.Uninstall.Steps} {
		for i := range steps {
			steps[i].Text = strings.Join(strings.Fields(steps[i].Text), " ")
		}
	}
	for i := range m.Options {
		m.Options[i].Text = strings.Join(strings.Fields(m.Options[i].Text), " ")
	}
	return m
}

// scramble hides the embedded font from anyone scanning the binary for a TTF, and unhides it again.
// Obfuscation only, since the key is derived right here.
func scramble(b []byte) []byte {
	out := make([]byte, len(b))
	rand.NewChaCha8(sha256.Sum256([]byte("musica.studio installer font"))).Read(out)
	for i := range out {
		out[i] ^= b[i]
	}
	return out
}

// target is where this component goes on this OS, or nil.
func (c *component) target() *target {
	for i := range c.Targets {
		if c.Targets[i].OS == runtime.GOOS {
			return &c.Targets[i]
		}
	}
	return nil
}

// available is what this OS can install: a target here, and in the payload when there is one.
func (m *manifest) available() []*component {
	var out []*component
	for _, c := range m.Components {
		if t := c.target(); t != nil && (pay == nil || len(entries(t.From)) > 0) {
			out = append(out, c)
		}
	}
	return out
}

func (m *manifest) option(id string) option {
	for _, o := range m.Options {
		if o.ID == id {
			return o
		}
	}
	return option{}
}

// asset is this OS's setup, the file the updater downloads.
func (m *manifest) asset() string {
	for _, a := range m.Updates.Assets {
		if a.OS == runtime.GOOS {
			return strings.TrimSpace(a.Name)
		}
	}
	return ""
}

func main() {
	flag.StringVar(&root, "root", "", "install under this folder instead of the real locations and register nothing (testing)")
	silent := flag.Bool("silent", false, "install with no window, reusing the last install's choices")
	update := flag.Bool("update", false, "install the version musica.studio offers, silently, if it is newer")
	remove := flag.Bool("uninstall", false, "remove what the last install put down (with -silent, no window)")
	packOS := flag.String("pack", "", "build step: -pack <goos> <build folder> <out.zip> zips that OS's targets")
	seal := flag.Bool("seal", false, "build step: -seal <font.ttf> <out> scrambles the font for embedding")
	flag.Parse()

	man = loadManifest()
	pay = openPayload()

	var err error
	switch {
	case *packOS != "":
		err = pack(*packOS, flag.Arg(0), flag.Arg(1))
	case *seal:
		var b []byte
		if b, err = os.ReadFile(flag.Arg(0)); err == nil {
			err = os.WriteFile(flag.Arg(1), scramble(b), 0o644)
		}
	case *remove && *silent:
		err = uninstall(nil)
	case *update:
		logToFile()
		err = checkUpdate()
	case *silent:
		logToFile()
		err = install(initialState(), nil)
	default:
		if err = runUI(*remove); err != nil { // no window at all, say a machine without graphics acceleration
			flag := "-silent"
			if *remove {
				flag = "-uninstall -silent"
			}
			self, _ := os.Executable()
			alert(man.Product.Name+" Setup", fmt.Sprintf("The setup could not open its window (%v).\n\nIt can run without one from a terminal:\n\"%s\" %s", err, self, flag))
		}
	}
	if err != nil {
		log.Fatal(err)
	}
}

// logToFile sends the log next to the install state, since silent runs have nowhere to show it.
func logToFile() {
	dir := dataDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	// grows a line or two per login; rotate it if that ever matters
	if f, err := os.OpenFile(filepath.Join(dir, "update.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
	}
}
