package main

import (
	"strings"
	"unicode/utf8"
)

// NickelMenu's command dialog does not reliably wrap long lines. Hard-wrap
// ourselves, including long unbroken paths and Unicode text.
func wrapStatus(s string, width int) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := ""
		for _, word := range words {
			if current != "" && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > width {
				lines = append(lines, current)
				current = ""
			}
			for utf8.RuneCountInString(word) > width {
				if current != "" {
					lines = append(lines, current)
					current = ""
				}
				runes := []rune(word)
				lines = append(lines, string(runes[:width]))
				word = string(runes[width:])
			}
			if word != "" {
				if current != "" {
					current += " "
				}
				current += word
			}
		}
		if current != "" {
			lines = append(lines, current)
		}
	}
	return strings.Join(lines, "\n")
}

func statusFailure(err error) string {
	text := strings.ToLower(err.Error())
	hint := "Please retry."
	switch {
	case strings.Contains(text, "token") || strings.Contains(text, "401") || strings.Contains(text, "403"):
		hint = "Check your Readwise token."
	case strings.Contains(text, "429"):
		hint = "Readwise is busy. Try later."
	case strings.Contains(text, "tls") || strings.Contains(text, "certificate"):
		hint = "Check Kobo date and Wi-Fi."
	case strings.Contains(text, "network") || strings.Contains(text, "dial tcp") || strings.Contains(text, "timeout") || strings.Contains(text, "no such host"):
		hint = "Check Wi-Fi, then retry."
	case strings.Contains(text, "ambiguous"):
		hint = "Multiple Readwise matches."
	case strings.Contains(text, "state") || strings.Contains(text, "history") || strings.Contains(text, "both "):
		hint = "Check saved sync history."
	case strings.Contains(text, "database") || strings.Contains(text, "sqlite") || strings.Contains(text, "wal"):
		hint = "Kobo database busy or changed.\nWait a moment, then retry."
	}
	return "Sync failed\n" + hint + "\nProgress kept when saved.\nDetails: run.log"
}
