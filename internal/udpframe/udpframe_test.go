package udpframe

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	payloads := [][]byte{[]byte(""), []byte("a"), bytes.Repeat([]byte("x"), MaxPayload)}
	for _, payload := range payloads {
		if err := Write(&buffer, payload); err != nil {
			t.Fatalf("write %d bytes: %v", len(payload), err)
		}
	}
	scratch := make([]byte, MaxPayload)
	for _, payload := range payloads {
		length, err := Read(&buffer, scratch)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(scratch[:length], payload) {
			t.Fatalf("payload mismatch: got %d bytes want %d", length, len(payload))
		}
	}
}

func TestWriteRejectsOversizedPayload(t *testing.T) {
	if err := Write(io.Discard, make([]byte, MaxPayload+1)); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("error=%v", err)
	}
}

func TestReadRejectsTruncatedFrame(t *testing.T) {
	if _, err := Read(bytes.NewReader([]byte{0, 5, 'a', 'b'}), make([]byte, MaxPayload)); err == nil {
		t.Fatal("expected error for truncated frame")
	}
}
