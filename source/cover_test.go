package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func testJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{R: 55, G: 200, B: 75, A: 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeEPUB(t *testing.T, filename string, cover []byte) {
	t.Helper()
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	files := map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`),
		"OPS/book.opf":           []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata><meta name="cover" content="front"/></metadata><manifest><item id="front" href="Images/front.jpg" media-type="image/jpeg"/></manifest></package>`),
		"OPS/Images/front.jpg":   cover,
	}
	for name, b := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEPUBCoverFindsOPFManifest(t *testing.T) {
	dir := t.TempDir()
	image := testJPEG(t)
	epub := filepath.Join(dir, "sample.kepub.epub")
	writeEPUB(t, epub, image)
	got := extractEPUBCover(epub)
	if !bytes.Equal(got, image) {
		t.Fatal("cover not extracted")
	}
}

func TestCacheCoverHashPath(t *testing.T) {
	root := t.TempDir()
	id := "file____mnt_onboard_book_epub"
	h := qhash(id)
	dir := filepath.Join(root, ".kobo-images", itoa(int(h&0xFF)), itoa(int((h&0xFF00)>>8)))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cover := testJPEG(t)
	if err := os.WriteFile(filepath.Join(dir, id+" - N3_LIBRARY_FULL.parsed"), cover, 0644); err != nil {
		t.Fatal(err)
	}
	if got := cachedCover(root, id); !bytes.Equal(got, cover) {
		t.Fatal("cached cover not found")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCoverSyncPatchesOnlyMissingCoverAndDoesNotResendHighlights(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kobo"), 0755); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(root, ".kobo", "KoboReader.sqlite")
	bookpath := filepath.Join(root, "mybook.epub")
	cover := testJPEG(t)
	writeEPUB(t, bookpath, cover)
	highlights := []Highlight{{ID: "one", Volume: bookpath, Text: "The test passage", Title: "My Book", Author: "Writer"}}
	patches := 0
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token testtoken" {
			t.Errorf("wrong token")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v2/books/":
			gets++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results":[{"id":42,"title":"My Book","author":"Writer","category":"books","cover_image_url":"https://readwise.io/static/images/default-book-icon-0.png"}],"next":null}`))
		case r.Method == "PATCH" && r.URL.Path == "/api/v2/books/42/":
			patches++
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			raw, err := base64.StdEncoding.DecodeString(payload["cover_image"])
			if err != nil || !bytes.Equal(raw, cover) {
				t.Errorf("wrong encoded cover")
			}
			w.Write([]byte(`{"id":42}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	result, err := syncCovers(srv.Client(), dbpath, srv.URL+"/api/v2/highlights/", "testtoken", highlights)
	if err != nil {
		t.Fatal(err)
	}
	if result.uploaded != 1 || gets != 1 || patches != 1 {
		t.Fatalf("bad result %+v, GET=%d PATCH=%d", result, gets, patches)
	}
}

func TestCoverSyncRespectsExistingAndAmbiguousBooks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kobo"), 0755); err != nil {
		t.Fatal(err)
	}
	image := testJPEG(t)
	epub := filepath.Join(root, "book.epub")
	writeEPUB(t, epub, image)
	items := []Highlight{{ID: "a", Volume: epub, Text: "Q", Title: "My Book", Author: "Writer"}}
	for _, mode := range []string{"existing", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			patches := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PATCH" {
					patches++
					w.Write([]byte(`{}`))
					return
				}
				if mode == "existing" {
					w.Write([]byte(`{"results":[{"id":42,"title":"My Book","author":"Writer","cover_image_url":"https://example.org/real-cover.jpg"}],"next":null}`))
				} else {
					w.Write([]byte(`{"results":[{"id":42,"title":"My Book","author":"Writer"},{"id":43,"title":"My Book","author":"Writer"}],"next":null}`))
				}
			}))
			defer srv.Close()
			report, err := syncCovers(srv.Client(), filepath.Join(root, ".kobo", "KoboReader.sqlite"), srv.URL+"/api/v2/highlights/", "token", items)
			if err != nil {
				t.Fatal(err)
			}
			if patches != 0 {
				t.Fatal("overwrote existing cover or ambiguous match")
			}
			if mode == "existing" && report.existing != 1 {
				t.Fatalf("bad summary: %+v", report)
			}
			if mode == "ambiguous" && report.ambiguous != 1 {
				t.Fatalf("bad summary: %+v", report)
			}
		})
	}
}
