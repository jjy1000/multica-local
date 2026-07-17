package handler

import "testing"

// TestIsLoopbackUpstreamURL pins the SSRF guard on the experimental
// upstream-registration endpoint: only http(s) URLs targeting a
// loopback host may be registered as a reverse-proxy upstream. Any
// external host must be rejected so the same-origin proxy can never
// be turned into an open proxy.
func TestIsLoopbackUpstreamURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"ipv4 loopback with port", "http://127.0.0.1:33333", true},
		{"ipv4 loopback subnet", "http://127.0.0.5:8080/health", true},
		{"ipv6 loopback", "http://[::1]:9000", true},
		{"localhost hostname", "http://localhost:8088", true},
		{"https loopback", "https://127.0.0.1:8443", true},
		{"external host", "http://attacker.example/leak", false},
		{"external ip", "http://10.0.0.1:80", false},
		{"public ip", "http://93.184.216.34", false},
		{"metadata endpoint", "http://169.254.169.254/latest/meta-data", false},
		{"non-http scheme", "file:///etc/passwd", false},
		{"gopher scheme", "gopher://127.0.0.1:70", false},
		{"empty string", "", false},
		{"garbage", "not a url at all ::::", false},
		{"host-only no scheme", "127.0.0.1:8090", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLoopbackUpstreamURL(tt.url); got != tt.want {
				t.Errorf("isLoopbackUpstreamURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
