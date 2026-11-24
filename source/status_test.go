package main

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatusFitsNarrowDialog(t *testing.T) {
	input := "Sync complete\nNew: 1234567890 Updated: 1234567890\n" + strings.Repeat("文", 80) + "\n" + strings.Repeat("a", 100)
	for _, line := range strings.Split(wrapStatus(input, 32), "\n") {
		if !utf8.ValidString(line) || utf8.RuneCountInString(line) > 32 {
			t.Fatalf("invalid dialog line: %q", line)
		}
	}
}
func TestStatusErrorsAreShortAndActionable(t *testing.T) {
	for _, tc := range []struct{ err, hint string }{
		{"Readwise returned HTTP 401", "token"},
		{"dial tcp: no such host", "Wi-Fi"},
		{"x509: certificate expired", "date"},
		{"invalid SQLite WAL checksum", "retry"},
	} {
		result := statusFailure(errors.New(tc.err + " " + strings.Repeat("sensitive-detail", 100)))
		if !strings.Contains(result, tc.hint) || strings.Contains(result, "sensitive-detail") {
			t.Fatal(result)
		}
		for _, line := range strings.Split(wrapStatus(result, 32), "\n") {
			if utf8.RuneCountInString(line) > 32 {
				t.Fatal(line)
			}
		}
	}
}
