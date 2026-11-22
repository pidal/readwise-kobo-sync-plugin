package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	batchSize         = 25
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
	Chapter           string
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
	Version  int             `json:"version"`
	Uploaded map[string]bool `json:"uploaded"`
}

func main() {
	dir := flag.String("dir", defaultDir, "plugin data directory")
	db := flag.String("db", defaultDB, "Kobo SQLite database path")
	dry := flag.Bool("dry-run", false, "count highlights without uploading")
	status := flag.Bool("status", false, "display last sync result")
	backfill := flag.Bool("backfill-chapters", false, "opt-in chapter backfill of previously uploaded highlights")
	flag.Parse()
	if *status {
		b, err := os.ReadFile(filepath.Join(*dir, "last-result.txt"))
		if err != nil {
			fmt.Println("Not synced yet.")
			return
		}
		fmt.Print(string(b))
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
	var result string
	var err error
	if *backfill {
		result, err = backfillChapters(*dir, *db, readwiseURL, nil)
	} else {
		result, err = sync(*dir, *db, readwiseURL, nil)
	}
	if err != nil {
		result = "Sync failed: " + err.Error()
		fmt.Fprintln(os.Stderr, result)
	} else {
		fmt.Println(result)
	}
	writeStatus(*dir, result)
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
	st, err := os.Stat(p)
	if err == nil && time.Since(st.ModTime()) > 30*time.Minute {
		if err := os.Remove(p); err == nil {
			return os.Mkdir(p, 0700)
		}
	}
	return errors.New("sync is already running (remove .adds/readwise-sync/.lock if it is stale)")
}

func writeStatus(dir, summary string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	_ = os.WriteFile(filepath.Join(dir, "last-result.txt"), []byte(now+"\n"+summary+"\n"), 0600)
}

func loadHighlights(dbpath string) ([]Highlight, error) {
	r, err := openSQLite(dbpath)
	if err != nil {
		return nil, fmt.Errorf("open Kobo highlights: %w", err)
	}
	defer r.close()
	type metadata struct{ title, author, imageID string }
	type chapterData struct {
		title    string
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
			chapters[id] = chapterData{title: title, index: index, hasIndex: idxErr == nil}
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
		// A few firmware versions use a chapter row whose title repeats
		// the book title; avoid presenting that as a chapter.
		if strings.EqualFold(strings.TrimSpace(chapter.title), strings.TrimSpace(book.title)) {
			chapter.title = ""
		}
		startOffset, _ := strconv.Atoi(strings.TrimSpace(str(row["startoffset"])))
		highlights = append(highlights, Highlight{
			ID: id, Volume: vol, Text: text, Note: strings.TrimSpace(str(row["annotation"])),
			Created: str(row["datecreated"]), Title: book.title, Author: book.author, ImageID: book.imageID,
			Chapter: chapter.title, ChapterIndex: chapter.index, ChapterIndexKnown: chapter.hasIndex,
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

// Readwise has no dedicated chapter field. The chapter is added to the note,
// without altering the highlight text or removing the reader's original note.
func combineChapterNote(chapter, personal string) string {
	chapter = strings.TrimSpace(chapter)
	personal = strings.TrimSpace(personal)
	var note string
	if chapter == "" {
		note = personal
	} else if personal == "" {
		note = "Chapter: " + chapter
	} else {
		note = "Chapter: " + chapter + "\n\n" + personal
	}
	if len([]rune(note)) > 8191 {
		note = string([]rune(note)[:8150]) + " [Note truncated]"
	}
	return note
}

func makePending(items []Highlight, uploaded map[string]bool) []pendingItem {
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
			if uploaded[key] {
				continue
			}
			value := ReadwiseHighlight{
				Text: chunk, Title: truncateRunes(h.Title, 511), Author: truncateRunes(h.Author, 1024),
				SourceType: "kobo_nickel_sync", Category: "books", HighlightedAt: parseDate(h.Created),
			}
			if part == 0 {
				value.Note = combineChapterNote(h.Chapter, h.Note)
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
	s := stateFile{Version: 1, Uploaded: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("read sync state: %w", err)
	}
	if s.Version != 1 {
		return s, fmt.Errorf("unsupported sync state version %d", s.Version)
	}
	if s.Uploaded == nil {
		s.Uploaded = make(map[string]bool)
	}
	return s, nil
}

func saveState(path string, s stateFile) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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
	return &http.Client{Timeout: 45 * time.Second, Transport: transport}, nil
}

func postBatch(client *http.Client, url, token string, batch []pendingItem) error {
	highlights := make([]ReadwiseHighlight, len(batch))
	for i, p := range batch {
		highlights[i] = p.value
	}
	body, err := json.Marshal(struct {
		Highlights []ReadwiseHighlight `json:"highlights"`
	}{highlights})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Token "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "KoboReadwiseSync/1.2")
		response, err := client.Do(request)
		if err != nil {
			if attempt == 2 {
				return fmt.Errorf("network or TLS error: %w", err)
			}
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated {
			return nil
		}
		if response.StatusCode == 429 || response.StatusCode >= 500 {
			if attempt < 2 {
				wait := time.Duration(attempt+1) * time.Second
				if n, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && n > 0 && n <= 30 {
					wait = time.Duration(n) * time.Second
				}
				time.Sleep(wait)
				continue
			}
		}
		// Do not persist raw Readwise response: can contain user content.
		_ = responseBody
		return fmt.Errorf("Readwise returned HTTP %d (check token, input limits, or service status)", response.StatusCode)
	}
	return errors.New("Readwise upload failed after retries")
}

func sync(dir, dbpath, url string, client *http.Client) (string, error) {
	tokenBytes, err := os.ReadFile(filepath.Join(dir, "token.txt"))
	if err != nil {
		return "", errors.New("Readwise token missing: create .adds/readwise-sync/token.txt")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" || strings.Contains(strings.ToLower(token), "paste") || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("token.txt is empty or invalid")
	}
	s, stateErr := loadState(filepath.Join(dir, "state.json"))
	if stateErr != nil {
		return "", stateErr
	}
	items, err := loadHighlights(dbpath)
	if err != nil {
		return "", err
	}
	pending := makePending(items, s.Uploaded)
	if client == nil {
		client, err = newHTTPClient()
		if err != nil {
			return "", err
		}
	}
	uploadedCount := 0
	for start := 0; start < len(pending); start += batchSize {
		end := start + batchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		if err := postBatch(client, url, token, batch); err != nil {
			return "", fmt.Errorf("uploaded %d of %d new highlights before error: %w", uploadedCount, len(pending), err)
		}
		for _, h := range batch {
			s.Uploaded[h.key] = true
		}
		if err := saveState(filepath.Join(dir, "state.json"), s); err != nil {
			return "", fmt.Errorf("Readwise accepted batch, but failed to save sync state: %w", err)
		}
		uploadedCount += len(batch)
	}
	coverStatus, err := syncCovers(client, dbpath, url, token, items)
	summary := fmt.Sprintf("Synced %d new highlights. Kobo highlights scanned: %d. Covers: %d uploaded, %d already present, %d unmatched, %d ambiguous.",
		uploadedCount, len(items), coverStatus.uploaded, coverStatus.existing, coverStatus.unmatched, coverStatus.ambiguous)
	if err != nil {
		return "", fmt.Errorf("%s Cover sync failed: %w", summary, err)
	}
	return summary, nil
}
