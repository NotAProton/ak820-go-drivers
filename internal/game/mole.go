// Package game implements whack-a-mole on the physical keyboard: a target
// key lights up, the player must press it before the timer runs out.
package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NotAProton/ak820-go-drivers/internal/device"
	"github.com/NotAProton/ak820-go-drivers/internal/protocol"
	"github.com/NotAProton/ak820-go-drivers/internal/termui"
)

// keyNameBySlot inverts the protocol key map for the playable keys.
func playableKeys() map[int]string {
	out := map[int]string{}
	for name, slot := range protocol.KeyIndex {
		out[slot] = name
	}
	return out
}

// typedKey maps a raw terminal byte to a key name.
func typedKey(b byte) (string, bool) {
	switch {
	case b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return string(b), true
	case b == ' ':
		return "space", true
	}
	return "", false
}

const (
	ambientR, ambientG, ambientB = 6, 2, 22
	moleR, moleG, moleB          = 255, 0, 30
	goldR, goldG, goldB          = 255, 215, 0
	maxLives                     = 3
)

// Stats carries the end-of-run summary.
type Stats struct {
	Score        int     `json:"score"`
	HighScore    int     `json:"high_score"`
	NewHighScore bool    `json:"new_high_score"`
	Hits         int     `json:"hits"`
	Misses       int     `json:"misses"`
	Accuracy     int     `json:"accuracy_pct"`
	Level        int     `json:"level"`
	MaxCombo     int     `json:"max_combo"`
	AvgReaction  int     `json:"avg_reaction_ms"`
	Reason       string  `json:"end_reason"`
	Duration     float64 `json:"duration_s"`
}

// Game drives one whack-a-mole session.
type Game struct {
	dev       *device.Device
	keys      *termui.KeyReader
	score     int
	level     int
	lives     int
	hits      int
	misses    int
	combo     int
	maxCombo  int
	reactions []int
	start     time.Time
}

// New creates a game session.
func New(dev *device.Device, keys *termui.KeyReader) *Game {
	return &Game{dev: dev, keys: keys, level: 1, lives: maxLives, start: time.Now()}
}

func (g *Game) paint(r, gr, b byte) {
	var slots [][3]byte = make([][3]byte, protocol.SlotCount)
	for i := range slots {
		slots[i] = [3]byte{r, gr, b}
	}
	frames := protocol.BuildPerKeyFrames(slots)
	for _, f := range frames {
		_, _ = g.dev.Send(f[4], f[5:], 0)
	}
}

func (g *Game) setKey(name string, c [3]byte) {
	slot, ok := protocol.KeyIndex[name]
	if !ok || slot >= protocol.SlotCount {
		return
	}
	var slots [][3]byte = make([][3]byte, protocol.SlotCount)
	slots[slot] = c
	for _, f := range protocol.BuildPerKeyFrames(slots) {
		_, _ = g.dev.Send(f[4], f[5:], 0)
	}
}

func (g *Game) restore() {
	// Factory-default look: mode 1, brightness 5, speed 4, rainbow.
	_, _ = g.dev.Send(protocol.CmdSetLightMode, []byte{0, 4, 3, 0, 7}, 0)
	_, _ = g.dev.Send(protocol.CmdApply, nil, 0)
}

func highscorePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "ak820", "mole_highscore.json")
}

func loadHighscore() int {
	b, err := os.ReadFile(highscorePath())
	if err != nil {
		return 0
	}
	var v struct {
		HighScore int `json:"highscore"`
	}
	if json.Unmarshal(b, &v) != nil {
		return 0
	}
	return v.HighScore
}

func saveHighscore(score int) {
	p := highscorePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, _ := json.Marshal(map[string]int{"highscore": score})
	_ = os.WriteFile(p, b, 0o644)
}

func (g *Game) render(target string, golden bool, left, maxT float64, msg string) {
	bar := 24
	filled := int(left / maxT * float64(bar))
	if filled < 0 {
		filled = 0
	}
	fmt.Fprintf(os.Stdout, "\r\033[K score:%6d  combo:%2dx  lives:%s  lvl:%d  target:[%s] %s%s %s",
		g.score, g.combo, strings.Repeat("♥", g.lives)+strings.Repeat("·", maxLives-g.lives),
		g.level, strings.ToUpper(target), strings.Repeat("█", filled),
		strings.Repeat("░", bar-filled), msg)
}

// Run plays until lives run out or the player quits (q / Esc / Ctrl-C).
// It always restores the keyboard lighting before returning.
func (g *Game) Run() Stats {
	high := loadHighscore()
	g.paint(ambientR, ambientG, ambientB)
	fmt.Println("Whack-a-mole: press the lit key before the bar empties. q / Esc quits.")
	reason := "out of lives"
	defer g.restore()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	playable := playableKeys()
	var names []int
	for slot := range playable {
		names = append(names, slot)
	}
	sort.Ints(names)

round:
	for g.lives > 0 {
		maxT := 1.4 - float64(g.level-1)*0.12
		if maxT < 0.45 {
			maxT = 0.45
		}
		slot := names[rng.Intn(len(names))]
		target := playable[slot]
		golden := rng.Float64() < 0.15

		g.paint(ambientR, ambientG, ambientB)
		if golden {
			g.setKey(target, [3]byte{goldR, goldG, goldB})
		} else {
			g.setKey(target, [3]byte{moleR, moleG, moleB})
		}

		started := time.Now()
		for {
			left := maxT - time.Since(started).Seconds()
			g.render(strings.ToUpper(target[:1])+target[1:], golden, left, maxT, "")
			if left <= 0 {
				g.miss()
				g.render(target, golden, 0, maxT, "  too slow!")
				time.Sleep(300 * time.Millisecond)
				continue round
			}
			select {
			case k, ok := <-g.keys.Keys():
				if !ok {
					reason = "input closed"
					g.lives = 0
					break round
				}
				if k == 'q' || k == 27 || k == 3 {
					reason = "quit"
					g.lives = 0
					break round
				}
				name, ok := typedKey(byte(k))
				if !ok {
					continue
				}
				react := time.Since(started).Milliseconds()
				if name == target {
					g.hit(golden, left, maxT, react)
					g.render(target, golden, 0, maxT, fmt.Sprintf("  HIT! %d ms", react))
					time.Sleep(120 * time.Millisecond)
					continue round
				}
				g.miss()
				g.render(target, golden, left, maxT, fmt.Sprintf("  wrong key (%s)", name))
				time.Sleep(250 * time.Millisecond)
				continue round
			case <-time.After(20 * time.Millisecond):
			}
		}
	}

	// Drain input and finish.
	for {
		select {
		case <-g.keys.Keys():
			continue
		default:
		}
		break
	}
	g.paint(0, 0, 0)
	stats := Stats{
		Score: g.score, HighScore: high, Hits: g.hits, Misses: g.misses,
		Accuracy: g.hits * 100 / max(1, g.hits+g.misses), Level: g.level,
		MaxCombo: g.maxCombo, Reason: reason, Duration: time.Since(g.start).Seconds(),
	}
	if len(g.reactions) > 0 {
		sum := 0
		for _, r := range g.reactions {
			sum += r
		}
		stats.AvgReaction = sum / len(g.reactions)
	}
	if stats.Score > high {
		stats.NewHighScore = true
		stats.HighScore = stats.Score
		saveHighscore(stats.Score)
	}
	return stats
}

func (g *Game) hit(golden bool, left, maxT float64, reactMS int64) {
	g.hits++
	g.combo++
	if g.combo > g.maxCombo {
		g.maxCombo = g.combo
	}
	base := 500
	if golden {
		base = 1200
		if g.lives < maxLives {
			g.lives++
		}
	}
	bonus := int((maxT - left) / maxT * 500)
	mult := 1 + g.combo/3
	if mult > 5 {
		mult = 5
	}
	g.score += (base + bonus) * mult
	g.reactions = append(g.reactions, int(reactMS))
	if g.hits%5 == 0 {
		g.level++
	}
	g.paint(ambientR, ambientG, ambientB)
}

func (g *Game) miss() {
	g.misses++
	g.combo = 0
	g.lives--
}

// ErrNoTTY is returned when the game is started without an interactive terminal.
var ErrNoTTY = errors.New("whack-a-mole needs an interactive terminal (stdin is not a TTY)")

// RequiresTTY reports whether the game can run in this terminal.
func RequiresTTY() error {
	if !termui.IsTerminal(os.Stdin.Fd()) {
		return ErrNoTTY
	}
	return nil
}
