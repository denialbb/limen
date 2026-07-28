package retrieval

import (
	"bytes"
	"testing"
)

// TestIsBinaryClampsToBinaryCheckLen pins the scan window: isBinary inspects
// only the first binaryCheckLen bytes, so a null byte past that window does not
// make the file binary. Without the clamp the last case would flip to true,
// which is what makes this table a real guard on the length arithmetic.
func TestIsBinaryClampsToBinaryCheckLen(t *testing.T) {
	// A null byte just past the scan window, in data longer than the window.
	pastWindow := bytes.Repeat([]byte("a"), binaryCheckLen+64)
	pastWindow[binaryCheckLen+10] = 0

	// A null byte just inside the scan window, same oversized data.
	insideWindow := bytes.Repeat([]byte("a"), binaryCheckLen+64)
	insideWindow[binaryCheckLen-1] = 0

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty data is not binary", nil, false},
		{"short text is not binary", []byte("package main\n"), false},
		{"null inside the first binaryCheckLen bytes is binary", insideWindow, true},
		{"null past binaryCheckLen is not binary (clamped scan)", pastWindow, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBinary(tc.data); got != tc.want {
				t.Fatalf("isBinary(len=%d) = %v, want %v", len(tc.data), got, tc.want)
			}
		})
	}
}
