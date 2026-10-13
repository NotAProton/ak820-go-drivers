package protocol

import (
	"fmt"
	"strings"
)

// KeyIndex maps normalized key names to per-key RGB slot indices.
// Source: extracted/app/layouts/kb-k600t.xml (key_index attributes).
// Slots 0-107 are transmitted in 0xF6..0xFB frames, 18 keys (3 bytes RGB) each.
var KeyIndex = map[string]int{
	"esc": 15, "f1": 14, "f2": 13, "f3": 12, "f4": 11, "f5": 10, "f6": 9,
	"f7": 8, "f8": 7, "f9": 6, "f10": 5, "f11": 4, "f12": 3, "del": 43,

	"grave": 16, "`": 16, "1": 17, "2": 18, "3": 19, "4": 20, "5": 21,
	"6": 22, "7": 23, "8": 24, "9": 25, "0": 26, "minus": 27, "equal": 28,
	"backspace": 29, "home": 31,

	"tab": 57, "q": 56, "w": 55, "e": 54, "r": 53, "t": 52, "y": 51,
	"u": 50, "i": 49, "o": 48, "p": 47, "lbracket": 46, "rbracket": 45,
	"backslash": 44, "pgup": 32,

	"capslock": 58, "a": 59, "s": 60, "d": 61, "f": 62, "g": 63, "h": 64,
	"j": 65, "k": 66, "l": 67, "semicolon": 68, "apostrophe": 69,
	"enter": 70, "pgdn": 41,

	"lshift": 90, "z": 89, "x": 88, "c": 87, "v": 86, "b": 85, "n": 84,
	"m": 83, "comma": 82, "period": 81, "slash": 80, "rshift": 79,
	"up": 78, "end": 42,

	"lctrl": 91, "lwin": 92, "lalt": 93, "space": 94, "ralt": 95,
	"fn": 97, "rctrl": 98, "left": 99, "down": 100, "right": 101,
}

// buildPerKeyFrames renders the six 0xF6..0xFB frames for a 108-slot RGB
// buffer (3 bytes per key).
func BuildPerKeyFrames(slots [][3]byte) [][]byte {
	frames := make([][]byte, 0, PerKeyFrames)
	for i := 0; i < PerKeyFrames; i++ {
		payload := make([]byte, 0, PerKeyFrameLen)
		for s := i * KeysPerFrame; s < (i+1)*KeysPerFrame && s < len(slots); s++ {
			payload = append(payload, slots[s][0], slots[s][1], slots[s][2])
		}
		for len(payload) < PerKeyFrameLen {
			payload = append(payload, 0)
		}
		frames = append(frames, Frame(byte(CmdPerKeyCustom0+i), payload))
	}
	return frames
}

// BuildCustomModeFrames returns the full custom per-key upload sequence:
// set-mode(24) + init + apply + six per-key frames. Single source of truth
// for the custom path; CLI callers must use this instead of hardcoding
// 0x07/0x0B/0x09 bytes.
func BuildCustomModeFrames(slots [][3]byte, brightness, speed byte) [][]byte {
	frames := [][]byte{
		Frame(CmdSetLightMode, []byte{byte(CustomMode - 1), brightness, speed, 0, 0}),
		Frame(CmdCustomModeInit, nil),
		Frame(CmdApply, nil),
	}
	return append(frames, BuildPerKeyFrames(slots)...)
}

// colorBytes resolves a per-key color: palette name or RRGGBB hex.
func ColorBytes(s string) ([3]byte, error) {
	var out [3]byte
	lower := strings.ToLower(s)
	if idx, ok := Palette[lower]; ok && lower != "rainbow" {
		switch idx {
		case 0:
			out = [3]byte{0xFF, 0x00, 0x00}
		case 1:
			out = [3]byte{0x00, 0xFF, 0x00}
		case 2:
			out = [3]byte{0x00, 0x40, 0xFF}
		case 3:
			out = [3]byte{0xFF, 0xFF, 0x00}
		case 4:
			out = [3]byte{0x80, 0x00, 0xFF}
		case 5:
			out = [3]byte{0x00, 0xE0, 0xFF}
		case 6:
			out = [3]byte{0xFF, 0xFF, 0xFF}
		default:
			return out, fmt.Errorf("%w: rainbow is not a per-key color; use RRGGBB hex", ErrUsage)
		}
		return out, nil
	}
	r, g, b, err := ParseColor(s)
	if err != nil {
		return out, fmt.Errorf("%w: per-key color must be a palette name (red green blue yellow purple cyan white) or RRGGBB hex: %v", ErrUsage, err)
	}
	return [3]byte{r, g, b}, nil
}
