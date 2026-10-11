# ak820

Linux control for the Ajazz AK820 Max Plus keyboard, built on the reverse-
engineered vendor HID protocol. One static Go binary, designed for humans and
agents alike.

The vendor driver ships only for Windows. This project speaks the keyboard's
config channel directly over hidraw, so lighting, per-key color, power
settings, and diagnostics all work natively on Linux — over USB or the 2.4G
dongle (Bluetooth carries no config channel; the keyboard firmware does not
expose one).

## Features

- Battery and connection status, device discovery
- All 22 lighting modes plus custom per-key RGB (every key individually)
- Palette colors, brightness, speed, direction
- Sleep timer, key response time, Fn switch, factory reset
- `whack-a-mole` on the physical keyboard
- Audio spectrum visualizer (18 bars via [cava](https://github.com/karlstav/cava))
- Agent-friendly: `--json` everywhere, `--dry-run`, `agent-context` manifest,
  stable exit codes, errors that enumerate valid values

## Install

Requires Go 1.22+ and Linux.

```sh
git clone https://github.com/NotAProton/ak820-go-drivers
cd ak820
make install    # builds, tests, installs to ~/.local/bin
```

Grant access to the vendor interface (the repo's `99-ajazz-ak820max.rules`):

```sh
sudo cp 99-ajazz-ak820max.rules /etc/udev/rules.d/
sudo udevadm control -R && sudo udevadm trigger
```

## Usage

```sh
ak820 list                       # find the keyboard
ak820 get battery                # percent + connection state
ak820 set mode 5                 # spectral cycle
ak820 set mode 7 --color cyan    # static, cyan
ak820 set key esc=ff0000 space=green   # custom per-key colors
ak820 game                       # whack-a-mole on the real keys
ak820 visualizer --theme ocean   # music reactive
ak820 agent-context              # machine-readable capability manifest
```

Every data command accepts `--json`; mutations accept `--dry-run` and
destructive ones (`reset`, `raw`) require `--yes`. See `ak820 --help`.

## Safety

The config channel only writes lighting and power settings — there is no
firmware-write path in this tool, and none of the vendor flash commands are
implemented. Mutations are explicit, `--dry-run` prints exact frames, and the
keyboard's factory reset requires a double confirmation (`--yes`).

## Documentation

- [PROTOCOL.md](PROTOCOL.md) — the vendor HID protocol specification
- [AGENTS.md](AGENTS.md) — build, test, and contribution conventions

## License

[MIT](LICENSE)
