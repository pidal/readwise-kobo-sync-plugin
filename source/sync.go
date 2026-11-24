package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Fingerprint  string `json:"fingerprint,omitempty"`
	// Persist upload intent before the network request. If interrupted, reconcile
	// this exact text before considering a new POST, even if Kobo changed since.
	Payload ReadwiseHighlight `json:"payload"`
}

func fingerprint(v ReadwiseHighlight) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
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

// Match only unique title/author AND unique text. No positional assumption
// about modified_highlights arrays in a multi-highlight CREATE response.
func (r *reconciler) find(v ReadwiseHighlight) (syncRecord, error) {
	if !r.loaded {
		bs, err := loadReadwiseBooks(r.client, r.base, r.token)
		if err != nil {
			return syncRecord{}, err
		}
		r.books = bs
		r.loaded = true
	}
	var bs []readwiseBook
	for _, b := range r.books {
		if (b.Category == "" || b.Category == "books") && keyBook(b.Title, b.Author) == keyBook(v.Title, v.Author) {
			bs = append(bs, b)
		}
	}
	if len(bs) > 1 {
		return syncRecord{}, errors.New("ambiguous Readwise book; no highlights changed")
	}
	if len(bs) == 0 {
		return syncRecord{}, nil
	}
	b := bs[0]
	hs, ok := r.highlights[b.ID]
	if !ok {
		var err error
		hs, err = loadBookHighlights(r.client, r.base, r.token, b.ID)
		if err != nil {
			return syncRecord{}, err
		}
		r.highlights[b.ID] = hs
	}
	var found []existingHighlight
	for _, h := range hs {
		if h.Text == v.Text {
			found = append(found, h)
		}
	}
	if len(found) > 1 {
		return syncRecord{}, errors.New("ambiguous Readwise highlight; no match selected")
	}
	if len(found) == 0 {
		return syncRecord{}, nil
	}
	return syncRecord{ID: found[0].ID, BookID: b.ID, LocationType: found[0].LocationType}, nil
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
	var err error
	if state.Records == nil {
		state.Records = map[string]syncRecord{}
	}
	// Include every current item: positions can change when an earlier highlight
	// is added. Stored IDs allow those positions to be updated in Readwise.
	all := makePending(items, map[string]bool{})
	if client == nil {
		client, err = newHTTPClient()
		if err != nil {
			return "", err
		}
	}
	base, err := apiBase(endpoint)
	if err != nil {
		return "", err
	}
	r := reconciler{client: client, base: base, token: token, highlights: map[int64][]existingHighlight{}}
	created, updated, unchanged, unresolved := 0, 0, 0, 0
	persist := func() error {
		if state.Version == 1 {
			if old, e := os.ReadFile(statePath); e == nil {
				backup := statePath + ".v1.bak"
				if _, e := os.Stat(backup); os.IsNotExist(e) {
					if e = atomicWrite(backup, old); e != nil {
						return e
					}
				}
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		state.Version = 2
		return saveState(statePath, state)
	}
	fail := func(err error) (string, error) {
		return "", fmt.Errorf("synced %d new, %d updated before error: %w", created, updated, err)
	}
	// Fail before network mutations for duplicate local text that Readwise would
	// collapse into one object. Cannot reliably own two different Kobo notes.
	review := []string{}
	counts := map[string]int{}
	currentKeys := map[string]bool{}
	for _, p := range all {
		counts[keyBook(p.value.Title, p.value.Author)+"\x00"+p.value.Text]++
		currentKeys[p.key] = true
	}
	lastProgress := time.Time{}
	for index, p := range all {
		if report != nil && time.Since(lastProgress) >= time.Second {
			report(fmt.Sprintf("Syncing...\nHighlights: %d / %d\nNew: %d  Updated: %d\nCheck status again shortly.", index, len(all), created, updated))
			lastProgress = time.Now()
		}
		if counts[keyBook(p.value.Title, p.value.Author)+"\x00"+p.value.Text] > 1 {
			unresolved++
			review = append(review, p.key+": identical local text has multiple owners")
			continue
		}
		rec, known := state.Records[p.key]
		fp := fingerprint(p.value)
		if known && rec.ID > 0 && rec.Fingerprint == fp {
			unchanged++
			continue
		}
		if rec.ID == 0 {
			target := p.value
			if known {
				target = rec.Payload
			}
			// Reconcile legacy entries and interrupted uploads. New items are also
			// checked to avoid duplicating an existing manual/official import.
			match, e := r.find(target)
			if e != nil {
				return fail(e)
			}
			if match.ID > 0 {
				rec.ID = match.ID
				rec.BookID = match.BookID
				rec.LocationType = match.LocationType
				state.Records[p.key] = rec
				if err := persist(); err != nil {
					return fail(err)
				}
			} else if state.Uploaded[p.key] {
				// Legacy history has no original text to match an edited Kobo passage.
				// Keep its uploaded flag and require user review rather than duplicate it.
				unresolved++
				review = append(review, p.key+": legacy upload could not be matched; original text may have changed")
				continue
			}
		}
		if rec.ID == 0 {
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
			// One item/request makes one returned ID unambiguous. Never guess when
			// the server returns an unexpected response or deduplicates silently.
			var ids []int64
			for _, b := range response {
				for _, id := range b.Modified {
					if id > 0 {
						ids = append(ids, id)
						rec.BookID = b.ID
					}
				}
			}
			if len(ids) == 1 {
				rec.ID = ids[0]
				rec.LocationType = "order"
			} else {
				r.loaded = false
				r.highlights = map[int64][]existingHighlight{}
				match, e := r.find(p.value)
				if e != nil {
					return fail(e)
				}
				if match.ID == 0 {
					return fail(errors.New("Readwise accepted upload but its ID could not be verified; retry will reconcile before uploading"))
				}
				rec.ID = match.ID
				rec.BookID = match.BookID
				rec.LocationType = match.LocationType
				state.Records[p.key] = rec
				if err := persist(); err != nil {
					return fail(err)
				}
				if err := patchHighlight(client, base, token, rec, p.value); err != nil {
					return fail(err)
				}
			}
			created++
			// Refresh stale caches after a creation.
			if !r.loaded {
				r.loaded = true
			}
			bookKnown := false
			for _, b := range r.books {
				if b.ID == rec.BookID {
					bookKnown = true
				}
			}
			if !bookKnown {
				r.books = append(r.books, readwiseBook{ID: rec.BookID, Title: p.value.Title, Author: p.value.Author, Category: "books"})
			}
			r.highlights[rec.BookID] = append(r.highlights[rec.BookID], existingHighlight{ID: rec.ID, Text: p.value.Text, Note: p.value.Note})
		} else {
			if err := patchHighlight(client, base, token, rec, p.value); err != nil {
				return fail(err)
			}
			updated++
		}
		rec.Payload = p.value
		rec.Fingerprint = fp
		state.Records[p.key] = rec
		state.Uploaded[p.key] = true
		if err := persist(); err != nil {
			return fail(err)
		}
	}
	for key := range state.Uploaded {
		if !currentKeys[key] {
			unresolved++
			review = append(review, key+": no current Kobo highlight/part; Readwise copy retained")
		}
	}
	if err := atomicWrite(filepath.Join(filepath.Dir(statePath), "sync-review.log"), []byte(strings.Join(review, "\n"))); err != nil {
		return fail(err)
	}
	if report != nil {
		report(fmt.Sprintf("Syncing...\nHighlights checked: %d\nChecking book covers.", len(all)))
	}
	covers, err := syncCovers(client, dbpath, endpoint, token, items)
	summary := fmt.Sprintf("Sync complete\nNew: %d  Updated: %d\nUnchanged: %d\nNeeds review: %d\nCovers added: %d\nCovers present: %d\nCovers skipped: %d", created, updated, unchanged, unresolved, covers.uploaded, covers.existing, covers.unmatched+covers.ambiguous)

	if err != nil {
		return "", fmt.Errorf("%s Cover sync failed: %w", summary, err)
	}
	return summary, nil
}

// PATCH has no documented location_type field or null-location clearing.
func patchHighlight(client *http.Client, base, token string, rec syncRecord, v ReadwiseHighlight) error {
	payload := map[string]any{"text": v.Text, "note": v.Note}
	if v.Location > 0 && (rec.LocationType == "" || rec.LocationType == "order") {
		payload["location"] = v.Location
	}
	return sendReadwiseJSON(client, http.MethodPatch, fmt.Sprintf("%s/api/v2/highlights/%d/", base, rec.ID), token, payload, nil)
}
