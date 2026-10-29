package checker

import "testing"

func TestDockerNPipePath(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"npipe:////./pipe/docker_engine", `\\.\pipe\docker_engine`},
		{"npipe://./pipe/docker_engine", `\\.\pipe\docker_engine`},
		{`npipe://\\.\pipe\docker_engine`, `\\.\pipe\docker_engine`},
	}
	for _, test := range tests {
		got, err := dockerNPipePath(test.host)
		if err != nil {
			t.Fatalf("dockerNPipePath(%q): %v", test.host, err)
		}
		if got != test.want {
			t.Fatalf("dockerNPipePath(%q) = %q, want %q", test.host, got, test.want)
		}
	}
}

func TestDockerNPipePathRejectsInvalidEndpoints(t *testing.T) {
	for _, host := range []string{
		"npipe://",
		"npipe://pipe/docker_engine",
		"unix:///var/run/docker.sock",
		"npipe:////./not-a-pipe",
	} {
		if _, err := dockerNPipePath(host); err == nil {
			t.Fatalf("dockerNPipePath(%q) unexpectedly succeeded", host)
		}
	}
}
