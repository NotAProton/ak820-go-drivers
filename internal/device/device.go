package device

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/NotAProton/ak820-go-drivers/internal/protocol"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Node describes one AK820 hidraw node found on the system.
type Node struct {
	Path       string // /dev/hidrawN
	Bus        string // "usb", "bluetooth", "unknown"
	Interface  string // bInterfaceNumber, "" if unavailable
	Vendor     uint32
	Product    uint32
	ConfigChan bool // descriptor is the 0xFF02 vendor in/out channel
	Err        string
}

var hidIDRe = regexp.MustCompile(`HID_ID=(\d+):([0-9A-Fa-f]{8}):([0-9A-Fa-f]{8})`)

func sysFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// DiscoverNodes scans /sys/class/hidraw for AK820 nodes.
func DiscoverNodes() []Node {
	entries, err := filepath.Glob("/sys/class/hidraw/hidraw*")
	if err != nil {
		return nil
	}
	var out []Node
	for _, d := range entries {
		dev := filepath.Join(d, "device")
		uev, err := os.ReadFile(filepath.Join(dev, "uevent"))
		if err != nil {
			continue
		}
		m := hidIDRe.FindSubmatch(uev)
		if m == nil {
			continue
		}
		bus, _ := strconv.Atoi(string(m[1]))
		vid, _ := strconv.ParseUint(string(m[2]), 16, 32)
		pid, _ := strconv.ParseUint(string(m[3]), 16, 32)
		isWired := bus == 3 && vid == protocol.VIDWired && pid == protocol.PIDWired
		is24G := bus == 3 && vid == protocol.VID24G && pid == protocol.PID24G
		isBT := bus == 5 && vid == protocol.VIDWired && pid == protocol.PIDWired
		if !isWired && !is24G && !isBT {
			continue
		}
		n := Node{
			Path:    "/dev/" + filepath.Base(d),
			Vendor:  uint32(vid),
			Product: uint32(pid),
		}
		// bInterfaceNumber lives in the parent USB interface dir; resolve
		// the HID device symlink first.
		if real, rerr := filepath.EvalSymlinks(dev); rerr == nil {
			n.Interface = sysFirstLine(filepath.Join(filepath.Dir(real), "bInterfaceNumber"))
		}
		switch {
		case isWired:
			n.Bus = "usb"
		case is24G:
			n.Bus = "2.4g-dongle"
		default:
			n.Bus = "bluetooth"
		}
		if n.Bus == "bluetooth" {
			n.Err = "config channel not available over bluetooth"
		} else {
			desc, derr := os.ReadFile(filepath.Join(dev, "report_descriptor"))
			if derr != nil {
				n.Err = "cannot read report_descriptor: " + derr.Error()
			} else {
				n.ConfigChan = protocol.IsVendorDescriptor(desc)
				if !n.ConfigChan {
					n.Err = "auxiliary HID interface (no vendor in/out channel)"
				}
			}
		}
		out = append(out, n)
	}
	return out
}

// PickDevice returns the config-capable node, honoring an explicit path and
// the AK820_DEVICE environment variable (precedence: flag > env > default).
func PickDevice(flagDevice string) (Node, error) {
	if flagDevice != "" {
		if _, err := os.Stat(flagDevice); err != nil {
			return Node{}, fmt.Errorf("device %s: %w", flagDevice, err)
		}
		return Node{Path: flagDevice, ConfigChan: true, Bus: "manual"}, nil
	}
	if env := os.Getenv("AK820_DEVICE"); env != "" {
		if _, err := os.Stat(env); err != nil {
			return Node{}, fmt.Errorf("device %s (from AK820_DEVICE): %w", env, err)
		}
		return Node{Path: env, ConfigChan: true, Bus: "manual"}, nil
	}
	nodes := DiscoverNodes()
	var cands []Node
	for _, n := range nodes {
		if n.ConfigChan {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		return Node{}, &DeviceNotFoundError{Nodes: nodes}
	}
	return cands[0], nil
}

// DeviceNotFoundError reports a missing config channel with remediation.
type DeviceNotFoundError struct{ Nodes []Node }

func (e *DeviceNotFoundError) Error() string {
	var b strings.Builder
	b.WriteString("no AK820 node with a config channel found")
	if len(e.Nodes) > 0 {
		b.WriteString("; found: ")
		for i, n := range e.Nodes {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%s", n.Path, n.Bus)
			if n.Err != "" {
				fmt.Fprintf(&b, ": %s", n.Err)
			}
			b.WriteString(")")
		}
	}
	b.WriteString(" — plug the keyboard in via USB (or use the 2.4G dongle)")
	return b.String()
}

// ErrDevice marks device-level failures (exit code 2).
var ErrDevice = errors.New("device error")

// Device is an open vendor-channel hidraw node.
type Device struct {
	f      *os.File
	Path   string
	Quiet  bool
	Writer func(s string) // log sink (stderr); nil = discard
}

// OpenDevice opens a hidraw node for the vendor channel.
func OpenDevice(n Node, quiet bool, logf func(string)) (*Device, error) {
	f, err := os.OpenFile(n.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %v", ErrDevice, n.Path, err)
	}
	return &Device{f: f, Path: n.Path, Quiet: quiet, Writer: logf}, nil
}

func (d *Device) log(s string) {
	if d.Writer != nil {
		d.Writer(s)
	}
}

func (d *Device) hex(b []byte) string {
	return hex.EncodeToString(b)
}

// Send writes a command frame and returns the reply bytes (may be empty).
// timeout bounds the read wait; 0 means fire-and-forget.
func (d *Device) Send(cmd byte, payload []byte, timeout time.Duration) ([]byte, error) {
	frame := protocol.Frame(cmd, payload)
	d.log("TX " + d.hex(frame))
	d.f.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := d.f.Write(frame); err != nil {
		return nil, fmt.Errorf("%w: write: %v", ErrDevice, err)
	}
	if timeout <= 0 {
		return nil, nil
	}
	if err := d.f.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDevice, err)
	}
	buf := make([]byte, 512)
	total := make([]byte, 0, 64)
	for {
		n, err := d.f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			d.log("RX " + d.hex(chunk))
			total = append(total, chunk...)
			// A battery-sized reply is complete; config dumps may chain.
			if bytes.Contains(total, protocol.Magic) && len(total) >= 8 {
				break
			}
		}
		if err != nil {
			if os.IsTimeout(err) {
				break
			}
			return total, nil // EAGAIN-ish after deadline; treat as end
		}
		if len(total) >= 64*8 { // bounded
			break
		}
	}
	return total, nil
}

// Close releases the node.
func (d *Device) Close() { _ = d.f.Close() }
