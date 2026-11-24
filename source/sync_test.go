package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeReadwise struct {
	server                   *httptest.Server
	items                    map[int64]ReadwiseHighlight
	posts, patches, failPost int
	requests, coverPatches   int
	coverFail                bool
	uniqueAcrossBooks        bool
	loseResponse             bool
	omitIDs                  bool
	ambiguous                bool
}

func newFakeReadwise(t *testing.T) *fakeReadwise {
	f := &fakeReadwise{items: map[int64]ReadwiseHighlight{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests++
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/books/":
			books := []readwiseBook{}
			if len(f.items) > 0 {
				for _, h := range f.items {
					books = append(books, readwiseBook{ID: 7, Title: h.Title, Author: h.Author, Category: "books"})
					break
				}
			}
			if f.ambiguous && len(books) > 0 {
				b := books[0]
				b.ID = 8
				books = append(books, b)
			}
			json.NewEncoder(w).Encode(map[string]any{"results": books, "next": nil})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/highlights/":
			hs := []existingHighlight{}
			for id, h := range f.items {
				if f.uniqueAcrossBooks && r.URL.Query().Get("book_id") == "8" {
					continue
				}
				hs = append(hs, existingHighlight{ID: id, Text: h.Text, Note: h.Note, LocationType: h.LocationType})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": hs, "next": nil})
		case r.Method == http.MethodPost:
			f.posts++
			if f.posts == f.failPost {
				w.WriteHeader(401)
				return
			}
			var body struct {
				Highlights []ReadwiseHighlight `json:"highlights"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Highlights) != 1 {
				t.Errorf("bad single-item upload: %v", err)
				w.WriteHeader(400)
				return
			}
			h := body.Highlights[0]
			id := int64(len(f.items) + 100)
			for existing, v := range f.items {
				if v.Text == h.Text && v.Title == h.Title && v.Author == h.Author {
					id = existing
				}
			}
			f.items[id] = h
			if f.loseResponse {
				f.loseResponse = false
				fmt.Fprint(w, "invalid JSON after accepted upload")
				return
			}
			if f.omitIDs {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"id":7,"modified_highlights":[%d]}]`, id)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v2/books/"):
			f.coverPatches++
			if f.coverFail {
				w.WriteHeader(400)
				return
			}
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPatch:
			f.patches++
			t.Error("existing highlights must never be PATCHed")
			w.WriteHeader(400)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	return f
}

func runItems(t *testing.T, dir string, f *fakeReadwise, hs []Highlight) (string, error) {
	t.Helper()
	path := filepath.Join(dir, "sync-state.json")
	state, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	return syncItems(path, filepath.Join(dir, "missing.sqlite"), f.server.URL+"/api/v2/highlights/", "test-token", state, hs, f.server.Client(), nil)
}
func TestOnlyNewHighlightsAreUploaded(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Original", Note: "Personal", Position: 1}}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	calls := f.requests
	hs[0].Text = "Edited existing passage"
	hs[0].Note = "Changed note"
	hs[0].Position = 2
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.requests != calls || f.patches != 0 {
		t.Fatal("existing highlight caused network requests")
	}
	for _, h := range f.items {
		if h.Text != "Original" || h.Note != "Personal" || h.Location != 1 {
			t.Fatal("existing content changed")
		}
	}
	hs = append(hs, Highlight{ID: "two", Volume: "book", Title: "Book", Text: "A new passage", Note: "New personal note", Position: 3})
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 2 || f.patches != 0 || len(f.items) != 2 {
		t.Fatal("new highlight not uploaded exactly once")
	}
}
func TestLostResponseReconcilesBeforePosting(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Original"}}
	f.loseResponse = true
	if _, err := runItems(t, dir, f, hs); err == nil {
		t.Fatal("expected uncertain response")
	}
	hs[0].Text = "Changed after interrupted sync"
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || len(f.items) != 1 || f.patches != 0 {
		t.Fatal("accepted upload duplicated")
	}
}
func TestExistingMatchesAndAmbiguity(t *testing.T) {
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Original", Position: 2}}
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			f := newFakeReadwise(t)
			defer f.server.Close()
			dir := t.TempDir()
			f.items[99] = ReadwiseHighlight{Title: "Book", Text: "Original"}
			f.ambiguous = ambiguous

			summary, err := runItems(t, dir, f, hs)
			if err != nil || f.posts != 0 || f.patches != 0 {
				t.Fatalf("existing highlight changed: %v", err)
			}
			if ambiguous && !strings.Contains(summary, "Needs review: 1") {
				t.Fatal(summary)
			}
			if !ambiguous && !strings.Contains(summary, "Already synced: 1") {
				t.Fatal(summary)
			}
		})
	}
}
func TestCorruptHistoryFailsClosed(t *testing.T) {
	dir := t.TempDir()
	data, err := prepareData(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{`broken`, `{}`, `{"version":2,"records":null}`} {
		if err := os.WriteFile(filepath.Join(data, "sync-state.json"), []byte(broken), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadState(filepath.Join(data, "sync-state.json")); err == nil {
			t.Fatal("corrupt history silently reset")
		}
	}
}

func TestAcceptedResponseWithoutIDsIsReconciled(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	f.omitIDs = true
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Text", Note: "Note", Position: 3}}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 0 {
		t.Fatalf("failed to reconcile accepted upload: posts=%d patches=%d", f.posts, f.patches)
	}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 0 {
		t.Fatal("reconciled ID was not persisted")
	}
}
func TestAdoptedPageLocationIsPreserved(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	f.items[99] = ReadwiseHighlight{Title: "Book", Text: "Text", Location: 42, LocationType: "page"}
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Text", Position: 3, Note: "Kobo note"}}
	if _, err := runItems(t, t.TempDir(), f, hs); err != nil {
		t.Fatal(err)
	}
	if f.items[99].Location != 42 || f.items[99].LocationType != "page" || f.items[99].Note != "" {
		t.Fatal("adopted page location overwritten")
	}
}
func TestRemovedHighlightsAreRetainedWithoutRequests(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	if _, err := runItems(t, dir, f, []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Text"}}); err != nil {
		t.Fatal(err)
	}
	calls := f.requests
	if _, err := runItems(t, dir, f, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.items) != 1 || f.requests != calls {
		t.Fatal("removed highlight caused requests")
	}
}

func TestExistingPersonalNotesAreNotUploadedAgain(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Text", Note: "Personal", StartPath: "/2/4"}}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	hs[0].Note = "Edited on Kobo"
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 0 {
		t.Fatal("existing note was uploaded again")
	}
	for _, h := range f.items {
		if h.Note != "Personal" {
			t.Fatal("personal note modified")
		}
	}
}

func TestUniquePassageAcrossDuplicateBooksIsRecognized(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	f.ambiguous = true
	f.uniqueAcrossBooks = true
	f.items[99] = ReadwiseHighlight{Title: "Book", Text: "Already in Readwise"}
	summary, err := runItems(t, t.TempDir(), f, []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Already in Readwise"}})
	if err != nil || f.posts != 0 || !strings.Contains(summary, "Already synced: 1") {
		t.Fatalf("failed unique match: %v %s", err, summary)
	}
}
func TestLongHighlightResumesButEditsDoNotAddParts(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	// Distinct chunks so Readwise deduplication cannot collapse them.
	text := strings.Repeat("a", 8000) + strings.Repeat("b", 8000) + strings.Repeat("c", 3000)
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: text}}
	f.failPost = 2
	if _, err := runItems(t, dir, f, hs); err == nil {
		t.Fatal("expected interruption")
	}
	f.failPost = 0
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if len(f.items) != 3 {
		t.Fatal("missing original parts")
	}
	calls := f.requests
	hs[0].Text += strings.Repeat("d", 10000)
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.requests != calls || len(f.items) != 3 {
		t.Fatal("edit was treated as new highlight parts")
	}
}
func TestCoverFailureDoesNotFailSavedHighlights(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	f.coverFail = true
	epub := filepath.Join(dir, "book.epub")
	writeEPUB(t, epub, testJPEG(t))
	hs := []Highlight{{ID: "one", Volume: epub, Title: "Book", Text: "Text"}}
	summary, err := runItems(t, dir, f, hs)
	if err != nil || !strings.Contains(summary, "Sync complete") || !strings.Contains(summary, "Covers skipped") {
		t.Fatalf("false failure: %v %s", err, summary)
	}
	calls := f.requests
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.requests != calls || f.posts != 1 {
		t.Fatal("retry repeated saved work")
	}
}
