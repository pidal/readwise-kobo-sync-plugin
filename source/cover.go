package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxCoverBytes = 4 << 20 // avoid huge memory and API requests on the e-reader

type localCover struct {
	title  string
	author string
	volume string
	data   []byte
}

type coverReport struct {
	found     int
	uploaded  int
	existing  int
	unmatched int
	ambiguous int
}

type readwiseBook struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	Category      string `json:"category"`
	CoverImageURL string `json:"cover_image_url"`
}

type epubPackage struct {
	Metadata struct {
		Meta []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Item []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			Properties string `xml:"properties,attr"`
			MediaType  string `xml:"media-type,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Guide struct {
		Reference []struct {
			Type string `xml:"type,attr"`
			Href string `xml:"href,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

// Allow only PNG, JPEG, GIF, and WebP as documented by the Readwise Books API.
func goodCover(data []byte) bool {
	if len(data) < 16 || len(data) > maxCoverBytes {
		return false
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

func readSmallFile(name string) []byte {
	f, err := os.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 16 || info.Size() > maxCoverBytes {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxCoverBytes+1))
	if err != nil || !goodCover(b) {
		return nil
	}
	return b
}

func qhash(imageID string) uint32 {
	var h uint32
	for _, b := range []byte(imageID) {
		h = (h << 4) + uint32(b)
		h ^= (h & 0xF0000000) >> 23
		h &= 0x0FFFFFFF
	}
	return h
}

func cachedCover(root, imageID string) []byte {
	if imageID == "" || strings.Contains(imageID, "/") || strings.Contains(imageID, "\\") || strings.Contains(imageID, "..") {
		return nil
	}
	h := qhash(imageID)
	roots := []string{
		filepath.Join(root, ".kobo", "images"),
		filepath.Join(root, ".kobo-images", strconv.Itoa(int(h&0xFF)), strconv.Itoa(int((h&0xFF00)>>8))),
	}
	// Prefer larger thumbnails, and treat .parsed as JPEG (verified by sniffing, not suffix).
	for _, suffix := range []string{"N3_FULL", "N3_LIBRARY_FULL", "NickelBookCover", "N3_LIBRARY_GRID"} {
		for _, dir := range roots {
			if b := readSmallFile(filepath.Join(dir, imageID+" - "+suffix+".parsed")); b != nil {
				return b
			}
		}
	}
	return nil
}

func volumePath(volume string) string {
	if strings.HasPrefix(volume, "file://") {
		parsed, err := url.Parse(volume)
		if err != nil || (parsed.Host != "" && parsed.Host != "localhost") {
			return ""
		}
		return parsed.Path
	}
	if filepath.IsAbs(volume) {
		return volume
	}
	return ""
}

func extractEPUBCover(filename string) []byte {
	lower := strings.ToLower(filename)
	if !strings.HasSuffix(lower, ".epub") {
		return nil
	}
	r, err := zip.OpenReader(filename)
	if err != nil {
		return nil
	}
	defer r.Close()
	files := make(map[string]*zip.File, len(r.File))
	for _, z := range r.File {
		files[path.Clean(z.Name)] = z
	}

	var opfPath string
	if container := files["META-INF/container.xml"]; container != nil {
		type rootContainer struct {
			Rootfiles []struct {
				FullPath string `xml:"full-path,attr"`
			} `xml:"rootfiles>rootfile"`
		}
		if b := readZipPart(container, 128<<10); b != nil {
			var c rootContainer
			if xml.Unmarshal(b, &c) == nil && len(c.Rootfiles) > 0 {
				opfPath = path.Clean(c.Rootfiles[0].FullPath)
			}
		}
	}
	if opfPath == "" {
		for name := range files {
			if strings.HasSuffix(strings.ToLower(name), ".opf") {
				opfPath = name
				break
			}
		}
	}
	var candidates []string
	if opf := files[opfPath]; opf != nil {
		b := readZipPart(opf, 512<<10)
		var pkg epubPackage
		if b != nil && xml.Unmarshal(b, &pkg) == nil {
			metaCoverID := ""
			for _, m := range pkg.Metadata.Meta {
				if strings.EqualFold(m.Name, "cover") {
					metaCoverID = m.Content
					break
				}
			}
			for _, item := range pkg.Manifest.Item {
				if item.ID == metaCoverID || strings.Contains(" "+item.Properties+" ", " cover-image ") {
					candidates = append(candidates, path.Clean(path.Join(path.Dir(opfPath), unescapeHref(item.Href))))
				}
			}
			for _, item := range pkg.Manifest.Item {
				if strings.Contains(strings.ToLower(item.Href), "cover") && strings.HasPrefix(item.MediaType, "image/") {
					candidates = append(candidates, path.Clean(path.Join(path.Dir(opfPath), unescapeHref(item.Href))))
				}
			}
		}
	}
	// EPUBs with incomplete metadata: use an image with cover in its filename.
	var generic []string
	for name := range files {
		lname := strings.ToLower(name)
		if strings.Contains(path.Base(lname), "cover") && (strings.HasSuffix(lname, ".jpg") || strings.HasSuffix(lname, ".jpeg") || strings.HasSuffix(lname, ".png") || strings.HasSuffix(lname, ".webp") || strings.HasSuffix(lname, ".gif")) {
			generic = append(generic, name)
		}
	}
	sort.Strings(generic)
	candidates = append(candidates, generic...)
	visited := make(map[string]bool)
	for _, name := range candidates {
		if visited[name] {
			continue
		}
		visited[name] = true
		if z := files[name]; z != nil {
			if b := readZipPart(z, maxCoverBytes); goodCover(b) {
				return b
			}
		}
	}
	return nil
}

func unescapeHref(s string) string {
	if n := strings.IndexByte(s, '#'); n >= 0 {
		s = s[:n]
	}
	if n, err := url.PathUnescape(s); err == nil {
		return n
	}
	return s
}

func readZipPart(z *zip.File, limit int64) []byte {
	if z.UncompressedSize64 == 0 || z.UncompressedSize64 > uint64(limit) {
		return nil
	}
	f, err := z.Open()
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(b) > int(limit) {
		return nil
	}
	return b
}

func keyBook(title, author string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " ")) + "\x00" + strings.ToLower(strings.Join(strings.Fields(author), " "))
}

// No image leaves the device except through an authenticated request to Readwise.
func collectCovers(dbpath string, highlights []Highlight) map[string]localCover {
	root := filepath.Dir(filepath.Dir(dbpath))
	covers := map[string]localCover{}
	seenVolumes := map[string]bool{}
	for _, h := range highlights {
		if seenVolumes[h.Volume] {
			continue
		}
		seenVolumes[h.Volume] = true
		title, author := truncateRunes(h.Title, 511), truncateRunes(h.Author, 1024)
		key := keyBook(title, author)
		if _, exists := covers[key]; exists { // mark ambiguous local editions
			covers[key] = localCover{}
			continue
		}
		var cover []byte
		if p := volumePath(h.Volume); p != "" {
			cover = extractEPUBCover(p)
		}
		if cover == nil {
			cover = cachedCover(root, h.ImageID)
		}
		if cover != nil {
			covers[key] = localCover{title: title, author: author, volume: h.Volume, data: cover}
		}
	}
	for k, c := range covers {
		if c.data == nil {
			delete(covers, k)
		}
	}
	return covers
}

func isDefaultReadwiseCover(u string) bool {
	return u == "" || strings.Contains(strings.ToLower(u), "/default-book-icon")
}

func sendReadwiseJSON(client *http.Client, method, endpoint, token string, payload any, out any) error {
	var body []byte
	var err error
	if payload != nil {
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("User-Agent", "KoboReadwiseSync")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			if attempt == 2 {
				return err
			}
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == 200 || resp.StatusCode == 201 {
			if out != nil && json.Unmarshal(b, out) != nil {
				return errors.New("unexpected JSON response from Readwise")
			}
			return nil
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if attempt < 2 {
				wait := time.Duration(attempt+1) * time.Second
				if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 && n < 90 {
					wait = time.Duration(n) * time.Second
				}
				time.Sleep(wait)
				continue
			}
		}
		return fmt.Errorf("Readwise books API HTTP %d", resp.StatusCode)
	}
	return errors.New("Readwise books API failed after retries")
}

func loadReadwiseBooks(client *http.Client, baseURL, token string) ([]readwiseBook, error) {
	var books []readwiseBook
	for pageNumber := 1; pageNumber <= 20; pageNumber++ {
		url := baseURL + "/api/v2/books/?category=books&page_size=1000&page=" + strconv.Itoa(pageNumber)
		var result struct {
			Next    string         `json:"next"`
			Results []readwiseBook `json:"results"`
		}
		if err := sendReadwiseJSON(client, http.MethodGet, url, token, nil, &result); err != nil {
			return nil, err
		}
		books = append(books, result.Results...)
		if result.Next == "" {
			return books, nil
		}
	}
	return nil, errors.New("Readwise book list exceeds 20 pages; cover sync aborted without changes")
}

func syncCovers(client *http.Client, dbpath, highlightsURL, token string, highlights []Highlight) (coverReport, error) {
	var report coverReport
	covers := collectCovers(dbpath, highlights)
	report.found = len(covers)
	if report.found == 0 {
		return report, nil
	}
	parsed, err := url.Parse(highlightsURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return report, errors.New("invalid Readwise URL")
	}
	baseURL := parsed.Scheme + "://" + parsed.Host
	books, err := loadReadwiseBooks(client, baseURL, token)
	if err != nil {
		return report, err
	}
	matches := make(map[string][]readwiseBook)
	for _, b := range books {
		if b.Category != "" && b.Category != "books" {
			continue
		}
		key := keyBook(b.Title, b.Author)
		if _, ok := covers[key]; ok {
			matches[key] = append(matches[key], b)
		}
	}
	keys := make([]string, 0, len(covers))
	for k := range covers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := covers[k]
		bs := matches[k]
		if len(bs) == 0 {
			report.unmatched++
			continue
		}
		if len(bs) > 1 {
			report.ambiguous++
			continue
		} // never patch an arbitrary same-title book
		b := bs[0]
		if !isDefaultReadwiseCover(b.CoverImageURL) {
			report.existing++
			continue
		}
		endpoint := fmt.Sprintf("%s/api/v2/books/%d/", baseURL, b.ID)
		if err := sendReadwiseJSON(client, http.MethodPatch, endpoint, token,
			map[string]string{"cover_image": base64.StdEncoding.EncodeToString(c.data)}, nil); err != nil {
			return report, fmt.Errorf("upload cover for %q: %w", c.title, err)
		}
		report.uploaded++
	}
	return report, nil
}
