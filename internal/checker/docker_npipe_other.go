//go:build !windows

package checker

import (
	"context"
	"fmt"
	"net"
)

func dockerNPipeDialer(string) (func(context.Context, string, string) (net.Conn, error), error) {
	return nil, fmt.Errorf("npipe Docker endpoints require a Windows build")
}
