package main

// Read-only SQLite table reader limited to the standard table B-tree format.
// This intentionally does not execute SQL, checkpoint a WAL, or write Kobo data.
// It reads committed WAL frames to see highlights that Nickel has not checkpointed.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

type sqliteReader struct {
	db         *os.File
	wal        *os.File
	pageSize   int
	usable     int
	encoding   uint32
	walOffsets map[uint32]int64
	pageCount  uint32
}

func openSQLite(path string) (*sqliteReader, error) {
	db, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closeFailure := func(err error) (*sqliteReader, error) { db.Close(); return nil, err }
	var header [100]byte
	if _, err := io.ReadFull(db, header[:]); err != nil {
		return closeFailure(err)
	}
	if string(header[:16]) != "SQLite format 3\x00" {
		return closeFailure(errors.New("not a SQLite 3 database"))
	}
	ps := int(binary.BigEndian.Uint16(header[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps > 65536 || ps&(ps-1) != 0 {
		return closeFailure(fmt.Errorf("invalid page size %d", ps))
	}
	usable := ps - int(header[20])
	if usable < 480 {
		return closeFailure(errors.New("invalid reserved bytes"))
	}
	enc := binary.BigEndian.Uint32(header[56:60])
	if enc == 0 {
		enc = 1
	}
	if enc > 3 {
		return closeFailure(fmt.Errorf("unsupported encoding %d", enc))
	}
	r := &sqliteReader{db: db, pageSize: ps, usable: usable, encoding: enc,
		walOffsets: make(map[uint32]int64), pageCount: binary.BigEndian.Uint32(header[28:32])}
	if err := r.openWAL(path + "-wal"); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func (r *sqliteReader) close() {
	if r.wal != nil {
		r.wal.Close()
	}
	if r.db != nil {
		r.db.Close()
	}
}

func (r *sqliteReader) openWAL(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var header [32]byte
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if st.Size() < 32 {
		f.Close()
		return nil
	}
	if _, err = f.ReadAt(header[:], 0); err != nil {
		f.Close()
		return err
	}
	magic := binary.BigEndian.Uint32(header[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		f.Close()
		return errors.New("unexpected SQLite WAL magic")
	}
	sz := int(binary.BigEndian.Uint32(header[8:12]))
	if sz == 0 {
		sz = 65536
	}
	if sz != r.pageSize {
		f.Close()
		return fmt.Errorf("WAL page size %d differs from DB", sz)
	}
	stride := int64(24 + r.pageSize)
	if stride <= 0 {
		f.Close()
		return errors.New("invalid WAL stride")
	}
	type walFrame struct {
		p   uint32
		pos int64
	}
	var frames []walFrame
	lastCommit := 0
	for offset := int64(32); offset+stride <= st.Size(); offset += stride {
		var fh [24]byte
		if _, err = f.ReadAt(fh[:], offset); err != nil {
			break
		}
		if string(fh[8:16]) != string(header[16:24]) {
			break
		} // WAL reset or torn frame
		pg := binary.BigEndian.Uint32(fh[:4])
		if pg == 0 {
			break
		}
		frames = append(frames, walFrame{p: pg, pos: offset + 24})
		if commitPages := binary.BigEndian.Uint32(fh[4:8]); commitPages > 0 {
			lastCommit = len(frames)
			r.pageCount = commitPages
		}
	}
	if lastCommit == 0 {
		f.Close()
		return nil
	}
	for _, fr := range frames[:lastCommit] {
		r.walOffsets[fr.p] = fr.pos
	}
	r.wal = f
	return nil
}

func (r *sqliteReader) page(pageNum uint32) ([]byte, error) {
	if pageNum == 0 || (r.pageCount != 0 && pageNum > r.pageCount) {
		return nil, fmt.Errorf("invalid page number %d", pageNum)
	}
	b := make([]byte, r.pageSize)
	f := r.db
	off := int64(pageNum-1) * int64(r.pageSize)
	if woff, ok := r.walOffsets[pageNum]; ok {
		f = r.wal
		off = woff
	}
	if _, err := f.ReadAt(b, off); err != nil {
		return nil, fmt.Errorf("read page %d: %w", pageNum, err)
	}
	return b, nil
}

// SQLite varints have up to nine bytes: eight 7-bit groups, then 8 bits.
func sqliteVarint(b []byte, off int) (uint64, int, error) {
	var val uint64
	for i := 0; i < 9; i++ {
		if off+i >= len(b) {
			return 0, 0, io.ErrUnexpectedEOF
		}
		c := b[off+i]
		if i == 8 {
			return (val << 8) | uint64(c), 9, nil
		}
		val = (val << 7) | uint64(c&0x7f)
		if c&0x80 == 0 {
			return val, i + 1, nil
		}
	}
	return 0, 0, errors.New("bad varint")
}

func (r *sqliteReader) payload(page []byte, pos int) ([]byte, error) {
	n, nbytes, err := sqliteVarint(page, pos)
	if err != nil {
		return nil, err
	}
	if n > 20*1024*1024 {
		return nil, fmt.Errorf("SQLite record unreasonably large: %d", n)
	}
	_, rowbytes, err := sqliteVarint(page, pos+nbytes)
	if err != nil {
		return nil, err
	}
	start := pos + nbytes + rowbytes
	size := int(n)
	maxLocal := r.usable - 35
	minLocal := (r.usable-12)*32/255 - 23
	local := size
	if size > maxLocal {
		local = minLocal + (size-minLocal)%(r.usable-4)
		if local > maxLocal {
			local = minLocal
		}
	}
	if local < 0 || start < 0 || start+local > len(page) {
		return nil, errors.New("invalid SQLite cell payload bounds")
	}
	result := make([]byte, 0, size)
	result = append(result, page[start:start+local]...)
	if local == size {
		return result, nil
	}
	if start+local+4 > len(page) {
		return nil, io.ErrUnexpectedEOF
	}
	next := binary.BigEndian.Uint32(page[start+local : start+local+4])
	visited := map[uint32]bool{}
	for len(result) < size {
		if next == 0 || visited[next] {
			return nil, errors.New("invalid overflow page chain")
		}
		visited[next] = true
		p, err := r.page(next)
		if err != nil {
			return nil, err
		}
		next = binary.BigEndian.Uint32(p[:4])
		take := size - len(result)
		if take > r.usable-4 {
			take = r.usable - 4
		}
		result = append(result, p[4:4+take]...)
	}
	return result, nil
}

func (r *sqliteReader) forEachRecord(root uint32, fn func([]any) error) error {
	seen := map[uint32]bool{}
	var walk func(uint32, int) error
	walk = func(id uint32, depth int) error {
		if depth > 64 || seen[id] {
			return errors.New("SQLite B-tree loop or depth exceeded")
		}
		seen[id] = true
		p, err := r.page(id)
		if err != nil {
			return err
		}
		head := 0
		if id == 1 {
			head = 100
		}
		if head+12 > len(p) {
			return errors.New("truncated B-tree header")
		}
		typ := p[head]
		if typ != 5 && typ != 13 {
			return fmt.Errorf("expected table B-tree, got page type %d", typ)
		}
		cellCount := int(binary.BigEndian.Uint16(p[head+3 : head+5]))
		sz := 8
		if typ == 5 {
			sz = 12
		}
		if head+sz+2*cellCount > len(p) {
			return errors.New("invalid SQLite cell pointer array")
		}
		for i := 0; i < cellCount; i++ {
			ptr := int(binary.BigEndian.Uint16(p[head+sz+2*i : head+sz+2*i+2]))
			if ptr < 0 || ptr >= len(p) {
				return errors.New("invalid SQLite cell pointer")
			}
			if typ == 5 {
				if ptr+4 > len(p) {
					return io.ErrUnexpectedEOF
				}
				if err := walk(binary.BigEndian.Uint32(p[ptr:ptr+4]), depth+1); err != nil {
					return err
				}
			} else {
				data, err := r.payload(p, ptr)
				if err != nil {
					return err
				}
				fields, err := r.decodeRecord(data)
				if err != nil {
					return err
				}
				if err := fn(fields); err != nil {
					return err
				}
			}
		}
		if typ == 5 {
			return walk(binary.BigEndian.Uint32(p[head+8:head+12]), depth+1)
		}
		return nil
	}
	return walk(root, 0)
}

func (r *sqliteReader) decodeRecord(b []byte) ([]any, error) {
	headerSize, n, err := sqliteVarint(b, 0)
	if err != nil {
		return nil, err
	}
	if headerSize > uint64(len(b)) || headerSize < uint64(n) {
		return nil, errors.New("invalid SQLite record header")
	}
	serials := []uint64{}
	for off := n; off < int(headerSize); {
		serial, k, err := sqliteVarint(b, off)
		if err != nil {
			return nil, err
		}
		off += k
		serials = append(serials, serial)
	}
	offset := int(headerSize)
	values := make([]any, 0, len(serials))
	for _, serial := range serials {
		var width int
		switch serial {
		case 0, 8, 9:
			width = 0
		case 1, 2, 3, 4, 5, 6:
			width = map[uint64]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 6, 6: 8}[serial]
		case 7:
			width = 8
		case 10, 11:
			return nil, errors.New("reserved SQLite serial type")
		default:
			width = int((serial - 12) / 2)
		}
		if width < 0 || offset+width > len(b) {
			return nil, errors.New("invalid SQLite value width")
		}
		raw := b[offset : offset+width]
		offset += width
		switch {
		case serial == 0:
			values = append(values, nil)
		case serial == 8:
			values = append(values, int64(0))
		case serial == 9:
			values = append(values, int64(1))
		case serial >= 1 && serial <= 6:
			var v int64
			for _, x := range raw {
				v = (v << 8) | int64(x)
			}
			if width < 8 && raw[0]&0x80 != 0 {
				v |= ^int64(0) << uint(width*8)
			}
			values = append(values, v)
		case serial == 7:
			values = append(values, math.Float64frombits(binary.BigEndian.Uint64(raw)))
		case serial >= 13 && serial%2 == 1:
			values = append(values, decodeSQLiteText(raw, r.encoding))
		case serial >= 12 && serial%2 == 0:
			values = append(values, raw)
		default:
			return nil, errors.New("unhandled serial type")
		}
	}
	return values, nil
}

func decodeSQLiteText(b []byte, enc uint32) string {
	if enc == 1 {
		return string(b)
	}
	words := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if enc == 2 {
			words = append(words, binary.LittleEndian.Uint16(b[i:i+2]))
		} else {
			words = append(words, binary.BigEndian.Uint16(b[i:i+2]))
		}
	}
	return string(utf16.Decode(words))
}

type sqliteTable struct {
	root    uint32
	columns []string
}

func (r *sqliteReader) table(name string) (sqliteTable, error) {
	var result sqliteTable
	err := r.forEachRecord(1, func(vals []any) error {
		if len(vals) < 5 || str(vals[0]) != "table" || !strings.EqualFold(str(vals[1]), name) {
			return nil
		}
		root, err := toInt(vals[3])
		if err != nil {
			return err
		}
		result.root = uint32(root)
		result.columns = parseColumns(str(vals[4]))
		return nil
	})
	if err != nil {
		return result, err
	}
	if result.root == 0 || len(result.columns) == 0 {
		return result, fmt.Errorf("SQLite table %q not found or has unknown schema", name)
	}
	return result, nil
}

func (r *sqliteReader) rows(name string, fn func(map[string]any) error) error {
	tab, err := r.table(name)
	if err != nil {
		return err
	}
	return r.forEachRecord(tab.root, func(vals []any) error {
		row := make(map[string]any, len(tab.columns))
		for i, col := range tab.columns {
			if i < len(vals) {
				row[col] = vals[i]
			}
		}
		return fn(row)
	})
}

func str(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return ""
	}
}
func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("expected SQLite int, got %T", v)
	}
}

// CREATE TABLE column order is the on-disk record order, including columns
// added by ALTER TABLE. Split on commas outside quotes and nested expressions.
func parseColumns(ddl string) []string {
	start := strings.Index(ddl, "(")
	if start < 0 {
		return nil
	}
	parts := []string{}
	depth := 0
	partStart := start + 1
	var quote byte
	for i := start; i < len(ddl); i++ {
		c := ddl[i]
		if quote != 0 {
			if (quote == '[' && c == ']') || (quote != '[' && c == quote) {
				if quote != '[' && i+1 < len(ddl) && ddl[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 {
				parts = append(parts, ddl[partStart:i])
				break
			}
		}
		if c == ',' && depth == 1 {
			parts = append(parts, ddl[partStart:i])
			partStart = i + 1
		}
	}
	cols := []string{}
	for _, part := range parts {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		var name string
		if s[0] == '"' || s[0] == '`' || s[0] == '[' {
			close := s[0]
			if close == '[' {
				close = ']'
			}
			end := strings.IndexByte(s[1:], close)
			if end < 0 {
				continue
			}
			name = s[1 : 1+end]
		} else {
			end := strings.IndexAny(s, " \t\n\r")
			if end < 0 {
				end = len(s)
			}
			name = s[:end]
		}
		switch strings.ToLower(name) {
		case "primary", "foreign", "unique", "constraint", "check":
			continue
		}
		cols = append(cols, strings.ToLower(name))
	}
	return cols
}
