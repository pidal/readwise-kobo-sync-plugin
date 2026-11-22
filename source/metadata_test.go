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

func TestChapterExtractionAndLocation(t *testing.T) {
	hs, err := loadHighlights("../tests/fixtures/chapters.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 3 {
		t.Fatalf("got %d", len(hs))
	}
	byID := map[string]Highlight{}
	for _, h := range hs {
		byID[h.ID] = h
	}
	if byID["id-early"].Chapter != "Chapter One" || byID["id-late"].Chapter != "Chapter Two" {
		t.Fatal("chapter title not joined")
	}
	if byID["id-early"].Position != 1 || byID["id-first"].Position != 2 || byID["id-late"].Position != 3 {
		t.Fatalf("wrong book order: %+v", byID)
	}
	pending := makePending(hs, map[string]bool{})
	for _, p := range pending {
		if p.value.LocationType != "order" || p.value.Location == 0 {
			t.Fatalf("wrong Readwise position: %+v", p)
		}
		if !strings.Contains(p.value.Note, "Chapter: ") {
			t.Fatalf("missing chapter: %+v", p.value)
		}
		if p.value.Text == "Second chapter text" && !strings.Contains(p.value.Note, "Original note") {
			t.Fatal("personal note lost")
		}
	}
	// No metadata may be invented from a missing chapter row.
	if x := combineChapterNote("", "my note"); x != "my note" {
		t.Fatal(x)
	}
}

func TestBackfillKeepsExistingNotesAndSkipsNonUploaded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	hs, err := loadHighlights("../tests/fixtures/chapters.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	pending := makePending(hs, map[string]bool{})
	state := stateFile{Version: 1, Uploaded: map[string]bool{}}
	for _, p := range pending {
		state.Uploaded[p.key] = true
	}
	if err := saveState(filepath.Join(dir, "state.json"), state); err != nil {
		t.Fatal(err)
	}
	var patchCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-token" {
			t.Error("auth")
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v2/books/"):
			fmt.Fprint(w, `{"results":[{"id":5,"title":"Test Chapters Book","author":"Author C","category":"books"}],"next":null}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/highlights/":
			fmt.Fprint(w, `{"results":[{"id":10,"text":"Earlier in chapter","note":"Personal Readwise note"},{"id":11,"text":"Early chapter text","note":"Chapter: already added"},{"id":12,"text":"Second chapter text","note":"Original note"}],"next":null}`)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v2/highlights/"):
			var x map[string]string
			if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
				t.Error(err)
			}
			if !strings.Contains(x["note"], "Chapter: ") {
				t.Error("missing chapter")
			}
			if strings.HasSuffix(r.URL.Path, "10/") && !strings.Contains(x["note"], "Personal Readwise note") {
				t.Error("lost changed note")
			}
			if strings.HasSuffix(r.URL.Path, "11/") {
				t.Error("re-patched already tagged")
			}
			patchCount++
			fmt.Fprint(w, `{"id":10}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	summary, err := backfillChapters(dir, "../tests/fixtures/chapters.sqlite", srv.URL+"/api/v2/highlights/", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if patchCount != 2 || !strings.Contains(summary, "2 updated") {
		t.Fatalf("patches=%d: %s", patchCount, summary)
	}
}
