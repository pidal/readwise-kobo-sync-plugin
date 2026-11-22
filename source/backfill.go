package main

// Optional, explicitly-invoked chapter backfill for highlights uploaded with
// an earlier version of this plugin.  It never changes Kobo's database, never
// deletes or recreates Readwise highlights, and never overwrites note text.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type existingHighlight struct {
	ID           int64  `json:"id"`
	Text         string `json:"text"`
	Note         string `json:"note"`
	LocationType string `json:"location_type"`
}

type backfillReport struct {
	Updated   int
	Already   int
	Unmatched int
	Ambiguous int
	NoChapter int
	TooLong   int
}

func loadBookHighlights(client *http.Client, baseURL, token string, id int64) ([]existingHighlight, error) {
	var all []existingHighlight
	for page := 1; page <= 20; page++ {
		endpoint := fmt.Sprintf("%s/api/v2/highlights/?book_id=%d&page_size=1000&page=%d", baseURL, id, page)
		var response struct {
			Next    string              `json:"next"`
			Results []existingHighlight `json:"results"`
		}
		if err := sendReadwiseJSON(client, http.MethodGet, endpoint, token, nil, &response); err != nil {
			return nil, err
		}
		all = append(all, response.Results...)
		if response.Next == "" {
			return all, nil
		}
		time.Sleep(3100 * time.Millisecond) // Highlight LIST rate limit: 20/min
	}
	return nil, errors.New("too many Readwise highlight pages for one book")
}

func hasChapterPrefix(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Chapter: ") {
			return true
		}
	}
	return false
}

func addChapterToExistingNote(chapter, existing string) string {
	if chapter == "" || hasChapterPrefix(existing) {
		return existing
	}
	return combineChapterNote(chapter, existing)
}

func backfillChapters(dir, dbpath, highlightsURL string, client *http.Client) (string, error) {
	dataDir, err := prepareData(dir)
	if err != nil {
		return "", err
	}
	tokenBytes, err := os.ReadFile(filepath.Join(dataDir, "token.conf"))
	if err != nil {
		return "", errors.New("Readwise token missing")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" || strings.ContainsAny(token, " \t\r\n") || strings.Contains(strings.ToLower(token), "paste") {
		return "", errors.New("invalid Readwise token")
	}
	state, err := loadState(filepath.Join(dataDir, "sync-state.json"))
	if err != nil {
		return "", err
	}
	highlights, err := loadHighlights(dbpath)
	if err != nil {
		return "", err
	}
	if client == nil {
		client, err = newHTTPClient()
		if err != nil {
			return "", err
		}
	}
	u, err := url.Parse(highlightsURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid Readwise API URL")
	}
	baseURL := u.Scheme + "://" + u.Host
	candidates := map[string][]Highlight{}
	for _, h := range highlights {
		if strings.TrimSpace(h.Chapter) == "" {
			continue
		}
		sum := sha256.Sum256([]byte(h.Volume + "\x00" + h.ID + "\x00" + strconv.Itoa(0)))
		if !state.Uploaded[hex.EncodeToString(sum[:])] {
			continue
		} // only previously uploaded by plugin
		k := keyBook(truncateRunes(h.Title, 511), truncateRunes(h.Author, 1024))
		candidates[k] = append(candidates[k], h)
	}
	if len(candidates) == 0 {
		return "No previously uploaded highlights with chapter data need scanning.", nil
	}
	rwBooks, err := loadReadwiseBooks(client, baseURL, token)
	if err != nil {
		return "", err
	}
	index := map[string][]readwiseBook{}
	for _, b := range rwBooks {
		if b.Category == "books" || b.Category == "" {
			k := keyBook(b.Title, b.Author)
			if _, ok := candidates[k]; ok {
				index[k] = append(index[k], b)
			}
		}
	}
	var report backfillReport
	keys := make([]string, 0, len(candidates))
	for k := range candidates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		bs := index[k]
		if len(bs) == 0 {
			report.Unmatched += len(candidates[k])
			continue
		}
		if len(bs) != 1 {
			report.Ambiguous += len(candidates[k])
			continue
		}
		existing, err := loadBookHighlights(client, baseURL, token, bs[0].ID)
		if err != nil {
			return "", fmt.Errorf("fetch existing Readwise highlights: %w", err)
		}
		textMatches := map[string][]existingHighlight{}
		for _, h := range existing {
			textMatches[h.Text] = append(textMatches[h.Text], h)
		}
		localTextCounts := map[string]int{}
		for _, h := range candidates[k] {
			localTextCounts[truncateRunes(h.Text, 8000)]++
		}
		for _, h := range candidates[k] {
			if localTextCounts[truncateRunes(h.Text, 8000)] != 1 {
				report.Ambiguous++
				continue
			}
			matches := textMatches[truncateRunes(h.Text, 8000)]
			// Avoid overwriting an unrelated highlight with identical text.
			// Partial long highlights are matched only by the first chunk.
			if len(matches) == 0 {
				report.Unmatched++
				continue
			}
			if len(matches) != 1 {
				report.Ambiguous++
				continue
			}
			old := matches[0]
			if hasChapterPrefix(old.Note) {
				report.Already++
				continue
			}
			// Do not truncate user-written notes in Readwise just to add metadata.
			if len([]rune("Chapter: "+h.Chapter+"\n\n"+old.Note)) > 8191 {
				report.TooLong++
				continue
			}
			note := addChapterToExistingNote(h.Chapter, old.Note)
			if note == old.Note {
				report.Already++
				continue
			}
			endpoint := fmt.Sprintf("%s/api/v2/highlights/%d/", baseURL, old.ID)
			if err := sendReadwiseJSON(client, http.MethodPatch, endpoint, token, map[string]string{"note": note}, nil); err != nil {
				return "", fmt.Errorf("backfilled %d chapter notes before error: %w", report.Updated, err)
			}
			report.Updated++
			// Standard API base limit is 240 req/min. Keep under that.
			time.Sleep(300 * time.Millisecond)
		}
	}
	return fmt.Sprintf("Chapter backfill: %d updated, %d already tagged, %d unmatched, %d ambiguous, %d too long. Existing Readwise notes were preserved.",
		report.Updated, report.Already, report.Unmatched, report.Ambiguous, report.TooLong), nil
}
