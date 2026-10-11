package protocol

import (
	"fmt"
	"strconv"
)

func BuildModeFrames(args []string) ([][]byte, map[string]any, error) {
	if len(args) == 0 {
		return nil, nil, fmt.Errorf("%w: set mode requires a mode number; valid: %s", ErrUsage, ModeList())
	}
	mode, err := strconv.Atoi(args[0])
	if err != nil || !ValidMode(mode) {
		return nil, nil, fmt.Errorf("%w: invalid mode %q; valid: %s", ErrUsage, args[0], ModeList())
	}
	brightness, speed, direction := 5, 4, 0
	colorIdx := -1
	noApply := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--brightness":
			brightness, err = parseRange(args, &i, 1, 5, "brightness")
		case "--speed":
			speed, err = parseRange(args, &i, 1, 5, "speed")
		case "--direction":
			direction, err = parseRange(args, &i, 0, 3, "direction")
		case "--color":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%w: --color needs a value; valid: %s", ErrUsage, PaletteList())
			}
			i++
			var ci byte
			ci, err = ParseColorIndex(args[i])
			colorIdx = int(ci)
		case "--no-apply":
			noApply = true
		default:
			return nil, nil, fmt.Errorf("%w: unknown flag %q for set mode (valid: --brightness --speed --direction --color --no-apply)", ErrUsage, args[i])
		}
		if err != nil {
			return nil, nil, err
		}
	}
	if colorIdx < 0 {
		colorIdx = 7 // driver default (rainbow)
	}
	payload := []byte{byte(mode - 1), byte(brightness - 1), byte(speed - 1), byte(direction), byte(colorIdx)}
	frames := [][]byte{Frame(CmdSetLightMode, payload)}
	if mode == CustomMode {
		frames = append(frames, Frame(CmdCustomModeInit, nil))
	}
	if !noApply {
		frames = append(frames, Frame(CmdApply, nil))
	}
	name := ModeNames[mode]
	if name == "" && mode == CustomMode {
		name = "custom (per-key)"
	}
	colorName := "rainbow"
	for name, idx := range Palette {
		if int(idx) == colorIdx && name != "lightblue" {
			colorName = name
			break
		}
	}
	desc := map[string]any{"mode": mode, "name": name, "brightness": brightness,
		"speed": speed, "direction": direction, "color": colorName,
		"colorindex": colorIdx, "applied": !noApply}
	return frames, desc, nil
}

func ValidMode(m int) bool { return (m >= MinMode && m <= MaxMode) || m == CustomMode }

func parseRange(args []string, i *int, min, max int, what string) (int, error) {
	if *i+1 >= len(args) {
		return 0, fmt.Errorf("%w: %s needs a value %d-%d", ErrUsage, what, min, max)
	}
	*i++
	v, err := strconv.Atoi(args[*i])
	if err != nil || v < min || v > max {
		return 0, fmt.Errorf("%w: %s must be %d-%d, got %q", ErrUsage, what, min, max, args[*i])
	}
	return v, nil
}
