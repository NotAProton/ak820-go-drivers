// Package main implements the AK820 Max Plus vendor-protocol wire format.
//
// Verified live (Linux hidraw, USB wired):
//   - vendor channel = the USB interface whose HID report descriptor starts
//     with 06 02 FF (usage page 0xFF02): 64-byte Input + 64-byte Output
//     reports, NO report IDs.
//   - frame = [AA 55 CC 33][CMD][payload...] zero-padded to 64 bytes.
//   - replies mirror the frame; battery (0x0E) replies immediately.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	VIDWired  = 0x1A2C
	PIDWired  = 0xA036
	VID24G    = 0x1A2C
	PID24G    = 0x8FFF
	FrameSize = 64
	SlotCount = 108 // per-key RGB slots across 0xF6..0xFB frames
)

// Magic is the little-endian 0x33CC55AA frame prefix.
var Magic = []byte{0xAA, 0x55, 0xCC, 0x33}

// Command bytes (static analysis of DeviceDriver.exe 1.0.4.6 + live tests).
const (
	CmdBlobPart0      = 0x00 // 3-part config blob write, part 0
	CmdBlobPart1      = 0x01
	CmdBlobPart2      = 0x02
	CmdChunkWrite     = 0x04 // config chunk write [seq+1, chunkId, len?]
	CmdSetLightMode   = 0x07 // [mode-1, bright-1, speed-1, dir, extra]
	CmdFactoryReset   = 0x08 // destructive
	CmdApply          = 0x09 // commit
	CmdQueryConfig    = 0x0A
	CmdCustomModeInit = 0x0B // empty; after 0x07 when mode==24
	CmdSleepTime      = 0x0C // [1..4]
	CmdKeyResponse    = 0x0D // [1..3]
	CmdBattery        = 0x0E // reply [percent, connMode]
	CmdHandshake      = 0x0F
	CmdFnSwitch       = 0x10 // empty, toggle fn layer switch
	CmdPerKeyRGB0     = 0xF0 // 0xF0-0xF5 per-key RGB (16 keys x RGB each)
	CmdPerKeyCustom0  = 0xF6 // 0xF6-0xFB custom-mode per-key RGB
	CmdWiredMode      = 0x88 // wired alternate: [00 88 00 mode]
	CmdWiredParams    = 0x89 // wired alternate: [00 89 00 flags R G B brightness]
)

// Frame builds a 64-byte command frame.
func Frame(cmd byte, payload []byte) []byte {
	f := make([]byte, FrameSize)
	copy(f, Magic)
	f[4] = cmd
	copy(f[5:], payload)
	return f
}

// BatteryReply scans raw IN data for a battery response and returns
// (percent, connection-byte, true).
func BatteryReply(data []byte) (int, byte, bool) {
	for i := 0; i+6 < len(data); i++ {
		if data[i] == Magic[0] && data[i+1] == Magic[1] && data[i+2] == Magic[2] &&
			data[i+3] == Magic[3] && data[i+4] == CmdBattery && data[i+5] <= 100 {
			return int(data[i+5]), data[i+6], true
		}
	}
	return 0, 0, false
}

// ModeNames maps mode number (1-based) to the driver's UI names.
var ModeNames = map[int]string{
	1:  "left-right ripple",
	2:  "starry dots",
	3:  "up-down ripple",
	4:  "bidirectional ejection",
	5:  "spectral cycle",
	6:  "kaleidoscope",
	7:  "static constant light",
	8:  "overall breathing",
	9:  "cross operation",
	10: "xiaguang flyover",
	11: "single-key breathing",
	12: "jiangshan picture scroll",
	13: "snake shaped winding",
	14: "tornado storm",
	15: "top three bottom three",
	16: "rain falling pavilion",
	17: "windmill",
	18: "sine wave",
	19: "galloping horse",
	20: "crisscrossing",
	21: "water ripple diffusion",
	22: "custom (per-key)",
}

const (
	MinMode       = 1
	MaxMode       = 22
	CustomMode    = 24 // driver treats 23/24 as the custom per-key mode
	MinBrightness = 1
	MaxBrightness = 5
	MinSpeed      = 1
	MaxSpeed      = 5
)

// ModeList renders the valid mode enum for error messages.
func ModeList() string {
	var b strings.Builder
	for i := MinMode; i <= MaxMode; i++ {
		if i > MinMode {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d=%s", i, ModeNames[i])
	}
	b.WriteString(", 24=custom (per-key)")
	return b.String()
}

// ErrUsage marks errors that should print usage hints.
var ErrUsage = errors.New("usage error")

// Palette maps colorindex values (0x07 payload byte 5) to colors.
// Verified live 2026-10-11: static mode, colorindex 0..7.
var Palette = map[string]byte{
	"red": 0, "green": 1, "blue": 2, "yellow": 3,
	"purple": 4, "cyan": 5, "lightblue": 5, "white": 6, "rainbow": 7,
}

// PaletteList renders the valid color enum for error messages.
func PaletteList() string {
	return "0=red, 1=green, 2=blue, 3=yellow, 4=purple, 5=cyan, 6=white, 7=rainbow " +
		"(names also accepted: red green blue yellow purple cyan white rainbow)"
}

// ParseColorIndex resolves a palette name or 0-7 index.
func ParseColorIndex(s string) (byte, error) {
	if v, ok := Palette[strings.ToLower(s)]; ok {
		return v, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 7 {
		return 0, fmt.Errorf("%w: invalid color %q; valid: %s", ErrUsage, s, PaletteList())
	}
	return byte(n), nil
}

// ParseHex decodes a hex string into bytes (spaces/colons tolerated).
func ParseHex(s string) ([]byte, error) {
	s = strings.NewReplacer(" ", "", ":", "", ",", "").Replace(s)
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("%w: hex payload must have even length: %q", ErrUsage, s)
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		_, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &out[i])
		if err != nil {
			return nil, fmt.Errorf("%w: invalid hex byte %q", ErrUsage, s[i*2:i*2+2])
		}
	}
	return out, nil
}

// ParseColor decodes RRGGBB into three bytes.
func ParseColor(s string) (r, g, b byte, err error) {
	v, err := ParseHex(s)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(v) != 3 {
		return 0, 0, 0, fmt.Errorf("%w: color must be RRGGBB (6 hex digits), got %q", ErrUsage, s)
	}
	return v[0], v[1], v[2], nil
}

// ParseU32 parses decimal, or hex when the value carries a 0x/0X prefix.
func ParseU32(s string) (uint32, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		var v uint64
		h := s[2:]
		if h == "" {
			return 0, fmt.Errorf("%w: invalid number %q", ErrUsage, s)
		}
		for _, c := range h {
			var d uint64
			switch {
			case c >= '0' && c <= '9':
				d = uint64(c - '0')
			case c >= 'a' && c <= 'f':
				d = uint64(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = uint64(c-'A') + 10
			default:
				return 0, fmt.Errorf("%w: invalid number %q", ErrUsage, s)
			}
			v = v*16 + d
			if v > 0xFFFFFFFF {
				return 0, fmt.Errorf("%w: number too large %q", ErrUsage, s)
			}
		}
		return uint32(v), nil
	}
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: invalid number %q", ErrUsage, s)
		}
		v = v*10 + uint64(c-'0')
		if v > 0xFFFFFFFF {
			return 0, fmt.Errorf("%w: number too large %q", ErrUsage, s)
		}
	}
	return uint32(v), nil
}

// u16 is a helper for tests.
func u16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }

// IsVendorDescriptor reports whether a report descriptor is the 0xFF02
// 64-byte vendor in/out channel (the config transport).
func IsVendorDescriptor(desc []byte) bool {
	return len(desc) >= 3 && desc[0] == 0x06 && desc[1] == 0x02 && desc[2] == 0xFF
}
