package main

import (
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
		if strings.Contains(p.value.Note, "Chapter: ") || strings.Contains(p.value.Note, "Kobo location:") {
			t.Fatalf("metadata leaked into note: %+v", p.value)
		}
		if p.value.Text == "Second chapter text" && !strings.Contains(p.value.Note, "Original note") {
			t.Fatal("personal note lost")
		}
	}
	if x := personalNote("my note"); x != "my note" {
		t.Fatal(x)
	}
}

func TestChapterBackfillDisabled(t *testing.T) {
	if _, err := backfillChapters(t.TempDir(), "missing", "invalid", nil); err == nil {
		t.Fatal("backfill must not write notes")
	}
}
