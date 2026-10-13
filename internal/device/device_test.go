package device

import (
	"testing"

	"github.com/NotAProton/ak820-go-drivers/internal/protocol"
)

func TestContainsReplyMatchesCmd(t *testing.T) {
	tx := protocol.Frame(0x0E, nil)
	if !containsReply(tx, 0x0E) {
		t.Fatal("own frame not detected")
	}
	if containsReply(tx, 0x0F) {
		t.Fatal("wrong CMD matched")
	}
	if containsReply([]byte{1, 2, 3}, 0x0E) {
		t.Fatal("garbage matched")
	}
}

func TestExplicitNodeRejectsUnknown(t *testing.T) {
	if _, err := PickDevice("/dev/null"); err == nil {
		t.Fatal("/dev/null accepted as keyboard")
	}
	if _, err := PickDevice("/nonexistent-hidraw-xyz"); err == nil {
		t.Fatal("missing path accepted")
	} else {
		if _, ok := err.(*DeviceNotFoundError); !ok {
			t.Fatalf("missing path must be DeviceNotFoundError (exit 3), got %T: %v", err, err)
		}
	}
}

func TestDiscoverSorted(t *testing.T) {
	nodes := DiscoverNodes()
	for i := 1; i < len(nodes); i++ {
		if nodes[i-1].Path > nodes[i].Path {
			t.Fatal("DiscoverNodes not sorted")
		}
	}
}
