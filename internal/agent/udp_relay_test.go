package agent

import (
	"net"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/udpframe"
)

func TestUDPRelayBridgesFramedTCPToUDPService(t *testing.T) {
	// Local UDP echo service standing in for the user's datagram service.
	service, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	go func() {
		buffer := make([]byte, udpframe.MaxPayload)
		for {
			length, addr, err := service.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = service.WriteTo(buffer[:length], addr)
		}
	}()
	port := service.LocalAddr().(*net.UDPAddr).Port

	relay, err := newUDPRelay("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// The server side of the tunnel connects over TCP and speaks frames.
	tunnel, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(relay.Port())))
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	_ = tunnel.(*net.TCPConn).SetDeadline(time.Now().Add(2 * time.Second))
	if err := udpframe.Write(tunnel, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, udpframe.MaxPayload)
	length, err := udpframe.Read(tunnel, buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:length]) != "ping" {
		t.Fatalf("echo=%q", buffer[:length])
	}
}

func TestUDPRelayCloseStopsAccepting(t *testing.T) {
	relay, err := newUDPRelay("127.0.0.1", 9)
	if err != nil {
		t.Fatal(err)
	}
	port := relay.Port()
	relay.Close()
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), 200*time.Millisecond); err == nil {
		t.Fatal("closed relay still accepts connections")
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
