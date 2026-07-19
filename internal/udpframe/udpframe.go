// Package udpframe implements the length-prefixed datagram framing used to
// carry UDP payloads across the TCP-only SSH tunnel. Each frame is a 2-byte
// big-endian payload length followed by the payload itself.
package udpframe

import (
	"encoding/binary"
	"errors"
	"io"
)

// MaxPayload is the largest datagram that fits in a frame.
const MaxPayload = 65535

var ErrPayloadTooLarge = errors.New("datagram exceeds maximum frame payload")

// Write frames a single datagram onto w.
func Write(w io.Writer, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrPayloadTooLarge
	}
	header := [2]byte{}
	binary.BigEndian.PutUint16(header[:], uint16(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// Read reads a single framed datagram into buf and returns its length.
// buf must hold at least MaxPayload bytes.
func Read(r io.Reader, buf []byte) (int, error) {
	header := [2]byte{}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, err
	}
	length := int(binary.BigEndian.Uint16(header[:]))
	if length > len(buf) {
		return 0, ErrPayloadTooLarge
	}
	if _, err := io.ReadFull(r, buf[:length]); err != nil {
		return 0, err
	}
	return length, nil
}
