package main

import (
	"encoding/json"
	"errors"
	"fmt"
	dev "github.com/NotAProton/ak820-go-drivers/internal/device"
	p "github.com/NotAProton/ak820-go-drivers/internal/protocol"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const version = "1.0.0"

// Exit codes (stable, documented in agent-context).
const (
	exitOK        = 0
	exitUsage     = 1
	exitDeviceErr = 2
	exitNotFound  = 3
)

type globals struct {
	device  string
	json    bool
	quiet   bool
	dryRun  bool
	yes     bool
	wait    time.Duration
	timeout time.Duration
}

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	gv               = globals{timeout: 1200 * time.Millisecond}
)

func usage() string {
	return `ak820 — control the Ajazz AK820 Max Plus keyboard (Linux, hidraw)

Usage: ak820 [flags] <command> [args]

Commands:
  list                        List AK820 hidraw nodes and their capabilities
  ping                        Handshake check (0x0F)
  get battery                 Battery percent + connection state
  get info                    dev.Device node, bus, interface details
  set mode N                  Light mode; N: 1-22 (named), 23/24=custom per-key (23 aliases 24)
      [--brightness 1-5] [--speed 1-5] [--direction 0-3]
      [--color red|green|blue|yellow|purple|cyan|white|rainbow] [--no-apply]
  set key NAME=COLOR ...      Custom per-key colors (mode 24), e.g.
                              set key esc=ff0000 space=green
                              key names: esc f1-f12 letters digits space
                              fn modifiers arrows ... (see agent-context)
  set sleep-time V            1=5min 2=15min 3=30min 4=never (experimental)
  set response-time V         1=fast 2=medium 3=slow (experimental)
  apply                       Commit pending changes (0x09)
  fnswitch                    Toggle the Fn-switch state (0x10)
  pipe                        Stream frames from stdin: "<cmd-hex> <payload-hex>" per line
  reset                       FACTORY RESET (0x08) — requires --yes
  raw --cmd 0xNN [--data HEX] Send a raw frame — requires --yes
  agent-context               Machine-readable capability manifest (JSON)
  version                     Print version

Global flags:
  --device PATH    hidraw node (--device=PATH also works; precedence: flag > $AK820_DEVICE > auto-detect)
  --json           Structured JSON on stdout for every data command
  --quiet          Suppress frame logging (stderr)
  --timeout DUR    Per-command device timeout (default 1.2s)
  --wait DUR       Retry device discovery for up to DUR before failing
  --dry-run        Print the frames that would be sent; never touch the device
  --yes, -y        Confirm destructive/raw mutations (non-interactive)

Notes:
  - Never prompts. stdout carries data, stderr carries logs/errors.
  - Exit codes: 0 ok, 1 usage, 2 device error, 3 device not found.
  - The config channel needs USB (or the 2.4G dongle); Bluetooth cannot
    configure this keyboard — 'ak820 list' marks BT nodes accordingly.
`
}

func emit(v any) {
	if gv.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintf(stderr, "error writing stdout: %v\n", err)
		}
	}
}

func logf(format string, a ...any) {
	if !gv.quiet {
		fmt.Fprintf(stderr, format+"\n", a...)
	}
}

func failJSON(err error) {
	if gv.json {
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(map[string]any{"ok": false, "error": err.Error()}) // best-effort error envelope
	}
	fmt.Fprintln(stderr, "error:", err.Error())
}

func usageErr(format string, a ...any) int {
	err := fmt.Errorf(format, a...)
	failJSON(err)
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Run 'ak820 --help' for usage.")
	return exitUsage
}

func notFoundErr(err error) int {
	failJSON(err)
	return exitNotFound
}

func deviceErr(err error) int {
	failJSON(err)
	return exitDeviceErr
}

func framesHex(fs [][]byte) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%x", f)
	}
	return out
}

// sendFrames transmits frames; in dry-run it only logs. The last frame waits
// for a reply when waitLast is set.
func sendFrames(d *dev.Device, frames [][]byte, label string, waitLast bool) error {
	if gv.dryRun {
		for _, f := range frames {
			logf("DRY-RUN %s TX %x", label, f)
		}
		return nil
	}
	for i, f := range frames {
		if i > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		if waitLast && i == len(frames)-1 {
			_, err := d.Send(f[4], f[5:], gv.timeout)
			if err != nil {
				return err
			}
		} else {
			if _, err := d.Send(f[4], f[5:], 0); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- discovery + open (device commands) ---

// pickAndOpen resolves the device (with --wait retry) and opens it.
// In dry-run it skips opening and returns a nil device.
func pickAndOpen() (*dev.Device, int) {
	var node dev.Node
	var err error
	deadline := time.Now().Add(gv.wait)
	for {
		node, err = dev.PickDevice(gv.device)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		var nf *dev.DeviceNotFoundError
		if errors.As(err, &nf) {
			return nil, notFoundErr(err)
		}
		return nil, deviceErr(err)
	}
	if gv.dryRun {
		logf("[i] dry-run: would use %s (%s)", node.Path, node.Bus)
		return nil, exitOK
	}
	logf("[i] using %s (%s)", node.Path, node.Bus)
	d, err := dev.OpenDevice(node, gv.quiet, func(s string) { logf("%s", s) })
	if err != nil {
		return nil, deviceErr(err)
	}
	return d, exitOK
}

// --- commands ---

func cmdList() int {
	nodes := dev.DiscoverNodes()
	if gv.json {
		emit(map[string]any{"ok": true, "devices": nodes})
		return exitOK
	}
	if len(nodes) == 0 {
		fmt.Fprintln(stderr, "no AK820 devices found (plug in via USB, or use the 2.4G dongle)")
		return exitOK
	}
	for _, n := range nodes {
		status := "config channel"
		if !n.ConfigChan {
			status = "no config channel"
			if n.Err != "" {
				status += " (" + n.Err + ")"
			}
		}
		fmt.Fprintf(stdout, "%-14s %-10s if=%-3s %s\n", n.Path, n.Bus, n.Interface, status)
	}
	return exitOK
}

func cmdPing(d *dev.Device) int {
	if gv.dryRun {
		logf("DRY-RUN ping TX %x", p.Frame(p.CmdHandshake, nil))
		emit(map[string]any{"ok": true, "dry_run": true, "pong": false})
		if !gv.json {
			fmt.Fprintln(stdout, "sent (0x0F does not reply)")
		}
		return exitOK
	}
	if _, err := d.Send(p.CmdHandshake, nil, 0); err != nil {
		return deviceErr(err)
	}
	emit(map[string]any{
		"ok":    true,
		"pong":  false,
		"reply": "",
		"note":  "0x0F is fire-and-forget in the vendor driver; no reply is normal",
	})
	if !gv.json {
		fmt.Fprintln(stdout, "sent (0x0F does not reply)")
	}
	return exitOK
}

func cmdGetBattery(d *dev.Device) int {
	if gv.dryRun {
		logf("DRY-RUN get-battery TX %x", p.Frame(p.CmdBattery, nil))
		emit(map[string]any{"ok": true, "dry_run": true})
		return exitOK
	}
	rx, err := d.Send(p.CmdBattery, nil, gv.timeout)
	if err != nil {
		return deviceErr(err)
	}
	pct, conn, ok := p.BatteryReply(rx)
	if !ok {
		return deviceErr(fmt.Errorf("%w: no battery reply (the firmware pushes battery status every ~5s; try --timeout 3s)", dev.ErrDevice))
	}
	connStr := map[byte]string{0: "usb", 1: "2.4g", 2: "2.4g", 3: "usb-charging"}[conn]
	if connStr == "" {
		connStr = fmt.Sprintf("unknown-0x%02x", conn)
	}
	emit(map[string]any{"ok": true, "percent": pct, "connection": connStr,
		"raw_conn": fmt.Sprintf("0x%02x", conn)})
	if !gv.json {
		fmt.Fprintf(stdout, "%d%% (%s)\n", pct, connStr)
	}
	return exitOK
}

func cmdGetInfo(node *dev.Node) int {
	emit(map[string]any{"ok": true, "device": node.Path, "bus": node.Bus,
		"interface": node.Interface, "nodes": dev.DiscoverNodes(),
		"protocol": "AA55CC33-framed vendor HID, 64-byte reports, usage page 0xFF02"})
	if !gv.json {
		fmt.Fprintf(stdout, "device: %s (%s)\n", node.Path, node.Bus)
	}
	return exitOK
}

func cmdSetSimple(d *dev.Device, cmd byte, v, min, max int, what string, key string) (any, error) {
	if v < min || v > max {
		return nil, fmt.Errorf("%w: %s must be %d-%d, got %d", p.ErrUsage, what, min, max, v)
	}
	fs := [][]byte{p.Frame(cmd, []byte{byte(v)}), p.Frame(p.CmdApply, nil)}
	if err := sendFrames(d, fs, "set-"+what, false); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "dry_run": gv.dryRun, key: v, "frames": framesHex(fs)}, nil
}

func cmdSetKey(d *dev.Device, args []string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: set key requires at least one NAME=COLOR pair (e.g. esc=ff0000 space=green); key names: %s", p.ErrUsage, keyList())
	}
	var slots [][3]byte = make([][3]byte, p.SlotCount)
	var set []string
	seen := map[string]bool{}
	for _, a := range args {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: expected NAME=COLOR, got %q", p.ErrUsage, a)
		}
		lower := strings.ToLower(parts[0])
		if seen[lower] {
			return nil, fmt.Errorf("%w: duplicate key %q (each key may appear once)", p.ErrUsage, parts[0])
		}
		seen[lower] = true
		idx, ok := p.KeyIndex[lower]
		if !ok {
			return nil, fmt.Errorf("%w: unknown key %q; valid: %s", p.ErrUsage, parts[0], keyList())
		}
		rgb, err := p.ColorBytes(parts[1])
		if err != nil {
			return nil, err
		}
		slots[idx] = rgb
		set = append(set, fmt.Sprintf("%s(slot %d)=%02x%02x%02x", parts[0], idx, rgb[0], rgb[1], rgb[2]))
	}
	// Defaults match BuildModeFrames (brightness 5 -> 4, speed 4 -> 3).
	frames := p.BuildCustomModeFrames(slots, 4, 3)
	if err := sendFrames(d, frames, "set-key", false); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "dry_run": gv.dryRun, "keys": set,
		"mode": 24, "frames_sent": len(frames)}, nil
}

func keyList() string {
	names := make([]string, 0, len(p.KeyIndex))
	for k := range p.KeyIndex {
		names = append(names, k)
	}
	// stable-ish ordering for readability
	order := "esc f1 f2 f3 f4 f5 f6 f7 f8 f9 f10 f11 f12 del grave 1 2 3 4 5 6 7 8 9 0 minus equal backspace home tab q w e r t y u i o p lbracket rbracket backslash pgup capslock a s d f g h j k l semicolon apostrophe enter pgdn lshift z x c v b n m comma period slash rshift up end lctrl lwin lalt space ralt fn rctrl left down right"
	var out []string
	for _, k := range strings.Fields(order) {
		if _, ok := p.KeyIndex[k]; ok {
			out = append(out, k)
		}
	}
	return strings.Join(out, " ")
}

func cmdFnSwitch(d *dev.Device) (any, error) {
	frames := [][]byte{p.Frame(p.CmdFnSwitch, nil)}
	if gv.dryRun {
		return map[string]any{"ok": true, "dry_run": true, "note": "0x10 toggles the keyboard's Fn-switch state; no ACK", "frames": framesHex(frames)}, nil
	}
	// 0x10 has no observable ACK; fire-and-forget.
	if err := sendFrames(d, frames, "fnswitch", false); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "dry_run": gv.dryRun, "note": "0x10 toggles the keyboard's Fn-switch state; no ACK"}, nil
}

func cmdRaw(d *dev.Device, args []string) (any, error) {
	if !gv.yes && !gv.dryRun {
		return nil, fmt.Errorf("%w: raw sends arbitrary frames to the keyboard; confirm with --yes", p.ErrUsage)
	}
	var cmdByte byte
	var data []byte
	var haveCmd bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cmd":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%w: --cmd needs a value (decimal or 0x..)", p.ErrUsage)
			}
			i++
			v, err := p.ParseU32(args[i])
			if err != nil || v > 0xFF {
				return nil, fmt.Errorf("%w: --cmd must be 0-255, got %q", p.ErrUsage, args[i])
			}
			cmdByte = byte(v)
			haveCmd = true
		case "--data":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%w: --data needs a hex string", p.ErrUsage)
			}
			i++
			var err error
			data, err = p.ParseHex(args[i])
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: unknown flag %q for raw (valid: --cmd --data)", p.ErrUsage, args[i])
		}
	}
	if !haveCmd {
		return nil, fmt.Errorf("%w: raw requires --cmd (0-255)", p.ErrUsage)
	}
	if len(data) > p.FrameSize-5 {
		return nil, fmt.Errorf("%w: payload max %d bytes, got %d", p.ErrUsage, p.FrameSize-5, len(data))
	}
	if gv.dryRun {
		f := p.Frame(cmdByte, data)
		logf("DRY-RUN raw TX %x", f)
		return map[string]any{"ok": true, "dry_run": true, "cmd": fmt.Sprintf("0x%02x", cmdByte), "frame": fmt.Sprintf("%x", f)}, nil
	}
	rx, err := d.Send(cmdByte, data, gv.timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "cmd": fmt.Sprintf("0x%02x", cmdByte), "reply": fmt.Sprintf("%x", rx)}, nil
}

func cmdReset(d *dev.Device) (any, error) {
	if !gv.yes && !gv.dryRun {
		return nil, fmt.Errorf("%w: factory reset erases ALL keyboard settings; confirm with --yes", p.ErrUsage)
	}
	fs := [][]byte{p.Frame(p.CmdFactoryReset, nil)}
	// The reset frame has no reliable reply; do not burn --timeout waiting.
	if err := sendFrames(d, fs, "reset", false); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "dry_run": gv.dryRun, "reset": "sent (firmware may need a few seconds)"}, nil
}

func cmdAgentContext() int {
	gv.json = true // the manifest is always machine-readable
	emit(map[string]any{
		"name":                  "ak820",
		"version":               version,
		"agent_context_version": 1,
		"description":           "Control the Ajazz AK820 Max Plus keyboard over hidraw",
		"protocol": map[string]any{
			"transport":   "USB HID vendor interface (usage page 0xFF02), 64-byte reports, no report IDs",
			"frame":       "[AA 55 CC 33][CMD][payload] zero-padded to 64 bytes",
			"connections": []string{"usb (config ok)", "2.4g dongle 1a2c:8fff (config ok)", "bluetooth (NO config channel)"},
			"verified":    []string{"0x0E battery", "0x07 set-mode", "0x07 colorindex palette", "0x09 apply", "0x0B+0xF6..0xFB per-key RGB", "0x08 factory-reset"},
			"refuted":     []string{"0x89 RGB color frame (no effect on wired connection)"},
			"send_only":   []string{"0x0C sleep-time (effect not reproduced)", "0x0D response-time (effect imperceptible ~4-5ms)", "0x10 fn-switch (effect not user-observable)"},
		},
		"commands": []map[string]any{
			{"name": "list", "json": true},
			{"name": "ping", "json": true},
			{"name": "get", "args": "battery|info", "json": true},
			{"name": "set mode", "args": "1-22|23|24 [--brightness 1-5] [--speed 1-5] [--direction 0-3] [--color red|green|blue|yellow|purple|cyan|white|rainbow] [--no-apply]", "json": true},
			{"name": "set key", "args": "name=color [name=color ...] (mode 24; palette names or RRGGBB)", "json": true},
			{"name": "set sleep-time", "args": "1-4", "json": true, "experimental": true},
			{"name": "set response-time", "args": "1-3", "json": true, "experimental": true},
			{"name": "apply", "json": true},
			{"name": "reset", "destructive": true, "requires": "--yes"},
			{"name": "raw", "args": "--cmd 0xNN [--data HEX]", "destructive": true, "requires": "--yes"},
			{"name": "agent-context", "json": true},
			{"name": "version"},
		},
		"per_key": keymapJSON(),
		"enums": map[string]any{
			"mode":          p.ModeList(),
			"color":         p.PaletteList(),
			"brightness":    "1-5",
			"speed":         "1-5",
			"direction":     "0-3",
			"sleep_time":    "1=5min,2=15min,3=30min,4=never",
			"response_time": "1=fast,2=medium,3=slow",
		},
		"env":        map[string]string{"AK820_DEVICE": "hidraw path override"},
		"flags":      []string{"--device", "--json", "--quiet", "--timeout DUR", "--wait DUR", "--dry-run", "--yes"},
		"exit_codes": map[string]string{"0": "ok", "1": "usage error", "2": "device error", "3": "device not found"},
	})
	return exitOK
}

// --- arg parsing ---

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid duration %q (e.g. 2s, 500ms)", p.ErrUsage, s)
	}
	return d, nil
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return exitOK
	}
	// Global flags are accepted in any position, including after the
	// subcommand (agent-friendly).
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			gv.json = true
		case a == "--quiet" || a == "-q":
			gv.quiet = true
		case a == "--dry-run":
			gv.dryRun = true
		case a == "--yes" || a == "-y":
			gv.yes = true
		case a == "--device" || len(a) > 9 && a[:9] == "--device=":
			v := ""
			if len(a) > 9 && a[:9] == "--device=" {
				v = a[9:]
				if v == "" {
					return usageErr("--device needs a path")
				}
			} else {
				if i+1 >= len(args) {
					return usageErr("--device needs a path")
				}
				i++
				v = args[i]
			}
			gv.device = v
		case a == "--timeout" || len(a) > 10 && a[:10] == "--timeout=":
			s := ""
			if len(a) > 10 && a[:10] == "--timeout=" {
				s = a[10:]
			} else {
				if i+1 >= len(args) {
					return usageErr("--timeout needs a duration")
				}
				i++
				s = args[i]
			}
			d, err := parseDuration(s)
			if err != nil {
				return usageErr("%v", err)
			}
			gv.timeout = d
		case a == "--wait" || len(a) > 7 && a[:7] == "--wait=":
			s := ""
			if len(a) > 7 && a[:7] == "--wait=" {
				s = a[7:]
			} else {
				if i+1 >= len(args) {
					return usageErr("--wait needs a duration")
				}
				i++
				s = args[i]
			}
			d, err := parseDuration(s)
			if err != nil {
				return usageErr("%v", err)
			}
			gv.wait = d
		default:
			cmdArgs = append(cmdArgs, a)
		}
	}
	if len(cmdArgs) == 0 {
		fmt.Fprint(stdout, usage())
		return exitOK
	}

	switch cmdArgs[0] {
	case "list":
		return cmdList()
	case "agent-context":
		return cmdAgentContext()
	case "version":
		if gv.json {
			emit(map[string]any{"ok": true, "version": version})
		} else {
			fmt.Fprintln(stdout, "ak820", version)
		}
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage())
		return exitOK
	case "pipe":
		return cmdPipe()
	case "ping", "get", "set", "apply", "reset", "raw", "fnswitch":
	default:
		return usageErr("unknown command %q; valid: list ping get set apply reset raw fnswitch pipe agent-context version", cmdArgs[0])
	}

	rest := cmdArgs[1:]
	if cmdArgs[0] == "set" && len(rest) >= 1 && (rest[0] == "sleep-time" || rest[0] == "response-time") && len(rest) != 2 {
		what := rest[0]
		if what == "sleep-time" {
			return usageErr("set sleep-time takes exactly one value 1-4 (1=5min 2=15min 3=30min 4=never), got %q", rest[1:])
		}
		return usageErr("set response-time takes exactly one value 1-3 (1=fast 2=medium 3=slow), got %q", rest[1:])
	}
	for _, a := range rest {
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Fprint(stdout, usage())
			return exitOK
		}
	}

	// Every device handler is dry-run safe (frame dumps, no I/O), so dry-run
	// never requires the device to exist.
	needsDevice := !gv.dryRun

	var d *dev.Device
	if needsDevice {
		var code int
		d, code = pickAndOpen()
		if d == nil {
			return code
		}
		defer d.Close()
	}
	switch cmdArgs[0] {
	case "ping":
		return cmdPing(d)
	case "get":
		if len(rest) == 0 {
			return usageErr("get requires battery|info")
		}
		switch rest[0] {
		case "battery":
			return cmdGetBattery(d)
		case "info":
			node, err := dev.PickDevice(gv.device)
			if err != nil {
				var nf *dev.DeviceNotFoundError
				if errors.As(err, &nf) {
					return notFoundErr(err)
				}
				return deviceErr(err)
			}
			return cmdGetInfo(&node)
		default:
			return usageErr("get requires battery|info, got %q", firstArg(rest))
		}
	case "set":
		if len(rest) == 0 {
			return usageErr("set requires mode|key|sleep-time|response-time (color goes via: set mode N --color red|green|...)")
		}
		var out any
		var err error
		switch rest[0] {
		case "key":
			out, err = cmdSetKey(d, rest[1:])
		case "mode":
			var frames [][]byte
			var desc map[string]any
			frames, desc, err = p.BuildModeFrames(rest[1:])
			if err != nil {
				break
			}
			err = sendFrames(d, frames, "set-mode", false)
			if err != nil {
				break
			}
			out = map[string]any{"ok": true, "dry_run": gv.dryRun, "set": desc, "frames": framesHex(frames)}
		case "sleep-time":
			if len(rest) != 2 {
				return usageErr("set sleep-time takes exactly one value 1-4 (1=5min 2=15min 3=30min 4=never), got %q", rest[1:])
			}
			v, e := strconv.Atoi(rest[1])
			if e != nil {
				return usageErr("set sleep-time requires 1-4 (1=5min 2=15min 3=30min 4=never)")
			}
			out, err = cmdSetSimple(d, p.CmdSleepTime, v, 1, 4, "sleep-time", "sleep_time")
		case "response-time":
			if len(rest) != 2 {
				return usageErr("set response-time takes exactly one value 1-3 (1=fast 2=medium 3=slow), got %q", rest[1:])
			}
			v, e := strconv.Atoi(rest[1])
			if e != nil {
				return usageErr("set response-time requires 1-3 (1=fast 2=medium 3=slow)")
			}
			out, err = cmdSetSimple(d, p.CmdKeyResponse, v, 1, 3, "response-time", "response_time")
		default:
			return usageErr("set requires mode|key|sleep-time|response-time, got %q (for color use: set mode N --color red|green|blue|yellow|purple|cyan|white|rainbow)", rest[0])
		}
		if err != nil {
			if errors.Is(err, p.ErrUsage) {
				return usageErr("%v", err)
			}
			return deviceErr(err)
		}
		emit(out)
		if !gv.json {
			fmt.Fprintf(stdout, "set %s done\n", rest[0])
		}
		return exitOK
	case "apply":
		if err := sendFrames(d, [][]byte{p.Frame(p.CmdApply, nil)}, "apply", false); err != nil {
			return deviceErr(err)
		}
		emit(map[string]any{"ok": true, "applied": true, "dry_run": gv.dryRun})
		if !gv.json {
			fmt.Fprintln(stdout, "committed (0x09)")
		}
		return exitOK
	case "fnswitch":
		out, err := cmdFnSwitch(d)
		if err != nil {
			return deviceErr(err)
		}
		emit(out)
		if !gv.json {
			fmt.Fprintln(stdout, "fn-switch toggled (0x10)")
		}
		return exitOK
	case "reset":
		out, err := cmdReset(d)
		if err != nil {
			if errors.Is(err, p.ErrUsage) {
				return usageErr("%v", err)
			}
			return deviceErr(err)
		}
		emit(out)
		if !gv.json {
			fmt.Fprintln(stdout, "factory reset sent")
		}
		return exitOK
	case "raw":
		out, err := cmdRaw(d, rest)
		if err != nil {
			if errors.Is(err, p.ErrUsage) {
				return usageErr("%v", err)
			}
			return deviceErr(err)
		}
		emit(out)
		if !gv.json {
			m := out.(map[string]any)
			if gv.dryRun {
				fmt.Fprintf(stdout, "sent %s (dry-run)\n", m["cmd"])
			} else {
				fmt.Fprintf(stdout, "sent %s, reply: %s\n", m["cmd"], m["reply"])
			}
		}
		return exitOK
	}
	return exitOK
}
