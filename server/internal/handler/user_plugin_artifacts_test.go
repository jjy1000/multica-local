package handler

import (
	"strings"
	"testing"
)

// TestSanitizeArtifactTitle pins the F-006 filename sanitizer: path traversal
// components are stripped to the basename, control characters are removed,
// and unusable inputs degrade to "".
func TestSanitizeArtifactTitle(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain name passes through", "report.md", "report.md"},
		{"path traversal stripped to basename", "../../etc/passwd", "passwd"},
		{"windows separators normalized", "..\\..\\evil.txt", "evil.txt"},
		{"parent dir reference rejected", "..", ""},
		{"dot rejected", ".", ""},
		{"root rejected", "/", ""},
		{"control chars stripped", "a\nb\t.txt", "ab.txt"},
		{"empty input rejected", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeArtifactTitle(c.in); got != c.want {
				t.Fatalf("sanitizeArtifactTitle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeArtifactExt pins the extension whitelist: only a dot + up to 12
// alphanumerics survive; anything that could smuggle path separators or
// traversal into the on-disk name degrades to "".
func TestSanitizeArtifactExt(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain extension passes", ".png", ".png"},
		{"uppercase normalized", ".PNG", ".png"},
		{"multi-char extension rejected", ".tar.gz", ""},
		{"path separator rejected", ".p/ng", ""},
		{"traversal rejected", "./..", ""},
		{"no dot rejected", "png", ""},
		{"control char rejected", ".pn\x00g", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeArtifactExt(c.in); got != c.want {
				t.Fatalf("sanitizeArtifactExt(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeArtifactMime pins the mime whitelist: safe types pass through,
// dangerous/inert types degrade to application/octet-stream.
func TestSanitizeArtifactMime(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"image passes", "image/png", "image/png"},
		{"text passes", "text/plain; charset=utf-8", "text/plain; charset=utf-8"},
		{"json passes", "application/json", "application/json"},
		{"executable degraded", "application/x-msdownload", "application/octet-stream"},
		// text/* passes through — attachments are served with Content-Disposition
		// attachment, so even a shell-script file can never execute inline.
		{"shell script kept as text (attachment-served)", "text/x-shellscript", "text/x-shellscript"},
		{"unknown degraded", "application/x-foo", "application/octet-stream"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeArtifactMime(c.in); got != c.want {
				t.Fatalf("sanitizeArtifactMime(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeArtifactTitle_MaxLen ensures an over-long filename is truncated
// rather than failing the upload.
func TestSanitizeArtifactTitle_MaxLen(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := sanitizeArtifactTitle(long)
	if len(got) != 200 {
		t.Fatalf("truncated title length = %d, want 200", len(got))
	}
}
