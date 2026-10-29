//go:build windows

package checker

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func dockerNPipeDialer(host string) (func(context.Context, string, string) (net.Conn, error), error) {
	path, err := dockerNPipePath(host)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, path)
	}, nil
}
