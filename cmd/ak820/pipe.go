package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	dev "github.com/NotAProton/ak820-go-drivers/internal/device"
	p "github.com/NotAProton/ak820-go-drivers/internal/protocol"
)

// cmdPipe implements `ak820 pipe`: a streaming transport primitive that reads
// "<cmd-hex> <payload-hex>" lines from stdin, frames them with the magic
// prefix and zero padding, and writes them to the vendor channel.
//
// It exists so companion tools (ak820-utils) can drive the keyboard through
// the core binary instead of duplicating device handling.
func cmdPipe() int {
	if !gv.yes && !gv.dryRun {
		err := fmt.Errorf("%w: pipe sends arbitrary frame streams to the keyboard; confirm with --yes", p.ErrUsage)
		failJSON(err)
		return exitUsage
	}
	node, err := dev.PickDevice(gv.device)
	if err != nil {
		var nf *dev.DeviceNotFoundError
		if errors.As(err, &nf) {
			return notFoundErr(err)
		}
		return deviceErr(err)
	}
	if gv.dryRun {
		emit(map[string]any{"ok": true, "dry_run": true, "device": node.Path,
			"note": "pipe reads stdin lines '<cmd-hex> <payload-hex>'"})
		return exitOK
	}
	d, oerr := dev.OpenDevice(node, gv.quiet, func(s string) { logf("%s", s) })
	if oerr != nil {
		return deviceErr(oerr)
	}
	defer d.Close()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	sent, bad := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmd, payload, err := parsePipeLine(line)
		if err != nil {
			bad++
			logf("pipe: %v", err)
			continue
		}
		if _, err := d.Send(cmd, payload, 0); err != nil {
			emit(map[string]any{"ok": false, "sent": sent, "rejected": bad, "error": err.Error()})
			return deviceErr(err)
		}
		sent++
	}
	if serr := scanner.Err(); serr != nil {
		emit(map[string]any{"ok": false, "sent": sent, "rejected": bad, "error": serr.Error()})
		return usageErr("reading stdin: %v", serr)
	}
	emit(map[string]any{"ok": true, "sent": sent, "rejected": bad})
	if !gv.json {
		fmt.Fprintf(stdout, "pipe: %d sent, %d rejected\n", sent, bad)
	}
	logf("pipe: %d frames sent, %d rejected", sent, bad)
	return exitOK
}

// keymapJSON exposes the physical key -> RGB slot mapping and the per-key
// upload geometry so companion tools can address keys without embedding
// protocol knowledge.
func keymapJSON() map[string]any {
	keys := map[string]int{}
	for name, slot := range p.KeyIndex {
		keys[name] = slot
	}
	return map[string]any{
		"slots":          p.SlotCount,
		"cmd_base":       fmt.Sprintf("0x%02x", p.CmdPerKeyCustom0),
		"frames":         p.PerKeyFrames,
		"keys_per_frame": p.KeysPerFrame,
		"keys":           keys,
	}
}

// parsePipeLine parses one pipe input line: "<cmd-hex> [payload-hex]".
// The command parses as hex with or without a 0x prefix ("0b" and "0x0b"
// are both 11). Comments (#) and blank lines are handled by the caller.
func parsePipeLine(line string) (byte, []byte, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, nil, fmt.Errorf("malformed line %q (want '<cmd-hex> [payload-hex]')", line)
	}
	cs := strings.TrimPrefix(strings.TrimPrefix(fields[0], "0x"), "0X")
	if cs == "" {
		return 0, nil, fmt.Errorf("empty cmd in %q", line)
	}
	cmd, err := strconv.ParseUint(cs, 16, 8)
	if err != nil {
		return 0, nil, fmt.Errorf("bad cmd %q: %w", fields[0], err)
	}
	var payload []byte
	if len(fields) >= 2 {
		payload, err = p.ParseHex(strings.Join(fields[1:], ""))
		if err != nil {
			return 0, nil, fmt.Errorf("bad payload: %w", err)
		}
		if len(payload) > p.FrameSize-5 {
			return 0, nil, fmt.Errorf("payload max %d bytes, got %d", p.FrameSize-5, len(payload))
		}
	}
	return byte(cmd), payload, nil
}
