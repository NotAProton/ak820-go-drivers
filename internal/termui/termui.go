// Package termui provides minimal terminal control for interactive
// subcommands: raw mode, non-blocking key reads, and screen clearing.
package termui

import (
	"os"
	"syscall"
	"unsafe"
)

// State captures the terminal attributes to restore on exit.
type State struct {
	termios syscall.Termios
	fd      uintptr
}

// IsTerminal reports whether fd is a TTY.
func IsTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return err == 0
}

// MakeRaw puts the terminal in raw mode and returns the previous state.
func MakeRaw(fd uintptr) (*State, error) {
	var old syscall.Termios
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&old)), 0, 0, 0); err != 0 {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCSETS,
		uintptr(unsafe.Pointer(&raw)), 0, 0, 0); err != 0 {
		return nil, err
	}
	return &State{termios: old, fd: fd}, nil
}

// Restore returns the terminal to its saved state.
func (s *State) Restore() {
	if s == nil {
		return
	}
	syscall.Syscall6(syscall.SYS_IOCTL, s.fd, syscall.TCSETS,
		uintptr(unsafe.Pointer(&s.termios)), 0, 0, 0)
}

// KeyReader delivers keypresses from stdin on a channel.
type KeyReader struct {
	keys chan rune
	stop chan struct{}
}

// NewKeyReader spawns a goroutine that reads single bytes from stdin.
func NewKeyReader() *KeyReader {
	kr := &KeyReader{keys: make(chan rune, 16), stop: make(chan struct{})}
	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-kr.stop:
				return
			default:
			}
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			select {
			case kr.keys <- rune(buf[0]):
			case <-kr.stop:
				return
			}
		}
	}()
	return kr
}

// Keys returns the key channel.
func (kr *KeyReader) Keys() <-chan rune { return kr.keys }

// Close stops the reader.
func (kr *KeyReader) Close() { close(kr.stop) }
