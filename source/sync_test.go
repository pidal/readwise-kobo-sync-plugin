package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fakeReadwise struct {
	server                   *httptest.Server
	items                    map[int64]ReadwiseHighlight
	posts, patches, failPost int
	loseResponse             bool
	omitIDs                  bool
	ambiguous                bool
}

func newFakeReadwise(t *testing.T) *fakeReadwise {
	f := &fakeReadwise{items: map[int64]ReadwiseHighlight{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		case r.Method == http.MethodPatch:
			id, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/highlights/"), "/"), 10, 64)
			h, ok := f.items[id]
			if e != nil || !ok {
				w.WriteHeader(404)
				return
			}
			var body struct {
				Text         string
				Note         string
				Location     *int
				LocationType string `json:"location_type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			h.Text = body.Text
			h.Note = body.Note
			if body.Location != nil {
				h.Location = *body.Location
			}
			if body.LocationType != "" {
				t.Error("undocumented location_type PATCH")
			}
			f.items[id] = h
			f.patches++
			fmt.Fprint(w, `{}`)
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
	return syncItems(path, filepath.Join(dir, "missing.sqlite"), f.server.URL+"/api/v2/highlights/", "test-token", state, hs, f.server.Client())
}
func TestOneWayEditsLocationsAndResume(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Author: "Author", Text: "Passage", Note: "My note", Chapter: "First", ChapterIndexKnown: true, StartPath: "/2/10", StartOffset: 4, Position: 1}}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	var id int64
	for k := range f.items {
		id = k
	}
	if f.items[id].Location != 1 || !strings.Contains(f.items[id].Note, "Kobo location:") {
		t.Fatal(f.items)
	}
	hs[0].Text = "Edited passage"
	hs[0].Note = ""
	hs[0].Position = 2
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 1 || f.items[id].Location != 2 || f.items[id].Text != "Edited passage" || strings.Contains(f.items[id].Note, "My note") {
		t.Fatal("edit did not update original highlight")
	}
	hs[0].StartPath = ""
	hs[0].Chapter = ""
	hs[0].Position = 0
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.items[id].Note != "" || f.items[id].Location != 2 || f.items[id].LocationType != "order" {
		t.Fatal("note was not cleared or existing order was lost")
	}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 2 {
		t.Fatal("unchanged item resent")
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
	if f.posts != 1 || len(f.items) != 1 || f.patches != 1 {
		t.Fatal("accepted upload duplicated")
	}
}
func TestLegacyMappingAndAmbiguity(t *testing.T) {
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Original", Position: 2}}
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			f := newFakeReadwise(t)
			defer f.server.Close()
			dir := t.TempDir()
			f.items[99] = ReadwiseHighlight{Title: "Book", Text: "Original"}
			f.ambiguous = ambiguous
			state := stateFile{Version: 1, Uploaded: map[string]bool{makePending(hs, map[string]bool{})[0].key: true}}
			if err := saveState(filepath.Join(dir, "sync-state.json"), state); err != nil {
				t.Fatal(err)
			}
			_, err := runItems(t, dir, f, hs)
			if ambiguous {
				if err == nil || f.patches != 0 || f.posts != 0 {
					t.Fatal("ambiguous book changed")
				}
			} else if err != nil || f.patches != 1 || f.posts != 0 {
				t.Fatalf("legacy not adopted: %v", err)
			}
		})
	}
}
func TestDataMigrationAndFailClosedState(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "token.txt"), []byte("private"), 0600)
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":1,"uploaded":{"a":true}}`), 0600)
	data, err := prepareData(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadState(filepath.Join(data, "sync-state.json"))
	if err != nil || !state.Uploaded["a"] {
		t.Fatal("lost history")
	}
	if _, err := os.Stat(filepath.Join(dir, "token.txt")); !os.IsNotExist(err) {
		t.Fatal("legacy token still importable")
	}
	os.WriteFile(filepath.Join(dir, "token.txt"), []byte("conflicting"), 0600)
	if _, err := prepareData(dir); err == nil {
		t.Fatal("conflicting token overwritten")
	}
	os.WriteFile(filepath.Join(data, "sync-state.json"), []byte(`broken`), 0600)
	if _, err := loadState(filepath.Join(data, "sync-state.json")); err == nil {
		t.Fatal("corrupt history silently reset")
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
	if f.posts != 1 || f.patches != 1 {
		t.Fatalf("failed to reconcile accepted upload: posts=%d patches=%d", f.posts, f.patches)
	}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || f.patches != 1 {
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
	if f.items[99].Location != 42 || f.items[99].LocationType != "page" || !strings.Contains(f.items[99].Note, "Kobo note") {
		t.Fatal("adopted page location overwritten")
	}
}
func TestRemovedHighlightsAreReportedAndRetained(t *testing.T) {
	f := newFakeReadwise(t)
	defer f.server.Close()
	dir := t.TempDir()
	hs := []Highlight{{ID: "one", Volume: "book", Title: "Book", Text: "Text"}}
	if _, err := runItems(t, dir, f, hs); err != nil {
		t.Fatal(err)
	}
	summary, err := runItems(t, dir, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.items) != 1 || !strings.Contains(summary, "1 need review") {
		t.Fatal(summary)
	}
	report, err := os.ReadFile(filepath.Join(dir, "sync-review.log"))
	if err != nil || !strings.Contains(string(report), "retained") {
		t.Fatal("missing removal report")
	}
}
