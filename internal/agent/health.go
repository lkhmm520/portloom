package agent

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"
)

type HealthChecker interface {
	Check(context.Context, string, int) error
}
type TCPHealthChecker struct{ Timeout time.Duration }

func (c TCPHealthChecker) Check(ctx context.Context, host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid TCP port %d", port)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("connect to local service: %w", err)
	}
	if err := connection.Close(); err != nil {
		return fmt.Errorf("close local health connection: %w", err)
	}
	return nil
}
