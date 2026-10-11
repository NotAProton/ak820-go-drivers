// Package viz renders an audio spectrum on the keyboard: cava feeds 18
// frequency bars, and each key lights according to the bar level at its
// column position.
package viz

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/NotAProton/ak820-go-drivers/internal/device"
	"github.com/NotAProton/ak820-go-drivers/internal/protocol"
)

const (
	numBars    = 18
	numRows    = 6
	targetFPS  = 60
	peakDecay  = 0.035
	smoothRate = 25.0
)

// theme is a color gradient from silence to peak.
type theme struct {
	colors    [][3]byte
	peakColor [3]byte
}

var themes = map[string]theme{
	"synthwave": {
		colors:    [][3]byte{{10, 2, 30}, {60, 8, 90}, {140, 10, 130}, {220, 30, 120}, {255, 90, 90}, {255, 200, 120}},
		peakColor: [3]byte{255, 255, 220},
	},
	"ocean": {
		colors:    [][3]byte{{0, 5, 25}, {0, 40, 90}, {0, 100, 160}, {0, 170, 210}, {60, 220, 230}, {200, 255, 250}},
		peakColor: [3]byte{255, 255, 255},
	},
	"fire": {
		colors:    [][3]byte{{15, 0, 0}, {90, 8, 0}, {180, 30, 0}, {240, 90, 0}, {255, 160, 20}, {255, 240, 120}},
		peakColor: [3]byte{255, 255, 255},
	},
}

// ThemeNames lists available themes.
func ThemeNames() []string {
	out := make([]string, 0, len(themes))
	for k := range themes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// keyCell is one physical key placed in the bar grid.
type keyCell struct {
	slot int
	bar  int // 0..numBars-1
	row  int // 0..numRows-1, 0 = top (F-row)
}

// layout derives the bar grid from the protocol key map. Column comes from
// the keyboard's physical x-order, row from y-order (both from the driver's
// layout geometry, embedded here as row assignments).
func layout() []keyCell {
	// Physical rows top-to-bottom with the slots from the driver layout.
	rows := [][]int{
		{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 43},            // esc..del
		{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 31}, // `..home
		{57, 56, 55, 54, 53, 52, 51, 50, 49, 48, 47, 46, 45, 44, 32}, // tab..pgup
		{58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 41},     // caps..pgdn
		{90, 89, 88, 87, 86, 85, 84, 83, 82, 81, 80, 79, 78, 42},     // shift..end
		{91, 92, 93, 94, 95, 97, 98, 99, 100, 101},                   // ctrl..right
	}
	// Relative x centers per row (from the layout file), 0..1.
	rowX := [][]float64{
		{0.04, 0.09, 0.13, 0.17, 0.21, 0.26, 0.30, 0.35, 0.38, 0.44, 0.48, 0.52, 0.56, 0.62},
		{0.04, 0.08, 0.12, 0.16, 0.20, 0.24, 0.28, 0.33, 0.37, 0.41, 0.45, 0.49, 0.53, 0.57, 0.67},
		{0.04, 0.10, 0.14, 0.18, 0.22, 0.26, 0.30, 0.35, 0.39, 0.43, 0.47, 0.51, 0.55, 0.59, 0.67},
		{0.05, 0.11, 0.15, 0.19, 0.23, 0.27, 0.32, 0.36, 0.40, 0.44, 0.48, 0.52, 0.57, 0.67},
		{0.04, 0.13, 0.17, 0.21, 0.25, 0.29, 0.33, 0.38, 0.42, 0.46, 0.50, 0.54, 0.62, 0.67},
		{0.04, 0.09, 0.14, 0.22, 0.45, 0.49, 0.53, 0.58, 0.62, 0.66},
	}
	var cells []keyCell
	for r, row := range rows {
		for i, slot := range row {
			bar := int(rowX[r][i] * numBars)
			if bar >= numBars {
				bar = numBars - 1
			}
			cells = append(cells, keyCell{slot: slot, bar: bar, row: r})
		}
	}
	return cells
}

// Visualizer runs the render loop.
type Visualizer struct {
	dev     *device.Device
	theme   theme
	themeIx int
	cells   []keyCell
	slots   [][3]byte

	mu      sync.Mutex
	raw     []int
	peaks   []float64
	smooth  []float64
	running bool
	cava    *exec.Cmd
	sigCh   chan os.Signal
}

// New creates a visualizer with the named theme.
func New(dev *device.Device, themeName string) *Visualizer {
	t, ok := themes[themeName]
	if !ok {
		t = themes["synthwave"]
	}
	return &Visualizer{
		dev: dev, theme: t, cells: layout(),
		slots:  make([][3]byte, protocol.SlotCount),
		raw:    make([]int, numBars),
		peaks:  make([]float64, numBars),
		smooth: make([]float64, numBars),
	}
}

func (v *Visualizer) flush() {
	frames := protocol.BuildPerKeyFrames(v.slots)
	for _, f := range frames {
		_, _ = v.dev.Send(f[4], f[5:], 0)
	}
}

func (v *Visualizer) restore() {
	_, _ = v.dev.Send(protocol.CmdSetLightMode, []byte{0, 4, 3, 0, 7}, 0)
	_, _ = v.dev.Send(protocol.CmdApply, nil, 0)
}

func (v *Visualizer) startCava() error {
	if _, err := exec.LookPath("cava"); err != nil {
		return fmt.Errorf("the visualizer renders audio from cava (https://github.com/karlstav/cava); install it and retry — original error: %w", err)
	}
	cfg, err := os.CreateTemp("", "ak820-cava-*.conf")
	if err != nil {
		return err
	}
	cfg.WriteString("input = pulse\nmethod = pulse\nchannels = mono\n" +
		fmt.Sprintf("bars = %d\nsamplerate = 44100\nascii_max_range = 100\nbar_delay = 0\n", numBars))
	cfg.Close()
	v.cava = exec.Command("cava", "-p", cfg.Name())
	v.cava.Stdout = nil
	stdout, err := v.cava.StdoutPipe()
	if err != nil {
		return err
	}
	if err := v.cava.Start(); err != nil {
		return fmt.Errorf("starting cava: %w", err)
	}
	go func() {
		buf := make([]byte, numBars)
		for v.running {
			n, err := stdout.Read(buf)
			if err != nil {
				return
			}
			if n == numBars {
				v.mu.Lock()
				for i := 0; i < numBars; i++ {
					v.raw[i] = int(buf[i])
				}
				v.mu.Unlock()
			}
		}
	}()
	return nil
}

func (v *Visualizer) stopCava() {
	if v.cava != nil && v.cava.Process != nil {
		_ = v.cava.Process.Kill()
		_ = v.cava.Wait()
	}
}

func gradient(colors [][3]byte, val float64) [3]byte {
	if val < 0 {
		val = 0
	}
	if val > 1 {
		val = 1
	}
	f := val * float64(len(colors)-1)
	i0 := int(f)
	i1 := i0 + 1
	if i1 >= len(colors) {
		i1 = len(colors) - 1
	}
	t := f - float64(i0)
	c0, c1 := colors[i0], colors[i1]
	return [3]byte{
		byte(float64(c0[0]) + (float64(c1[0])-float64(c0[0]))*t),
		byte(float64(c0[1]) + (float64(c1[1])-float64(c0[1]))*t),
		byte(float64(c0[2]) + (float64(c1[2])-float64(c0[2]))*t),
	}
}

// Run creates a visualizer and blocks until Ctrl-C, then restores the keyboard.
func Run(dev *device.Device, themeName string) error {
	return New(dev, themeName).run()
}

// Run blocks until the user presses Ctrl-C, then restores the keyboard.
func (v *Visualizer) run() error {
	if err := v.startCava(); err != nil {
		return err
	}
	defer v.stopCava()
	defer v.restore()
	for i := range v.slots {
		v.slots[i] = [3]byte{2, 0, 8}
	}

	v.sigCh = make(chan os.Signal, 1)
	signal.Notify(v.sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	v.running = true
	defer func() { v.running = false }()

	frameBudget := time.Second / targetFPS
	last := time.Now()
	fmt.Println("visualizer running — Ctrl-C to stop")
	for v.running {
		loopStart := time.Now()
		dt := loopStart.Sub(last).Seconds()
		last = loopStart

		v.mu.Lock()
		for b := 0; b < numBars; b++ {
			level := float64(v.raw[b]) / 100.0
			if level > v.smooth[b] {
				v.smooth[b] += (level - v.smooth[b]) * (min64(1, dt*smoothRate))
			} else {
				v.smooth[b] += (level - v.smooth[b]) * 0.35
			}
			if v.smooth[b] > v.peaks[b] {
				v.peaks[b] = v.smooth[b]
			} else {
				v.peaks[b] -= peakDecay * dt * 60 / 60
			}
		}
		peaks := append([]float64(nil), v.peaks...)
		bars := append([]float64(nil), v.smooth...)
		v.mu.Unlock()

		for _, c := range v.cells {
			lvl := bars[c.bar]
			pk := peaks[c.bar]
			// Row 0 is the top of the board; light from the bottom up.
			height := float64(numRows-1-c.row) / float64(numRows-1)
			switch {
			case height <= lvl:
				v.slots[c.slot] = gradient(v.theme.colors, lvl)
			case abs(height-pk) < 0.14 && pk > 0.15:
				v.slots[c.slot] = v.theme.peakColor
			default:
				v.slots[c.slot] = [3]byte{2, 0, 8}
			}
		}
		v.flush()

		if wait := frameBudget - time.Since(loopStart); wait > 0 {
			time.Sleep(wait)
		}
		select {
		case <-v.sigCh:
			v.running = false
			fmt.Println("\nstopped")
		default:
		}
	}
	return nil
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
