package protocol

import (
	"bytes"
	"testing"
)

func TestFrame(t *testing.T) {
	f := Frame(0x07, []byte{6, 4, 3, 0, 0})
	if len(f) != 64 {
		t.Fatalf("frame len = %d, want 64", len(f))
	}
	if !bytes.Equal(f[:4], Magic) {
		t.Fatalf("magic = %x, want aa55cc33", f[:4])
	}
	if f[4] != 0x07 {
		t.Fatalf("cmd = %02x", f[4])
	}
	if f[5] != 6 || f[6] != 4 || f[7] != 3 || f[8] != 0 || f[9] != 0 {
		t.Fatalf("payload = %x", f[5:10])
	}
	for i, b := range f[10:] {
		if b != 0 {
			t.Fatalf("padding nonzero at %d: %02x", i+10, b)
		}
	}
}

func TestBatteryReply(t *testing.T) {
	reply := append([]byte{}, Magic...)
	reply = append(reply, 0x0E, 0x57, 0x03)
	pct, conn, ok := BatteryReply(reply)
	if !ok || pct != 0x57 || conn != 0x03 {
		t.Fatalf("pct=%d conn=%02x ok=%v", pct, conn, ok)
	}
	// prefixed variant must also parse (defensive against RID-prefixed RX)
	prefixed := append([]byte{0x00}, reply...)
	if p, c, ok := BatteryReply(prefixed); !ok || p != 0x57 || c != 0x03 {
		t.Fatalf("prefixed form: %d %02x %v", p, c, ok)
	}
	if _, _, ok := BatteryReply([]byte{1, 2, 3}); ok {
		t.Fatal("garbage parsed as battery")
	}
	bad := append(append([]byte{}, Magic...), 0x0E, 0xFF, 0x03)
	if _, _, ok := BatteryReply(bad); ok {
		t.Fatal(">100 percent accepted")
	}
}

func TestParseHex(t *testing.T) {
	b, err := ParseHex("AA55 cc 33")
	if err != nil || len(b) != 4 {
		t.Fatalf("ParseHex: %v %x", err, b)
	}
	if _, err := ParseHex("abc"); err == nil {
		t.Fatal("odd-length hex accepted")
	}
}

func TestParseColor(t *testing.T) {
	r, g, b, err := ParseColor("ff8800")
	if err != nil || r != 0xFF || g != 0x88 || b != 0x00 {
		t.Fatalf("color: %v %02x%02x%02x", err, r, g, b)
	}
}

func TestParseU32(t *testing.T) {
	if v, _ := ParseU32("0x0e"); v != 14 {
		t.Fatalf("hex u32: %d", v)
	}
	if v, _ := ParseU32("0x10"); v != 16 { // hex even without letters — regression guard
		t.Fatalf("hex u32 no-letter: %d", v)
	}
	if v, _ := ParseU32("14"); v != 14 {
		t.Fatalf("dec u32: %d", v)
	}
	if _, err := ParseU32("zz"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestBuildModeFrames(t *testing.T) {
	fs, desc, err := BuildModeFrames([]string{"7", "--brightness", "3", "--speed", "2"})
	if err != nil {
		t.Fatalf("BuildModeFrames: %v", err)
	}
	if len(fs) != 2 { // mode frame + apply
		t.Fatalf("frames = %d, want 2", len(fs))
	}
	if fs[0][5] != 6 || fs[0][6] != 2 || fs[0][7] != 1 {
		t.Fatalf("mode payload wrong: %x", fs[0][5:10])
	}
	if desc["mode"] != 7 || desc["brightness"] != 3 || desc["speed"] != 2 {
		t.Fatalf("desc wrong: %v", desc)
	}
	// invalid mode enumerates valid values
	_, _, err = BuildModeFrames([]string{"99"})
	if err == nil {
		t.Fatal("invalid mode accepted")
	}
	// custom mode adds init frame
	fs, _, err = BuildModeFrames([]string{"24", "--no-apply"})
	if err != nil {
		t.Fatalf("custom mode: %v", err)
	}
	if len(fs) != 2 || fs[1][4] != CmdCustomModeInit {
		t.Fatalf("custom mode frames: %x", fs)
	}
}

func TestVendorDescriptor(t *testing.T) {
	if !IsVendorDescriptor([]byte{0x06, 0x02, 0xFF, 0x00}) {
		t.Fatal("vendor descriptor not detected")
	}
	if IsVendorDescriptor([]byte{0x05, 0x01, 0x09}) {
		t.Fatal("keyboard descriptor flagged as vendor")
	}
}

func TestParseU32Empty(t *testing.T) {
	if _, err := ParseU32(""); err == nil {
		t.Fatal("empty string accepted as 0")
	}
}

func TestMode23Alias(t *testing.T) {
	if !ValidMode(23) {
		t.Fatal("mode 23 must be accepted as custom alias")
	}
	fs23, _, err := BuildModeFrames([]string{"23", "--no-apply"})
	if err != nil {
		t.Fatalf("mode 23: %v", err)
	}
	fs24, _, err := BuildModeFrames([]string{"24", "--no-apply"})
	if err != nil {
		t.Fatalf("mode 24: %v", err)
	}
	if len(fs23) != len(fs24) || string(fs23[0]) != string(fs24[0]) {
		t.Fatalf("mode 23 wire frame %x must equal mode 24 %x", fs23[0], fs24[0])
	}
}

func TestColorBytesRainbowCase(t *testing.T) {
	for _, s := range []string{"rainbow", "Rainbow", "RAINBOW"} {
		_, err := ColorBytes(s)
		if err == nil {
			t.Fatalf("%q accepted as per-key color", s)
		}
	}
}

func TestBuildCustomModeFrames(t *testing.T) {
	var slots [][3]byte = make([][3]byte, SlotCount)
	fs := BuildCustomModeFrames(slots, 4, 3)
	if len(fs) != 3+PerKeyFrames {
		t.Fatalf("custom frames = %d, want %d", len(fs), 3+PerKeyFrames)
	}
	if fs[0][4] != CmdSetLightMode || fs[1][4] != CmdCustomModeInit || fs[2][4] != CmdApply {
		t.Fatalf("custom prefix wrong: %02x %02x %02x", fs[0][4], fs[1][4], fs[2][4])
	}
	for i := 0; i < PerKeyFrames; i++ {
		if fs[3+i][4] != byte(CmdPerKeyCustom0+i) {
			t.Fatalf("per-key frame %d cmd = %02x", i, fs[3+i][4])
		}
	}
}

func TestGeometryConstants(t *testing.T) {
	if PerKeyFrames != 6 || KeysPerFrame != 18 || PerKeyFrameLen != 54 {
		t.Fatalf("geometry constants wrong: %d %d %d", PerKeyFrames, KeysPerFrame, PerKeyFrameLen)
	}
}
