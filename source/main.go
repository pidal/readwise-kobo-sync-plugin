package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed certs.pem
var certFS embed.FS

const (
	defaultDir        = "/mnt/onboard/.adds/readwise-sync"
	defaultDB         = "/mnt/onboard/.kobo/KoboReader.sqlite"
	readwiseURL       = "https://readwise.io/api/v2/highlights/"
	maxHighlightRunes = 8000
)

type Highlight struct {
	ID                string
	Volume            string
	Text              string
	Note              string
	Created           string
	Title             string
	Author            string
	ImageID           string
	ChapterIndex      int
	ChapterIndexKnown bool
	StartPath         string
	StartOffset       int
	Position          int // book-order ordinal: not a printed page
}

type ReadwiseHighlight struct {
	Text          string `json:"text"`
	Title         string `json:"title"`
	Author        string `json:"author,omitempty"`
	Note          string `json:"note,omitempty"`
	SourceType    string `json:"source_type"`
	Category      string `json:"category"`
	HighlightedAt string `json:"highlighted_at,omitempty"`
	Location      int    `json:"location,omitempty"`
	LocationType  string `json:"location_type,omitempty"`
}

type pendingItem struct {
	key   string
	value ReadwiseHighlight
}

type stateFile struct {
	Version int                   `json:"version"`
	Records map[string]syncRecord `json:"records"`
}

func main() {
	dir := flag.String("dir", defaultDir, "plugin data directory")
	db := flag.String("db", defaultDB, "Kobo SQLite database path")
	dry := flag.Bool("dry-run", false, "count highlights without uploading")
	status := flag.Bool("status", false, "display last sync result")
	flag.Parse()
	if *status {
		b, err := os.ReadFile(filepath.Join(*dir, "last-result.log"))
		if err != nil {
			fmt.Println("Not synced yet.")
			return
		}
		fmt.Println(wrapStatus(strings.TrimSpace(string(b)), 32))
		return
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *dry {
		list, err := loadHighlights(*db)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Found %d Kobo highlights (nothing uploaded).\n", len(list))
		return
	}
	if err := acquireLock(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.Remove(filepath.Join(*dir, ".lock"))
	if logfile, e := os.OpenFile(filepath.Join(*dir, "run.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
		defer logfile.Close()
		os.Stdout = logfile
		os.Stderr = logfile
	}

	writeStatus(*dir, "Syncing...\nReading Kobo highlights.")
	result, err := sync(*dir, *db, readwiseURL, nil)
	if err != nil {
		result = "Sync failed: " + err.Error()
		fmt.Fprintln(os.Stderr, result)
	} else {
		fmt.Println(result)
	}
	if err != nil {
		writeStatus(*dir, statusFailure(err))
	} else {
		writeStatus(*dir, result)
	}
	if err != nil {
		os.Remove(filepath.Join(*dir, ".lock"))
		os.Exit(1)
	}
}

func acquireLock(dir string) error {
	p := filepath.Join(dir, ".lock")
	if err := os.Mkdir(p, 0700); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}
	return errors.New("sync is already running (remove .adds/readwise-sync/.lock if it is stale)")
}

func writeStatus(dir, summary string) {
	now := time.Now().Format("02 Jan 15:04")
	_ = atomicWrite(filepath.Join(dir, "last-result.log"), []byte(now+"\n"+wrapStatus(summary, 32)+"\n"))
}

func loadHighlights(dbpath string) ([]Highlight, error) {
	before, err := databaseStamp(dbpath)
	if err != nil {
		return nil, err
	}
	r, err := openSQLite(dbpath)
	if err != nil {
		return nil, fmt.Errorf("open Kobo highlights: %w", err)
	}
	defer r.close()
	type metadata struct{ title, author, imageID string }
	type chapterData struct {
		index    int
		hasIndex bool
	}
	books := make(map[string]metadata)
	chapters := make(map[string]chapterData)
	err = r.rows("content", func(row map[string]any) error {
		id := str(row["contentid"])
		if id == "" {
			return nil
		}
		typ := str(row["contenttype"])
		title := strings.TrimSpace(str(row["title"]))
		if typ == "6" {
			author := strings.TrimSpace(str(row["attribution"]))
			if title != "" {
				books[id] = metadata{title, author, strings.TrimSpace(str(row["imageid"]))}
			}
		} else {
			// Chapter rows are generally ContentType=9. Use the exact
			// Bookmark.ContentID join; never infer a chapter from a filename.
			index, idxErr := strconv.Atoi(strings.TrimSpace(str(row["volumeindex"])))
			chapters[id] = chapterData{index: index, hasIndex: idxErr == nil}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read Kobo book metadata: %w", err)
	}
	var highlights []Highlight
	err = r.rows("Bookmark", func(row map[string]any) error {
		text := strings.TrimSpace(str(row["text"]))
		if text == "" {
			return nil
		}
		hidden := strings.ToLower(strings.TrimSpace(str(row["hidden"])))
		if hidden == "1" || hidden == "true" {
			return nil
		}
		id := str(row["bookmarkid"])
		vol := str(row["volumeid"])
		if id == "" || vol == "" {
			return nil
		}
		book := books[vol]
		if book.title == "" {
			book.title = filenameTitle(vol)
		}
		chapter := chapters[str(row["contentid"])]
		startOffset, _ := strconv.Atoi(strings.TrimSpace(str(row["startoffset"])))
		highlights = append(highlights, Highlight{
			ID: id, Volume: vol, Text: text, Note: strings.TrimSpace(str(row["annotation"])),
			Created: str(row["datecreated"]), Title: book.title, Author: book.author, ImageID: book.imageID,
			ChapterIndex: chapter.index, ChapterIndexKnown: chapter.hasIndex,
			StartPath: str(row["startcontainerpath"]), StartOffset: startOffset,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read Kobo bookmarks: %w", err)
	}
	// Kobo does not provide reliable physical page numbers for EPUBs.
	// If all chapter indices and paths are available for a book, a
	// numeric per-book ordinal can be sent to Readwise as "order".
	byVolume := make(map[string][]int)
	for i := range highlights {
		byVolume[highlights[i].Volume] = append(byVolume[highlights[i].Volume], i)
	}
	for _, inds := range byVolume {
		ordered := true
		for _, i := range inds {
			if !highlights[i].ChapterIndexKnown || highlights[i].StartPath == "" {
				ordered = false
				break
			}
		}
		if !ordered {
			continue
		}
		sort.SliceStable(inds, func(a, b int) bool {
			x, y := highlights[inds[a]], highlights[inds[b]]
			if x.ChapterIndex != y.ChapterIndex {
				return x.ChapterIndex < y.ChapterIndex
			}
			if cmp := compareKoboPath(x.StartPath, y.StartPath); cmp != 0 {
				return cmp < 0
			}
			if x.StartOffset != y.StartOffset {
				return x.StartOffset < y.StartOffset
			}
			return x.ID < y.ID
		})
		for order, i := range inds {
			highlights[i].Position = order + 1
		}
	}
	sort.Slice(highlights, func(i, j int) bool {
		if highlights[i].Volume == highlights[j].Volume {
			return highlights[i].Created < highlights[j].Created
		}
		return highlights[i].Volume < highlights[j].Volume
	})
	after, err := databaseStamp(dbpath)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, errors.New("Kobo database changed during extraction; retry sync")
	}
	return highlights, nil
}

var pathNumber = regexp.MustCompile(`[0-9]+`)

// Compare CFI and KEPUB span paths by numeric components ("2" < "10").
// If paths are not comparable, lexical fallback is deterministic but does not
// claim to be a printed page number.
func compareKoboPath(a, b string) int {
	pa, pb := pathNumber.FindAllString(a, -1), pathNumber.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.ParseUint(pa[i], 10, 64)
		nb, eb := strconv.ParseUint(pb[i], 10, 64)
		if ea == nil && eb == nil {
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
		}
	}
	return strings.Compare(a, b)
}

func filenameTitle(volume string) string {
	base := volume
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.TrimSuffix(base, ".kepub.epub")
	base = strings.TrimSuffix(base, ".epub")
	if base == "" {
		return "Unknown Kobo book"
	}
	return base
}

func parseDate(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range []string{
		time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000", "2006-01-02T15:04:05", "2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// Only personal annotations belong in Readwise notes.
func personalNote(note string) string {
	if len([]rune(note)) > 8191 {
		return string([]rune(note)[:8150]) + " [Note truncated]"
	}
	return note
}

func makePending(items []Highlight) []pendingItem {
	var pending []pendingItem
	for _, h := range items {
		chars := []rune(h.Text)
		for start, part := 0, 0; start < len(chars); start, part = start+maxHighlightRunes, part+1 {
			end := start + maxHighlightRunes
			if end > len(chars) {
				end = len(chars)
			}
			chunk := string(chars[start:end])
			sum := sha256.Sum256([]byte(h.Volume + "\x00" + h.ID + "\x00" + strconv.Itoa(part)))
			key := hex.EncodeToString(sum[:])
			value := ReadwiseHighlight{
				Text: chunk, Title: truncateRunes(h.Title, 511), Author: truncateRunes(h.Author, 1024),
				SourceType: "kobo_nickel_sync", Category: "books", HighlightedAt: parseDate(h.Created),
			}
			if part == 0 {
				value.Note = personalNote(h.Note)
			}
			if h.Position > 0 {
				value.LocationType = "order"
				value.Location = h.Position
			}
			pending = append(pending, pendingItem{key, value})
		}
	}
	return pending
}

func loadState(path string) (stateFile, error) {
	s := stateFile{Version: 2, Records: make(map[string]syncRecord)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return s, fmt.Errorf("read sync state: %w", err)
	}
	if fields["version"] == nil || fields["records"] == nil || string(fields["records"]) == "null" {
		return s, errors.New("sync history is incomplete; restore your saved data instead of resetting it")
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("read sync state: %w", err)
	}
	if s.Version != 2 {
		return s, fmt.Errorf("unsupported sync state version %d", s.Version)
	}
	if s.Records == nil {
		s.Records = make(map[string]syncRecord)
	}
	return s, nil
}

func saveState(path string, s stateFile) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}

func newHTTPClient() (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	bundled, err := certFS.ReadFile("certs.pem")
	if err != nil {
		return nil, err
	}
	if !roots.AppendCertsFromPEM(bundled) {
		return nil, errors.New("could not load bundled CA certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Timeout: 45 * time.Second, Transport: &pacedTransport{base: transport, lists: map[string]time.Time{}}}, nil
}
