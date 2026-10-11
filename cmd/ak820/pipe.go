package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
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
	if !gv.yes {
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
	scanner.Buffer(make([]byte, 1024), 1024)
	sent, bad := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || len(fields) > 2 {
			bad++
			fmt.Fprintf(stderr, "pipe: malformed line %q (want '<cmd-hex> [payload-hex]')\n", line)
			continue
		}
		cmd, err := p.ParseU32(fields[0])
		if err != nil || cmd > 0xFF {
			bad++
			fmt.Fprintf(stderr, "pipe: bad cmd %q: %v\n", fields[0], err)
			continue
		}
		var payload []byte
		if len(fields) == 2 {
			payload, err = p.ParseHex(fields[1])
			if err != nil {
				bad++
				fmt.Fprintf(stderr, "pipe: bad payload: %v\n", err)
				continue
			}
			if len(payload) > p.FrameSize-5 {
				bad++
				fmt.Fprintf(stderr, "pipe: payload max %d bytes, got %d\n", p.FrameSize-5, len(payload))
				continue
			}
		}
		if _, err := d.Send(byte(cmd), payload, 0); err != nil {
			return deviceErr(err)
		}
		sent++
	}
	if serr := scanner.Err(); serr != nil {
		return deviceErr(fmt.Errorf("reading stdin: %w", serr))
	}
	emit(map[string]any{"ok": true, "sent": sent, "rejected": bad})
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
		"frames":         6,
		"keys_per_frame": p.SlotCount / 6,
		"keys":           keys,
	}
}
