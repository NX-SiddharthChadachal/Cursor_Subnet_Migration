// Package preflight validates the inputs before the Controller takes over: it
// confirms each endpoint answers on the Prism API port, reads the product
// versions through the service layer, and picks the service backend for the run.
package preflight

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// tcpDialTimeout bounds the port probe.
const tcpDialTimeout = 10 * time.Second

// CheckTCP reports whether the Prism API port accepts a TCP connection. This is
// the authoritative reachability signal: an endpoint that answers here is usable
// even when ICMP is filtered.
func CheckTCP(ctx context.Context, ep model.Endpoint) error {
	dialer := net.Dialer{Timeout: tcpDialTimeout}
	ctx, cancel := context.WithTimeout(ctx, tcpDialTimeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", ep.Address())
	if err != nil {
		return fmt.Errorf("cannot reach %s on TCP port %d: %w", ep.Host, ep.Port, err)
	}
	return conn.Close()
}

// CheckICMP runs a single best-effort ping. Raw ICMP sockets need privileges on
// most systems, so this shells out to the platform ping binary and treats any
// problem as "unknown" rather than as a failure. The result is informational
// only; CheckTCP decides whether the run proceeds.
func CheckICMP(ctx context.Context, ep model.Endpoint) (reachable bool, known bool) {
	var args []string
	switch runtime.GOOS {
	case "windows":
		args = []string{"-n", "1", "-w", "3000", ep.Host}
	default:
		args = []string{"-c", "1", "-W", "3", ep.Host}
	}

	path, err := exec.LookPath("ping")
	if err != nil {
		return false, false
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, path, args...).Run(); err != nil {
		// A non-zero exit can mean "no reply" or "ICMP not permitted"; either
		// way it is not evidence that the endpoint is down.
		return false, true
	}
	return true, true
}
