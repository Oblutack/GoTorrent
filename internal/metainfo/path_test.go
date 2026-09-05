package metainfo

import (
	"strings"
	"testing"
)

func TestValidatePathSegment(t *testing.T) {
	tests := []struct {
		name    string
		seg     string
		wantErr bool
	}{
		// Ordinary names that must keep working.
		{name: "plain file", seg: "ubuntu-25.04-desktop-amd64.iso"},
		{name: "directory", seg: "Season 1"},
		{name: "leading dot", seg: ".hidden"},
		{name: "unicode", seg: "запись.mkv"},
		{name: "dashes and brackets", seg: "[GRP] Show - 01 (1080p).mkv"},
		{name: "device name with more text", seg: "CONTENTS.txt"},
		{name: "device name as a suffix", seg: "MYCOM1"},
		{name: "at the length limit", seg: strings.Repeat("a", 255)},

		// Traversal.
		{name: "empty", seg: "", wantErr: true},
		{name: "dot", seg: ".", wantErr: true},
		{name: "dotdot", seg: "..", wantErr: true},

		// Separators smuggled inside a single segment.
		{name: "forward slash", seg: "a/b", wantErr: true},
		{name: "backslash", seg: `a\b`, wantErr: true},
		{name: "traversal with slash", seg: "../etc", wantErr: true},
		{name: "unix absolute path", seg: "/etc/passwd", wantErr: true},
		{name: "windows unc", seg: `\\server\share`, wantErr: true},

		// Drive letters and NTFS alternate data streams.
		{name: "drive letter", seg: "C:", wantErr: true},
		{name: "drive letter with path", seg: `C:\Windows\System32`, wantErr: true},
		{name: "alternate data stream", seg: "file:stream", wantErr: true},

		// Control characters.
		{name: "NUL byte", seg: "evil\x00.txt", wantErr: true},
		{name: "newline", seg: "evil\n.txt", wantErr: true},
		{name: "DEL", seg: "evil\x7f.txt", wantErr: true},

		// Windows quirks.
		{name: "trailing dot", seg: "evil.", wantErr: true},
		{name: "trailing space", seg: "evil ", wantErr: true},
		{name: "reserved CON", seg: "CON", wantErr: true},
		{name: "reserved lowercase nul", seg: "nul", wantErr: true},
		{name: "reserved with extension", seg: "LPT1.txt", wantErr: true},
		{name: "reserved mixed case", seg: "CoM9.dat", wantErr: true},

		// Length.
		{name: "over the length limit", seg: strings.Repeat("a", 256), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathSegment(tt.seg)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidatePathSegment(%q) = nil, want an error", tt.seg)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidatePathSegment(%q) = %v, want nil", tt.seg, err)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
	}{
		{name: "single file", parts: []string{"readme.txt"}},
		{name: "nested", parts: []string{"Season 1", "Episode 1.mkv"}},

		{name: "no segments", parts: nil, wantErr: true},
		{name: "traversal in the middle", parts: []string{"a", "..", "..", "b"}, wantErr: true},
		{name: "traversal at the front", parts: []string{"..", "..", "Windows", "System32", "evil.dll"}, wantErr: true},
		{name: "empty segment", parts: []string{"a", "", "b"}, wantErr: true},
		{name: "separator smuggled in", parts: []string{"a", "../../b"}, wantErr: true},
		{name: "too deep", parts: make([]string, maxPathDepth+1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.parts)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidatePath(%q) = nil, want an error", tt.parts)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidatePath(%q) = %v, want nil", tt.parts, err)
			}
		})
	}
}
