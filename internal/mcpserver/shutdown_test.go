package mcpserver

import (
	"fmt"
	"io"
	"testing"
)

// errClosing stands in for the go-sdk's internal jsonrpc2.ErrServerClosing,
// which is not importable. The benign end-of-session error is composed exactly
// like the SDK does it: the closing sentinel via %w, the io.EOF cause via %v.
var errClosing = fmt.Errorf("server is closing")

func TestGracefulClose(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"raw io.EOF", io.EOF, true},
		{"sdk closing on EOF", fmt.Errorf("%w: %v", errClosing, io.EOF), true},
		{"malformed input is not swallowed", fmt.Errorf("invalid character 'o' in literal null"), false},
		{"closing on a write error is not an EOF", fmt.Errorf("%w: %v", errClosing, io.ErrClosedPipe), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gracefulClose(tc.err); got != tc.want {
				t.Fatalf("gracefulClose(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
