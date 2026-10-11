# AK820 Max Plus — Vendor HID Protocol

The vendor protocol of the Ajazz AK820 Max Plus (Windows driver
`DeviceDriver.exe` v1.0.4.6), reverse-engineered by static analysis of the
driver and verified against hardware. Line addresses refer to the unmodified
driver binary.

## Transport

The keyboard exposes three USB interfaces (VID:PID `1A2C:A036` wired /
`1A2C:8FFF` 2.4G dongle):

| Linux if | Report descriptor | Role |
|---|---|---|
| **0** | Usage page `0xFF02`, 64-byte Input **and** 64-byte Output reports, no report IDs | Vendor config channel. All commands and replies live here. |
| 1 | Boot keyboard, no report IDs | Keys + LEDs |
| 2 | Usage page `0xFF00`, report ID 6, 3-byte input only | Auxiliary; no output or feature reports |

`config.xml` in the driver references `MI_02`, but the driver selects its
handle by usage page `0xFF02`, which is interface 0. Writes to interface 2
are silently ignored by the firmware.

Frame layout (64 bytes on the wire, no report ID byte):

```
offset 0..3 : AA 55 CC 33            (magic, little-endian dword 0x33CC55AA)
offset 4    : CMD
offset 5..63: payload, zero-padded
```

Replies mirror the frame. The battery command (`0x0E`) answers immediately;
the firmware also pushes battery status autonomously every 5 seconds
(`[AA 55 CC 33][0E][percent][conn]`, percent < 100).

Bluetooth mode has no config channel: the HID descriptor is keyboard-only,
writes are accepted but never answered, and control-channel reports fail.
Configuration requires USB or the 2.4G dongle.

The driver also contains senders for other connection classes (alternate
wired frames `[00][88][00][mode]` / `[00][89][00][flags R G B brightness]`,
Bluetooth variants `07 F7 ...` / `BB AA 99 88 AA ...`, and a flush frame with
magic `FF FE AA FF`). The `AA 55 CC 33` format works on wired connections and
is the only one verified.

## Command table

| CMD | Name | Payload (offset 5+) | Status |
|-----|------|---------------------|--------|
| `0x00`-`0x02` | config blob write, 3 parts | 54-byte slice of a ≤162-byte blob; part number is the CMD byte; 20 ms between parts | not verified |
| `0x04` | key-macro/chunk write | `[seq+1, chunkId, (len)]` | not verified |
| `0x07` | set light mode | `[mode-1, brightness-1, speed-1, direction, colorindex]` | verified |
| `0x08` | factory reset | empty; destructive | verified |
| `0x09` | apply/commit | empty; no reply observed | verified |
| `0x0A` | query config | empty; no reply observed | not verified |
| `0x0B` | custom-mode init | empty; precedes the `0xF6..` per-key upload | verified |
| `0x0C` | sleep time | `[1..4]` (5 min / 15 min / 30 min / never) | sends accepted, effect not reproduced |
| `0x0D` | key response time | `[1..3]` gears (~2-3 ms steps on wired) | sends accepted, effect imperceptible |
| `0x0E` | battery | reply `[percent, conn]` | verified |
| `0x0F` | handshake | empty; fire-and-forget | verified |
| `0x10` | fn-switch toggle | empty | sends accepted, effect not user-observable |
| `0x88`/`0x89` | wired light params | RGB variant carries `[flags R G B brightness]` | refuted: no effect on wired |
| `0xF0`-`0xF5` | per-key RGB | 6 × 16 keys × RGB | not verified |
| `0xF6`-`0xFB` | per-key RGB (custom mode) | 6 × 18 keys × RGB, slots 0-107 | verified |

## Light modes

Value = mode number in `0x07` payload byte 0 (sent as mode-1):

1 left-right ripple, 2 starry dots, 3 up-down ripple, 4 bidirectional
ejection, 5 spectral cycle, 6 kaleidoscope, 7 static, 8 breathing, 9 cross,
10 xiaguang flyover, 11 single-key breathing, 12 scroll painting, 13 snake,
14 tornado, 15 top3-bottom3, 16 rain, 17 windmill, 18 sine wave,
19 galloping horse, 20 crisscross, 21 water ripple, 22 custom.
Modes 23/24 select the custom per-key path (`0x0B` + `0xF6`-`0xFB`).

## Color palette

`0x07` payload byte 4 selects a palette index (verified):

| index | color |
|---|---|
| 0 | red |
| 1 | green |
| 2 | blue |
| 3 | yellow |
| 4 | purple |
| 5 | cyan |
| 6 | white |
| 7 | rainbow (default on animated modes) |

## Per-key RGB (custom mode)

Sequence: `0x07 [23, brightness-1, speed-1, direction, 0]` → `0x0B` → `0x09`
→ six frames `0xF6+i`, each carrying 18 keys × 3 RGB bytes (slots 0-17,
18-35, … 90-107).

Slot → physical key mapping (from the driver's `layouts/kb-k600t.xml`,
`key_index` attribute): Esc 15, F1 14 … F12 3, Del 43, backtick 16, digits
17-26, minus 27, equal 28, Backspace 29, Home 31, Tab 57, QWERTY row 56-47,
brackets 46/45, backslash 44, PgUp 32, CapsLock 58, ASDF row 59-67,
semicolon 68, apostrophe 69, Enter 70, PgDn 41, LShift 90, ZXCV row 89-80,
RShift 79, Up 78, End 42, LCtrl 91, LWin 92, LAlt 93, Space 94, RAlt 95,
Fn 97, RCtrl 98, arrows 99-101.

## Driver state

The Windows driver stores configuration in SQLite
(`t_light_data`, `t_key_macro_data`, `t_configdata`, …) under
`AppData/Local/AJAZZ AK820 Max Plus三模RGB版 Files/db/`. Light rows carry
profile, mode, brightness, speed, direction, colorful, colorindex,
color_value, config_func, reserved, status.
