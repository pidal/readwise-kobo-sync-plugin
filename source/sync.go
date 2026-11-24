package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type syncRecord struct {
	LocationType string `json:"readwise_location_type,omitempty"`
	ID           int64  `json:"readwise_highlight_id,omitempty"`
	BookID       int64  `json:"readwise_book_id,omitempty"`
	// Persist upload intent before the network request. If interrupted, reconcile
	// this exact text before considering a new POST, even if Kobo changed since.
	Payload ReadwiseHighlight `json:"payload"`
}

func apiBase(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("invalid Readwise URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

type reconciler struct {
	client      *http.Client
	base, token string
	books       []readwiseBook
	loaded      bool
	highlights  map[int64][]existingHighlight
}

var errAmbiguousMatch = errors.New("multiple Readwise matches")

// Matching never modifies Readwise. Even when books share a title/author,
// an exact passage may identify just one existing highlight across them.
func (r *reconciler) find(v ReadwiseHighlight) (syncRecord, error) {
	if !r.loaded {
		books, err := loadReadwiseBooks(r.client, r.base, r.token)
		if err != nil {
			return syncRecord{}, err
		}
		r.books = books
		r.loaded = true
	}
	var candidates []readwiseBook
	for _, b := range r.books {
		if (b.Category == "" || b.Category == "books") && keyBook(b.Title, b.Author) == keyBook(v.Title, v.Author) {
			candidates = append(candidates, b)
		}
	}
	var matches []syncRecord
	for _, book := range candidates {
		highlights, ok := r.highlights[book.ID]
		if !ok {
			var err error
			highlights, err = loadBookHighlights(r.client, r.base, r.token, book.ID)
			if err != nil {
				return syncRecord{}, err
			}
			r.highlights[book.ID] = highlights
		}
		for _, h := range highlights {
			if h.Text == v.Text {
				matches = append(matches, syncRecord{ID: h.ID, BookID: book.ID, LocationType: h.LocationType})
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 || len(candidates) > 1 {
		return syncRecord{}, errAmbiguousMatch
	}
	return syncRecord{}, nil
}

func sync(dir, dbpath, endpoint string, client *http.Client) (string, error) {
	dataDir, err := prepareData(dir)
	if err != nil {
		return "", err
	}
	tokenBytes, err := os.ReadFile(filepath.Join(dataDir, "token.conf"))
	if err != nil {
		return "", errors.New("Readwise token missing: create .adds/readwise-sync/data/token.conf")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" || strings.Contains(strings.ToLower(token), "paste") || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("token.conf is empty or invalid")
	}
	statePath := filepath.Join(dataDir, "sync-state.json")
	state, err := loadState(statePath)
	if err != nil {
		return "", err
	}
	if state.Records == nil {
		state.Records = map[string]syncRecord{}
	}
	items, err := loadHighlights(dbpath)
	if err != nil {
		return "", err
	}
	return syncItems(statePath, dbpath, endpoint, token, state, items, client, func(message string) { writeStatus(dir, message) })
}

func syncItems(statePath, dbpath, endpoint, token string, state stateFile, items []Highlight, client *http.Client, report func(string)) (string, error) {
	if state.Records == nil {
		state.Records = map[string]syncRecord{}
	}
	all := makePending(items)
	base, err := apiBase(endpoint)
	if err != nil {
		return "", err
	}
	// Snapshot every part of a new bookmark before its first request. Once a
	// bookmark is tracked, editing it cannot introduce extra "new" parts.
	pending := []pendingItem{}
	skipped := 0
	for _, h := range items {
		parts := makePending([]Highlight{h})
		if len(parts) == 0 {
			continue
		}
		_, tracked := state.Records[parts[0].key]
		for _, p := range parts {
			rec, known := state.Records[p.key]
			if rec.ID > 0 || (tracked && !known) {
				skipped++
				continue
			}
			if !known {
				state.Records[p.key] = syncRecord{Payload: p.value}
			}
			pending = append(pending, p)
		}
	}
	created, matched, unresolved := 0, 0, 0
	summary := func() string {
		return fmt.Sprintf("Sync complete\nNew: %d\nAlready synced: %d\nNeeds review: %d", created, skipped+matched, unresolved)
	}
	persist := func() error { return saveState(statePath, state) }
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("saved %d new highlights before error: %w", created, err)
	}
	review := []string{}
	saveReview := func() error {
		return atomicWrite(filepath.Join(filepath.Dir(statePath), "sync-review.log"), []byte(strings.Join(review, "\n")))
	}
	if len(pending) == 0 {
		if err := saveReview(); err != nil {
			return fail(err)
		}
		return summary(), nil
	}
	if client == nil {
		client, err = newHTTPClient()
		if err != nil {
			return "", err
		}
	}
	r := reconciler{client: client, base: base, token: token, highlights: map[int64][]existingHighlight{}}
	// Protect distinct Kobo annotations that Readwise would collapse into one.
	counts := map[string]int{}
	for _, p := range all {
		counts[keyBook(p.value.Title, p.value.Author)+"\x00"+p.value.Text]++
	}
	lastProgress := time.Time{}
	newItems := []Highlight{}
	newBookKeys := map[string]bool{}
	for index, p := range pending {
		if report != nil && time.Since(lastProgress) >= time.Second {
			report(fmt.Sprintf("Syncing new highlights...\nChecked: %d / %d\nUploaded: %d\nAlready synced: %d", index, len(pending), created, skipped+matched))
			lastProgress = time.Now()
		}
		if counts[keyBook(p.value.Title, p.value.Author)+"\x00"+p.value.Text] > 1 {
			unresolved++
			review = append(review, p.key+": identical local passages; not uploaded")
			continue
		}
		rec, known := state.Records[p.key]
		target := p.value
		if known {
			target = rec.Payload
		}
		match, err := r.find(target)
		if errors.Is(err, errAmbiguousMatch) {
			unresolved++
			review = append(review, p.key+": multiple Readwise matches; not uploaded")
			continue
		}
		if err != nil {
			return fail(err)
		}
		if match.ID > 0 {
			// Existing remote text, notes and locations are left exactly as they are.
			match.Payload = target
			state.Records[p.key] = match
			if err := persist(); err != nil {
				return fail(err)
			}
			matched++
			continue
		}
		// Save the exact creation payload before sending. Recovery matches this
		// payload if Kobo changes while an uncertain request is being resolved.
		if known {
			p.value = target
		}
		rec.Payload = p.value
		state.Records[p.key] = rec
		if err := persist(); err != nil {
			return fail(err)
		}
		var response []struct {
			ID       int64   `json:"id"`
			Modified []int64 `json:"modified_highlights"`
		}
		payload := struct {
			Highlights []ReadwiseHighlight `json:"highlights"`
		}{[]ReadwiseHighlight{p.value}}
		if err := sendReadwiseJSON(client, http.MethodPost, endpoint, token, payload, &response); err != nil {
			return fail(err)
		}
		var ids []int64
		for _, book := range response {
			for _, id := range book.Modified {
				if id > 0 {
					ids = append(ids, id)
					rec.BookID = book.ID
				}
			}
		}
		if len(ids) == 1 {
			rec.ID = ids[0]
			rec.LocationType = "order"
		} else {
			r.loaded = false
			r.highlights = map[int64][]existingHighlight{}
			match, err := r.find(p.value)
			if errors.Is(err, errAmbiguousMatch) {
				unresolved++
				review = append(review, p.key+": upload accepted but ID ambiguous; left pending for review")
				continue
			}
			if err != nil {
				return fail(err)
			}
			if match.ID == 0 {
				return fail(errors.New("upload accepted but ID not verified; retry to reconcile"))
			}
			rec.ID = match.ID
			rec.BookID = match.BookID
			rec.LocationType = match.LocationType
		}
		state.Records[p.key] = rec
		if err := persist(); err != nil {
			return fail(err)
		}
		created++
		newBookKeys[keyBook(p.value.Title, p.value.Author)] = true
		bookKnown := false
		for _, book := range r.books {
			if book.ID == rec.BookID {
				bookKnown = true
			}
		}
		if !bookKnown {
			r.books = append(r.books, readwiseBook{ID: rec.BookID, Title: p.value.Title, Author: p.value.Author, Category: "books"})
		}
		r.highlights[rec.BookID] = append(r.highlights[rec.BookID], existingHighlight{ID: rec.ID, Text: p.value.Text, Note: p.value.Note})
	}
	if err := saveReview(); err != nil {
		return fail(err)
	}
	// Covers are optional and only checked for newly uploaded books. A cover
	// failure must not turn successfully saved highlights into "Sync failed".
	for _, h := range items {
		if newBookKeys[keyBook(truncateRunes(h.Title, 511), truncateRunes(h.Author, 1024))] {
			newItems = append(newItems, h)
		}
	}
	if len(newItems) > 0 {
		if report != nil {
			report("Highlights saved.\nChecking new book covers.")
		}
		covers, err := syncCovers(client, dbpath, endpoint, token, newItems)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cover warning (highlights saved): %v\n", err)
			return summary() + "\nCovers skipped; see run.log", nil
		}
		return summary() + fmt.Sprintf("\nCovers added: %d", covers.uploaded), nil
	}
	return summary(), nil
}
