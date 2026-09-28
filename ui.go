package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/colorm"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/gomonobold"
)

// Layout in design pixels; Draw multiplies by the monitor's scale factor.
const (
	winW, winH             = 640, 480
	frame                  = 12      // grey border
	left, right            = 24, 616 // content edges
	headerH                = 50      // the window drags by this strip
	boxX, boxY             = 320, 196
	shadowY                = 354
	layerW, layerH         = 260, 300 // area the box is drawn into
	btnX, btnY, btnW, btnH = 222, 396, 196, 45
	titleY, introY, bodyY  = 62, 104, 148
)

type rect struct{ x, y, w, h float64 }

func (r rect) has(x, y float64) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

var (
	closeBox = rect{596, 14, 32, 32}
	btnBox   = rect{btnX, btnY, btnW, btnH}
	backBox  = rect{left, btnY, 80, btnH}
	licBox   = rect{left, bodyY, right - left, 228}
	optBox   = rect{left, bodyY, right - left, 88}
)

var (
	white = color.RGBA{0xff, 0xff, 0xff, 0xff}
	black = color.RGBA{0x00, 0x00, 0x00, 0xff}
	pale  = color.RGBA{0xe8, 0xe8, 0xe8, 0xff} // frame, buttons, selected tiles
	hover = color.RGBA{0xd8, 0xd8, 0xd8, 0xff}
	faint = color.RGBA{0xf4, 0xf4, 0xf4, 0xff} // hover on white
	edge  = color.RGBA{0xc8, 0xc8, 0xc8, 0xff} // outline of the borderless window
	mid   = color.RGBA{0xb4, 0xb4, 0xb4, 0xff} // unselected, disabled
	grey  = color.RGBA{0x78, 0x74, 0x78, 0xff} // "studio", body text, the shadow
)

// In seconds.
const (
	spinTime = 1.4  // install. spins the box up and fades it to white
	restTime = 0.6  // then the rest of the window fades
	fadeTime = 0.25 // steps cross-fade through white
	minWork  = 1.2  // the progress bar shows at least this long
)

// The box is a PC big box, 19 x 24 x 5 units. The map wraps it unfolded as a cross at 40 px per
// unit, so a new product only needs a new map:
//
//	      [ top  ]
//	[L ][ front ][R ][ back ]
//	      [bottom]
const (
	bw, bh, bd = 19.0, 24.0, 5.0
	hx, hy, hz = bw / 2, bh / 2, bd / 2
	ppu        = 40.0
	sub        = 6 // grid per side, keeps the affine texturing close to perspective-correct

	camZ  = 80.0  // camera distance
	focal = 700.0 // px, puts the box about 215 px tall
	tilt  = 0.2   // rad, camera looks slightly down so the top shows
)

type vec3 struct{ x, y, z float64 }

func (a vec3) add(b vec3) vec3    { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) mul(k float64) vec3 { return vec3{a.x * k, a.y * k, a.z * k} }
func (a vec3) dot(b vec3) float64 { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec3) norm() float64      { return math.Sqrt(a.dot(a)) }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}

// A side is its top-left corner and right/down edges as seen from outside, plus where its
// top-left sits in the map, in units.
type side struct {
	o, u, v vec3
	mx, my  float64
}

var sides = [6]side{
	{vec3{-hx, hy, hz}, vec3{bw, 0, 0}, vec3{0, -bh, 0}, bd, bd},         // front
	{vec3{hx, hy, hz}, vec3{0, 0, -bd}, vec3{0, -bh, 0}, bd + bw, bd},    // right
	{vec3{hx, hy, -hz}, vec3{-bw, 0, 0}, vec3{0, -bh, 0}, 2*bd + bw, bd}, // back
	{vec3{-hx, hy, -hz}, vec3{0, 0, bd}, vec3{0, -bh, 0}, 0, bd},         // left
	{vec3{-hx, hy, -hz}, vec3{bw, 0, 0}, vec3{0, 0, bd}, bd, 0},          // top
	{vec3{-hx, -hy, hz}, vec3{bw, 0, 0}, vec3{0, 0, -bd}, bd, bd + bh},   // bottom
}

var lightDir = vec3{-0.4, 0.6, 1}.mul(1 / vec3{-0.4, 0.6, 1}.norm())

// item is something clickable on the current step.
type item struct {
	r   rect
	act func()
}

type game struct {
	s                  float64 // monitor scale factor
	font, mono         *text.GoTextFaceSource
	small, body, big   text.Face // 16, 24 and 32 px
	box, shadow, layer *ebiten.Image
	images             map[string]*ebiten.Image
	vs                 []ebiten.Vertex
	is                 []uint16

	last      time.Time
	t, yaw    float64
	light     bool    // software rendering: 30 fps and no supersampling
	probed    bool    // light has been decided
	spinAt    float64 // when install. was clicked on the box, -1 when not spinning
	step, to  int     // current step, and the one being faded to
	stepAt    float64 // when the current step was entered
	dim, hdim float64 // content and header faded to white, 0..1
	mx, my    float64 // cursor, design pixels
	focus     int     // keyboard focus in items(), -1 for none
	drag      bool
	dragX     float64
	dragY     float64
	quit      bool

	steps       []step // the flow on screen: install, or uninstall
	swap        []step // the other flow, switched to once the fade to white completes
	removing    bool   // steps is the uninstall flow
	fromInstall bool   // uninstall was opened from the box, so back. returns there

	comps            []*component
	sel              map[string]bool
	dirs             map[string]string
	updater          bool
	installed        []installedItem // what the last install put down
	installedVersion string
	picking          bool
	picked           chan [2]string // folder picker result: component id, folder
	lic              []string       // licence, wrapped at the current scale
	scroll           int
	prog             progress
	bar              float64 // progress bar as drawn, eased toward the real value
}

func runUI(remove bool) error {
	mono, err := text.NewGoTextFaceSource(bytes.NewReader(gomonobold.TTF))
	if err != nil {
		return err
	}
	var font *text.GoTextFaceSource // nil in a build without the licensed font: all Go Mono then
	if b, err := assets.ReadFile("payload/font.bin"); err == nil {
		if font, err = text.NewGoTextFaceSource(bytes.NewReader(scramble(b))); err != nil {
			return err
		}
	}
	g := &game{
		font:     font,
		mono:     mono,
		steps:    man.Steps,
		removing: remove,
		shadow:   newShadow(),
		images:   map[string]*ebiten.Image{},
		last:     time.Now(),
		yaw:      0.5,
		spinAt:   -1,
		focus:    -1,
		comps:    man.available(),
		sel:      map[string]bool{},
		dirs:     map[string]string{},
		picked:   make(chan [2]string, 1),
	}
	g.box = g.image(man.Product.Map)
	if remove {
		g.steps = man.Uninstall.Steps
	}
	if last, err := loadState(); err == nil {
		g.installed, g.installedVersion = installedItems(last), last.Version
	}
	st := initialState()
	g.updater = st.Updater
	for _, c := range g.comps {
		g.dirs[c.ID] = expand(c.target().To)
		if d, ok := st.Dirs[c.ID]; ok {
			g.sel[c.ID], g.dirs[c.ID] = true, d
		}
	}

	ebiten.SetWindowTitle(man.Product.Name + " v" + man.Product.Version)
	ebiten.SetWindowSize(winW, winH)
	ebiten.SetWindowDecorated(false)
	ebiten.SetWindowIcon([]image.Image{decode(man.Product.Icon)})
	ebiten.SetTPS(ebiten.SyncWithFPS)
	ebiten.SetScreenClearedEveryFrame(false) // Draw paints every pixel itself
	return ebiten.RunGame(g)
}

func mustRead(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

func decode(name string) image.Image {
	img, _, err := image.Decode(bytes.NewReader(mustRead(name)))
	if err != nil {
		panic(err)
	}
	return img
}

func (g *game) image(name string) *ebiten.Image {
	if g.images[name] == nil {
		g.images[name] = ebiten.NewImageFromImage(decode(name))
	}
	return g.images[name]
}

func clamp01(x float64) float64 { return max(0, min(1, x)) }

func smooth(x float64) float64 { return x * x * (3 - 2*x) }

// toWhite fades c toward the white background.
func toWhite(c color.RGBA, f float64) color.RGBA {
	l := func(v uint8) uint8 { return uint8(float64(v) + (255-float64(v))*f + 0.5) }
	return color.RGBA{l(c.R), l(c.G), l(c.B), 0xff}
}

func (g *game) cur() step { return g.steps[g.step] }

func (g *game) spin() float64 {
	if g.spinAt < 0 {
		return 0
	}
	return clamp01((g.t - g.spinAt) / spinTime)
}

func (g *game) busy() bool { return g.spinAt >= 0 || g.to != g.step || g.swap != nil || g.drag }

func (g *game) hot(r rect) bool { return !g.busy() && r.has(g.mx, g.my) }

func (g *game) chosen() []*component {
	var out []*component
	for _, c := range g.comps {
		if g.sel[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

func (g *game) failed() error {
	_, _, _, _, err := g.prog.get()
	return err
}

// back is the link at the bottom left: back. to the step before, or on the box, uninstall. when
// there is an install to remove. An empty label means none.
func (g *game) back() (string, func()) {
	t := g.cur().Type
	switch {
	case t == "progress" || t == "finish":
	case g.step > 0:
		return "back.", func() { g.to = g.step - 1 }
	case g.removing && g.fromInstall:
		return "back.", func() { g.swap = man.Steps }
	case t == "box" && !g.removing && len(g.installed) > 0 && len(man.Uninstall.Steps) > 0:
		return "uninstall.", func() { g.swap, g.fromInstall = man.Uninstall.Steps, true }
	}
	return "", nil
}

func (g *game) primaryOK() bool {
	switch g.cur().Type {
	case "progress":
		return false
	case "components":
		return len(g.chosen()) > 0
	case "box":
		if g.removing && len(g.installed) == 0 {
			return false // nothing to uninstall
		}
	}
	return g.cur().Button != ""
}

// button is the step's button label. On the box, over an existing install, it says what
// installing would do.
func (g *game) button() string {
	st := g.cur()
	switch {
	case st.Type != "box" || g.removing || g.installedVersion == "":
	case newer(man.Product.Version, g.installedVersion):
		return "update."
	case !newer(g.installedVersion, man.Product.Version):
		return "reinstall."
	}
	return st.Button
}

func (g *game) primary() {
	switch g.cur().Type {
	case "box":
		g.spinAt, g.to = g.t, g.step+1
	case "finish":
		g.quit = true
	default:
		g.to = g.step + 1
	}
}

func (g *game) enter(n int) {
	g.step, g.to, g.stepAt, g.focus, g.scroll = n, n, g.t, -1, 0
	if g.cur().Type == "progress" && g.removing {
		go func() { g.prog.finish(uninstall(&g.prog)) }()
	} else if g.cur().Type == "progress" {
		st := state{Dirs: map[string]string{}, Updater: g.updater}
		for _, c := range g.chosen() {
			st.Dirs[c.ID] = g.dirs[c.ID]
		}
		go func() { g.prog.finish(install(st, &g.prog)) }()
	}
}

func (g *game) tile(i int) rect {
	const w, gap = 108, 13
	n := float64(len(g.comps))
	return rect{winW/2 - (n*w+(n-1)*gap)/2 + float64(i)*(w+gap), bodyY, w, 132}
}

func (g *game) changeBox(i int) rect { return rect{right - 76, bodyY + float64(i)*48, 76, 40} }

// items are the clickable things on the current step, in tab order.
func (g *game) items() []item {
	var its []item
	switch g.cur().Type {
	case "components":
		for i, c := range g.comps {
			its = append(its, item{g.tile(i), func() { g.sel[c.ID] = !g.sel[c.ID] }})
		}
	case "folders":
		for i, c := range g.chosen() {
			its = append(its, item{g.changeBox(i), func() { g.pick(c) }})
		}
	case "options":
		its = append(its, item{optBox, func() { g.updater = !g.updater }})
	}
	if label, act := g.back(); label != "" {
		its = append(its, item{backBox, act})
	}
	if g.primaryOK() {
		its = append(its, item{btnBox, g.primary})
	}
	return its
}

func (g *game) Update() error {
	if !g.probed {
		// Every Mac on macOS 12 or later with a GPU has Metal, so OpenGL here means Apple's software
		// renderer (a VM, a Hackintosh; see patches/). Run lighter there: 30 fps, no supersampling.
		var d ebiten.DebugInfo
		ebiten.ReadDebugInfo(&d)
		g.light, g.probed = runtime.GOOS == "darwin" && d.GraphicsLibrary == ebiten.GraphicsLibraryOpenGL, true
	}
	if wait := time.Second/30 - time.Since(g.last); g.light && wait > 0 {
		time.Sleep(wait)
	}
	now := time.Now()
	dt := min(now.Sub(g.last).Seconds(), 0.1)
	g.last = now
	g.t += dt
	cx, cy := ebiten.CursorPosition()
	g.mx, g.my = float64(cx)/g.s, float64(cy)/g.s
	spin := g.spin()
	g.yaw += dt * (0.45 + 20*spin*spin)

	switch {
	case g.spinAt >= 0: // leaving the box: it spins and whitens, then everything else fades
		g.dim = clamp01((g.t - g.spinAt - spinTime) / restTime)
		g.hdim = g.dim
		if g.t-g.spinAt >= spinTime+restTime {
			g.spinAt = -1
			g.enter(g.to)
		}
	case g.to != g.step || g.swap != nil:
		if g.dim = min(1, g.dim+dt/fadeTime); g.dim == 1 {
			if g.swap != nil {
				g.steps, g.swap, g.removing, g.to = g.swap, nil, !g.removing, 0
			}
			g.enter(g.to)
		}
	default:
		g.dim, g.hdim = max(0, g.dim-dt/fadeTime), max(0, g.hdim-dt/fadeTime)
	}

	select {
	case p := <-g.picked:
		g.picking = false
		if p[1] != "" {
			g.dirs[p[0]] = p[1]
		}
	default:
	}

	working := g.cur().Type == "progress"
	if working {
		done, total, _, finished, _ := g.prog.get()
		if total > 0 {
			g.bar += (float64(done)/float64(total) - g.bar) * min(1, dt*6)
		}
		if finished && g.to == g.step && g.t-g.stepAt > minWork && (g.bar > 0.99 || g.failed() != nil) {
			g.to = g.step + 1
		}
	}

	if g.quit {
		return ebiten.Termination
	}

	// Borderless: the header strip moves the window, the x closes it.
	pressed := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	released := inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft)
	if released && closeBox.has(g.mx, g.my) && !g.drag && !working {
		return ebiten.Termination
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) && !working {
		return ebiten.Termination
	}
	if pressed && g.my < headerH && !closeBox.has(g.mx, g.my) {
		g.drag, g.dragX, g.dragY = true, g.mx, g.my
	}
	if g.drag {
		if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			g.drag = false
		} else {
			x, y := ebiten.WindowPosition()
			ebiten.SetWindowPosition(x+int(math.Round(g.mx-g.dragX)), y+int(math.Round(g.my-g.dragY)))
		}
		return nil
	}
	if g.busy() {
		return nil
	}

	its := g.items()
	if released {
		for _, it := range its {
			if it.r.has(g.mx, g.my) {
				it.act()
				return nil
			}
		}
	}
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	switch {
	case len(its) == 0:
	case inpututil.IsKeyJustPressed(ebiten.KeyTab) && shift, inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft):
		g.focus = (max(g.focus, 0) - 1 + len(its)) % len(its)
	case inpututil.IsKeyJustPressed(ebiten.KeyTab), inpututil.IsKeyJustPressed(ebiten.KeyArrowRight):
		g.focus = (g.focus + 1) % len(its)
	case inpututil.IsKeyJustPressed(ebiten.KeySpace) && g.focus >= 0 && g.focus < len(its):
		its[g.focus].act()
	case inpututil.IsKeyJustPressed(ebiten.KeyEnter), inpututil.IsKeyJustPressed(ebiten.KeySpace):
		if g.primaryOK() {
			g.primary()
		}
	case inpututil.IsKeyJustPressed(ebiten.KeyBackspace):
		if label, act := g.back(); label == "back." {
			act()
		}
	}

	if g.cur().Type == "licence" {
		_, wy := ebiten.Wheel()
		d := -int(wy * 3)
		for k, n := range map[ebiten.Key]int{ebiten.KeyArrowDown: 1, ebiten.KeyArrowUp: -1, ebiten.KeyPageDown: 10, ebiten.KeyPageUp: -10} {
			if inpututil.IsKeyJustPressed(k) || inpututil.KeyPressDuration(k) > 20 {
				d += n
			}
		}
		g.scroll = max(0, min(g.scroll+d, len(g.licence())-g.licVisible()))
	}
	return nil
}

// pick asks the OS for a folder without blocking the window; the answer arrives on g.picked.
// the platform's own picker run as a tool (PowerShell, osascript, zenity or kdialog)
func (g *game) pick(c *component) {
	if g.picking {
		return
	}
	g.picking = true
	id, cur := c.ID, g.dirs[c.ID]
	go func() {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			q := "'" + strings.ReplaceAll(cur, "'", "''") + "'"
			cmd = exec.Command("powershell", "-NoProfile", "-STA", "-Command",
				"Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.SelectedPath = "+q+"; if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }")
		case "darwin":
			cmd = exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Choose a folder")`)
		default:
			cmd = exec.Command("zenity", "--file-selection", "--directory", "--filename="+cur+"/")
			if _, err := exec.LookPath("zenity"); err != nil {
				cmd = exec.Command("kdialog", "--getexistingdirectory", cur)
			}
		}
		hide(cmd)
		out, err := cmd.Output()
		dir := strings.TrimSpace(string(out))
		if err != nil || dir == "" {
			g.picked <- [2]string{id, ""}
			return
		}
		g.picked <- [2]string{id, filepath.Clean(dir)}
	}()
}

func (g *game) licVisible() int { return int((licBox.h - 16) / 20) }

func (g *game) licence() []string {
	if g.lic == nil {
		var paras []string
		for _, p := range strings.Split(strings.ReplaceAll(string(mustRead(g.cur().File)), "\r\n", "\n"), "\n\n") {
			paras = append(paras, strings.Join(strings.Fields(p), " "))
		}
		g.lic = wrap(strings.Join(paras, "\n\n"), g.small, (licBox.w-32)*g.s)
	}
	return g.lic
}

// wrap breaks s into lines no wider than width (device pixels); "\n" starts a new line.
func wrap(s string, face text.Face, width float64) []string {
	var out []string
	space := text.Advance(" ", face)
	for _, para := range strings.Split(s, "\n") {
		line, w := "", 0.0
		for _, word := range strings.Fields(para) {
			ww := text.Advance(word, face)
			if line != "" && w+space+ww > width {
				out = append(out, line)
				line, w = "", 0
			}
			if line != "" {
				line, w = line+" ", w+space
			}
			line, w = line+word, w+ww
		}
		out = append(out, line)
	}
	return out
}

// fit cuts s from the left until it is no wider than width, for long folder paths.
func fit(s string, face text.Face, width float64) string {
	if text.Advance(s, face) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && text.Advance("..."+string(r), face) > width {
		r = r[1:]
	}
	return "..." + string(r)
}

// view spins p with the box, then tilts it toward the camera.
func (g *game) view(p vec3) vec3 {
	sy, cy := math.Sincos(g.yaw)
	st, ct := math.Sincos(tilt)
	x, z := p.x*cy+p.z*sy, p.z*cy-p.x*sy
	return vec3{x, p.y*ct - z*st, p.y*st + z*ct}
}

// renderBox draws the box into the layer at ss times the resolution, for smooth edges once shrunk.
func (g *game) renderBox(bob, ss float64) {
	w, h := int(layerW*ss*g.s), int(layerH*ss*g.s)
	if g.layer == nil || g.layer.Bounds().Dx() != w || g.layer.Bounds().Dy() != h {
		g.layer = ebiten.NewImage(w, h)
	}
	g.layer.Clear()
	k := ss * g.s
	cam := vec3{0, 0, camZ}
	for _, f := range sides {
		n := g.view(f.v.cross(f.u))
		mid := g.view(f.o.add(f.u.mul(0.5)).add(f.v.mul(0.5)))
		if n.dot(cam.add(mid.mul(-1))) <= 0 {
			continue // facing away; the box is convex so this is all the depth sorting it needs
		}
		br := float32(min(1, 0.5+0.6*max(0, n.dot(lightDir)/n.norm())))
		g.vs, g.is = g.vs[:0], g.is[:0]
		for j := 0; j <= sub; j++ {
			for i := 0; i <= sub; i++ {
				a, b := float64(i)/sub, float64(j)/sub
				p := g.view(f.o.add(f.u.mul(a)).add(f.v.mul(b)))
				d := focal / (camZ - p.z)
				g.vs = append(g.vs, ebiten.Vertex{
					DstX:   float32((layerW/2 + p.x*d) * k),
					DstY:   float32((layerH/2 - p.y*d + bob) * k),
					SrcX:   float32((f.mx + a*f.u.norm()) * ppu),
					SrcY:   float32((f.my + b*f.v.norm()) * ppu),
					ColorR: br, ColorG: br, ColorB: br, ColorA: 1,
				})
				if i < sub && j < sub {
					q := uint16(j*(sub+1) + i)
					g.is = append(g.is, q, q+1, q+sub+1, q+1, q+sub+2, q+sub+1)
				}
			}
		}
		g.layer.DrawTriangles(g.vs, g.is, g.box, &ebiten.DrawTrianglesOptions{Filter: ebiten.FilterLinear})
	}
}

// drawBox draws the bobbing box, whitened by white, and its shadow, faded by rest.
func (g *game) drawBox(dst *ebiten.Image, white, rest float64) {
	s := g.s
	up := math.Sin(g.t * 1.8) // 1 = top of the bob
	if rest < 1 {             // the shadow shrinks and lightens as the box rises
		w, h := 92*(1-0.08*up), 24*(1-0.08*up)
		a := float32((1 - rest) * (0.85 - 0.15*up))
		op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
		op.GeoM.Scale(w*s/64, h*s/64)
		op.GeoM.Translate((boxX-w/2)*s, (shadowY-h/2)*s)
		op.ColorScale.Scale(a, a, a, a)
		dst.DrawImage(g.shadow, op)
	}
	if white >= 1 {
		return
	}
	ss := 2.0 // supersampling, which the software renderer can't afford
	if g.light {
		ss = 1
	}
	g.renderBox(-5*up, ss)
	var cm colorm.ColorM
	cm.Scale(1-white, 1-white, 1-white, 1)
	cm.Translate(white, white, white, 0)
	op := &colorm.DrawImageOptions{Filter: ebiten.FilterLinear}
	op.GeoM.Scale(1/ss, 1/ss)
	op.GeoM.Translate(math.Round((boxX-layerW/2)*s), math.Round((boxY-layerH/2)*s))
	colorm.DrawImage(dst, g.layer, cm, op)
}

func (g *game) fill(dst *ebiten.Image, r rect, c color.RGBA) {
	s := g.s
	vector.FillRect(dst, float32(r.x*s), float32(r.y*s), float32(r.w*s), float32(r.h*s), c, false)
}

func (g *game) stroke(dst *ebiten.Image, r rect, width float64, c color.RGBA) {
	s := g.s
	h := width / 2 // StrokeRect centres the line on the edge; keep it inside
	vector.StrokeRect(dst, float32((r.x+h)*s), float32((r.y+h)*s), float32((r.w-width)*s), float32((r.h-width)*s), float32(width*s), c, false)
}

func (g *game) label(dst *ebiten.Image, face text.Face, str string, x, y float64, align text.Align, c color.RGBA) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x*g.s, y*g.s)
	op.PrimaryAlign = align
	op.ColorScale.ScaleWithColor(c)
	text.Draw(dst, str, face, op)
}

// icon draws a black-on-clear icon in colour c.
func (g *game) icon(dst *ebiten.Image, name string, r rect, c color.RGBA) {
	img := g.image(name)
	var cm colorm.ColorM
	cm.Scale(0, 0, 0, 1)
	cm.Translate(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255, 0)
	op := &colorm.DrawImageOptions{Filter: ebiten.FilterLinear}
	b := img.Bounds()
	op.GeoM.Scale(r.w*g.s/float64(b.Dx()), r.h*g.s/float64(b.Dy()))
	op.GeoM.Translate(r.x*g.s, r.y*g.s)
	colorm.DrawImage(dst, img, cm, op)
}

func capH(f text.Face, s float64) float64 { return f.Metrics().HAscent / s }

func (g *game) Draw(screen *ebiten.Image) {
	s := g.s
	// One full-window pass, then only the frame's strips: repainting the whole window per layer
	// is what a software renderer can't keep up with.
	sw, sh := float32(winW*s), float32(winH*s)
	screen.Fill(white)
	for _, r := range []rect{{0, 0, winW, frame}, {0, winH - frame, winW, frame}, {0, frame, frame, winH - 2*frame}, {winW - frame, frame, frame, winH - 2*frame}} {
		g.fill(screen, r, pale)
	}
	vector.StrokeRect(screen, 0.5, 0.5, sw-1, sh-1, 1, edge, false) // the borderless window's outline

	st, d := g.cur(), g.dim
	fg := func(c color.RGBA) color.RGBA { return toWhite(c, d) }
	err := g.failed()
	working := st.Type == "progress"

	// Header: product and version, the publisher, the x.
	hc := func(c color.RGBA) color.RGBA { return toWhite(c, g.hdim) }
	g.label(screen, g.body, man.Product.Name+" v"+man.Product.Version, frame+4, 21, text.AlignStart, hc(black))
	pub := strings.SplitN(man.Product.Publisher, ".", 2) // musica.studio: "musica." black, "studio" grey
	x := closeBox.x - 8
	if len(pub) == 2 {
		g.label(screen, g.body, pub[1], x, 21, text.AlignEnd, hc(grey))
		x -= text.Advance(pub[1], g.body) / s
		pub[0] += "."
	}
	g.label(screen, g.body, pub[0], x, 21, text.AlignEnd, hc(black))
	xc := black
	if working {
		xc = mid
	} else if g.hot(closeBox) {
		g.fill(screen, closeBox, hc(pale))
	}
	g.label(screen, g.body, "x", closeBox.x+closeBox.w/2, 21, text.AlignCenter, hc(xc)) // on the header's baseline

	switch {
	case st.Type == "box":
		g.drawBox(screen, max(smooth(g.spin()), d), d)
	case st.Type == "finish" && st.Box && err == nil:
		g.drawBox(screen, d, d)
	}

	title, intro, introW := st.Title, st.Text, float64(right-left)
	if st.Type == "finish" {
		if err != nil {
			title, intro = "failed.", err.Error()
		} else if st.Box {
			introW = 170 // a column beside the box
		}
	}
	if title != "" {
		g.label(screen, g.big, title, left, titleY, text.AlignStart, fg(black))
	}
	for i, l := range wrap(intro, g.small, introW*s) {
		g.label(screen, g.small, l, left, introY+20*float64(i), text.AlignStart, fg(grey))
	}

	switch st.Type {
	case "licence":
		g.fill(screen, licBox, fg(pale))
		lines, vis := g.licence(), g.licVisible()
		for i := 0; i < vis && g.scroll+i < len(lines); i++ {
			g.label(screen, g.small, lines[g.scroll+i], licBox.x+12, licBox.y+8+20*float64(i), text.AlignStart, fg(black))
		}
		if n := len(lines); n > vis {
			th := licBox.h * float64(vis) / float64(n)
			ty := licBox.y + (licBox.h-th)*float64(g.scroll)/float64(n-vis)
			g.fill(screen, rect{licBox.x + licBox.w - 6, ty, 4, th}, fg(black))
		}

	case "components":
		about := ""
		for i, c := range g.comps {
			r, on := g.tile(i), g.sel[c.ID]
			bg, ink := white, mid
			if on {
				bg, ink = pale, black
			}
			if g.hot(r) && on {
				bg = hover
			} else if g.hot(r) {
				bg = faint
			}
			g.fill(screen, r, fg(bg))
			if !on {
				g.stroke(screen, r, 2, fg(pale))
			} else {
				g.fill(screen, rect{r.x + r.w - 16, r.y + 8, 8, 8}, fg(black))
			}
			g.icon(screen, c.Icon, rect{r.x + r.w/2 - 32, r.y + 22, 64, 64}, fg(ink))
			g.label(screen, g.small, c.Name, r.x+r.w/2, r.y+100, text.AlignCenter, fg(ink))
			if g.hot(r) || g.focus == i {
				about = c.About
			}
		}
		for i, l := range wrap(about, g.small, (right-left)*s) {
			g.label(screen, g.small, l, left, bodyY+152+20*float64(i), text.AlignStart, fg(grey))
		}

	case "folders":
		for i, c := range g.chosen() {
			y := bodyY + 48*float64(i)
			g.icon(screen, c.Icon, rect{left, y + 4, 32, 32}, fg(black))
			g.label(screen, g.small, c.Name, left+44, y+2, text.AlignStart, fg(black))
			g.label(screen, g.small, fit(g.dirs[c.ID], g.small, 460*s), left+44, y+22, text.AlignStart, fg(grey))
			ink := black
			if g.hot(g.changeBox(i)) || g.picking {
				ink = mid
			}
			g.label(screen, g.small, "change.", right, y+12, text.AlignEnd, fg(ink))
		}

	case "options":
		o := man.option("updater")
		if g.hot(optBox) {
			g.fill(screen, optBox, fg(faint))
		}
		cb := rect{left + 8, optBox.y + 16, 20, 20}
		g.stroke(screen, cb, 2, fg(black))
		if g.updater {
			g.fill(screen, rect{cb.x + 5, cb.y + 5, 10, 10}, fg(black))
		}
		g.icon(screen, o.Icon, rect{left + 44, optBox.y + 2, 48, 48}, fg(black))
		g.label(screen, g.body, o.Name, left+108, optBox.y+6, text.AlignStart, fg(black))
		g.label(screen, g.small, o.Note, left+120+text.Advance(o.Name, g.body)/s, optBox.y+12, text.AlignStart, fg(grey))
		for i, l := range wrap(o.Text, g.small, (right-left-116)*s) {
			g.label(screen, g.small, l, left+108, optBox.y+40+20*float64(i), text.AlignStart, fg(grey))
		}

	case "progress":
		_, _, what, _, _ := g.prog.get()
		bar := rect{left, 228, right - left, 12}
		g.fill(screen, bar, fg(pale))
		g.fill(screen, rect{bar.x, bar.y, bar.w * g.bar, bar.h}, fg(black))
		g.label(screen, g.small, fit(what, g.small, 520*s), left, 252, text.AlignStart, fg(grey))
		g.label(screen, g.small, fmt.Sprintf("%d%%", int(g.bar*100+0.5)), right, 252, text.AlignEnd, fg(grey))
	}

	// Bottom row: back, the step's button, where you are.
	if st.Button != "" && !working {
		ok, bg, ink := g.primaryOK(), pale, black
		if !ok {
			ink = mid
		} else if g.hot(btnBox) {
			bg = hover
		}
		g.fill(screen, btnBox, fg(bg))
		g.label(screen, g.body, g.button(), btnX+btnW/2, btnY+(btnH-capH(g.body, s))/2, text.AlignCenter, fg(ink))
	}
	if label, _ := g.back(); label != "" {
		ink := black
		if g.hot(backBox) {
			ink = grey
		}
		g.label(screen, g.body, label, left, btnY+(btnH-capH(g.body, s))/2, text.AlignStart, fg(ink))
	}
	n := len(g.steps)
	for i := range n {
		c := pale
		if i < g.step {
			c = mid
		} else if i == g.step {
			c = black
		}
		g.fill(screen, rect{right - 14*float64(n-i) + 6, btnY + btnH/2 - 4, 8, 8}, fg(c))
	}

	if its := g.items(); g.focus >= 0 && g.focus < len(its) {
		r := its[g.focus].r
		g.stroke(screen, rect{r.x - 4, r.y - 4, r.w + 8, r.h + 8}, 2, fg(black))
	}
}

func (g *game) Layout(w, h int) (int, int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	if s != g.s {
		g.s = s
		g.small, g.body, g.big = g.face(16*s), g.face(24*s), g.face(32*s)
		g.lic = nil
	}
	return int(float64(w) * s), int(float64(h) * s)
}

// face is the pixel font, with Go Mono Bold for the glyphs it lacks (all but ASCII, as build.sh
// subsets it), scaled so both share an ascent and lines keep their place.
func (g *game) face(size float64) text.Face {
	if g.font == nil {
		return &text.GoTextFace{Source: g.mono, Size: size}
	}
	a := &text.GoTextFace{Source: g.font, Size: size}
	m := &text.GoTextFace{Source: g.mono, Size: size}
	m.Size *= a.Metrics().HAscent / m.Metrics().HAscent
	f, _ := text.NewMultiFace(a, m)
	return f
}

// newShadow is a soft grey disc, stretched into an ellipse when drawn.
func newShadow() *ebiten.Image {
	const n = 64
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			d := math.Hypot(float64(x)-n/2+0.5, float64(y)-n/2+0.5) / (n / 2)
			a := 1 - smooth(clamp01((d-0.35)/0.65))
			img.SetRGBA(x, y, color.RGBA{uint8(float64(grey.R) * a), uint8(float64(grey.G) * a), uint8(float64(grey.B) * a), uint8(255 * a)})
		}
	}
	return ebiten.NewImageFromImage(img)
}
