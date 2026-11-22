package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSqliteFixture(t *testing.T) {
	items, err := loadHighlights("../tests/fixtures/basic.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 51 {
		t.Fatalf("want 51, got %d", len(items))
	}
	if items[0].Title != "Test Book" || items[0].Author != "Test Author" {
		t.Fatalf("metadata missing: %+v", items[0])
	}
}

func TestPendingLongTextAndIds(t *testing.T) {
	h := Highlight{ID: "id", Volume: "book", Text: strings.Repeat("文", 19000), Note: "This is a note", Title: "Chinese"}
	pending := makePending([]Highlight{h}, map[string]bool{})
	if len(pending) != 3 {
		t.Fatalf("got %d parts", len(pending))
	}
	if len([]rune(pending[0].value.Text)) != 8000 {
		t.Fatal("bad split")
	}
	if pending[0].value.Note != "This is a note" || pending[1].value.Note != "" {
		t.Fatal("note should only be on first chunk")
	}
	visited := map[string]bool{pending[0].key: true}
	if count := len(makePending([]Highlight{h}, visited)); count != 2 {
		t.Fatalf("expected 2 pending, got %d", count)
	}
}

func TestSyncPersistsOnlySuccessfulBatches(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("test-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var count int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-token" {
			t.Error("wrong authorization")
		}
		if r.URL.Path != "/api/v2/highlights/" {
			t.Error("wrong URL")
		}
		var body struct {
			Highlights []ReadwiseHighlight `json:"highlights"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Highlights) > 25 {
			t.Error("batch too large")
		}
		if body.Highlights[0].Title != "Test Book" || body.Highlights[0].Note == "" {
			t.Error("lost metadata or note")
		}
		n := atomic.AddInt32(&count, 1)
		if n == 2 {
			w.WriteHeader(401)
		} else {
			w.WriteHeader(200)
			w.Write([]byte("[]"))
		}
	}))
	defer fake.Close()
	db := "../tests/fixtures/basic.sqlite"
	// Batch 1 succeeds, batch 2 fails. Resuming sends only the remaining two batches.
	_, err := sync(dir, db, fake.URL+"/api/v2/highlights/", fake.Client())
	if err == nil || !strings.Contains(err.Error(), "uploaded 25 of 51") {
		t.Fatalf("expected partial failure: %v", err)
	}
	saved, err := loadState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Uploaded) != 25 {
		t.Fatalf("want 25 persisted ids, got %d", len(saved.Uploaded))
	}
	result, err := sync(dir, db, fake.URL+"/api/v2/highlights/", fake.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Synced 26 new highlights") {
		t.Fatal(result)
	}
	if _, err := sync(dir, db, fake.URL+"/api/v2/highlights/", fake.Client()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&count); got != 4 {
		t.Fatalf("expected 4 HTTP calls, got %d", got)
	}
}

func TestBadTokenNoUpload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("PASTE_YOUR_TOKEN_HERE"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := sync(dir, "../tests/fixtures/basic.sqlite", readwiseURL, nil)
	if err == nil {
		t.Fatal("expected token error")
	}
}
