package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParsePipeLine(t *testing.T) {
	cmd, payload, err := parsePipeLine("0b")
	if err != nil || cmd != 0x0B || len(payload) != 0 {
		t.Fatalf("bare hex cmd: cmd=%02x err=%v", cmd, err)
	}
	if cmd, _, err := parsePipeLine("0x0b"); err != nil || cmd != 0x0B {
		t.Fatalf("prefixed cmd: %02x %v", cmd, err)
	}
	cmd, payload, err = parsePipeLine("07 1704030000")
	if err != nil || cmd != 0x07 || !bytes.Equal(payload, []byte{0x17, 0x04, 0x03, 0x00, 0x00}) {
		t.Fatalf("cmd+payload: cmd=%02x payload=%x err=%v", cmd, payload, err)
	}
	cmd, payload, err = parsePipeLine("f6 1030ff")
	if err != nil || cmd != 0xF6 || !bytes.Equal(payload, []byte{0x10, 0x30, 0xff}) {
		t.Fatalf("letter cmd: cmd=%02x payload=%x err=%v", cmd, payload, err)
	}
	if _, _, err := parsePipeLine("zz"); err == nil {
		t.Fatal("garbage cmd accepted")
	}
	if _, _, err := parsePipeLine("09 00ff"); err != nil {
		t.Fatalf("valid short payload rejected: %v", err)
	}
	overlong := "09 " + strings.Repeat("ff", 70)
	if _, _, err := parsePipeLine(overlong); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestParsePipeLineSpaces(t *testing.T) {
	cmd, payload, err := parsePipeLine("09 aa bb cc")
	if err != nil || cmd != 0x09 || len(payload) != 3 {
		t.Fatalf("spaced payload: cmd=%02x payload=%x err=%v", cmd, payload, err)
	}
	if _, _, err := parsePipeLine(""); err == nil {
		t.Fatal("empty line accepted")
	}
}
