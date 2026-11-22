package main

import (
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(filepath.Join(dir, "token.txt"), []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	fake := newFakeReadwise(t)
	defer fake.server.Close()
	fake.failPost = 2
	db := "../tests/fixtures/basic.sqlite"
	_, err := sync(dir, db, fake.server.URL+"/api/v2/highlights/", fake.server.Client())
	if err == nil {
		t.Fatal("expected interrupted sync")
	}
	saved, err := loadState(filepath.Join(dir, "data", "sync-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Uploaded) != 1 {
		t.Fatalf("expected one saved highlight: %+v", saved)
	}
	fake.failPost = 0
	result, err := sync(dir, db, fake.server.URL+"/api/v2/highlights/", fake.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Synced 50 new highlights") {
		t.Fatal(result)
	}
	calls := fake.posts
	if _, err := sync(dir, db, fake.server.URL+"/api/v2/highlights/", fake.server.Client()); err != nil {
		t.Fatal(err)
	}
	if fake.posts != calls || len(fake.items) != 51 {
		t.Fatalf("duplicate uploads: calls=%d items=%d", fake.posts, len(fake.items))
	}
	saved, err = loadState(filepath.Join(dir, "data", "sync-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range saved.Records {
		if rec.ID == 0 || rec.BookID == 0 || rec.Fingerprint == "" {
			t.Fatalf("missing identity: %+v", rec)
		}
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
