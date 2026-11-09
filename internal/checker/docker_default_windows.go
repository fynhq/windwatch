//go:build windows

package checker

// Windows is not an official standalone target. Docker checks require an
// explicit DOCKER_HOST (for example tcp://host:2375) on this platform.
const defaultDockerHost = ""
