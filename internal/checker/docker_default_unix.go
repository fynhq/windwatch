//go:build !windows

package checker

const defaultDockerHost = "unix:///var/run/docker.sock"
