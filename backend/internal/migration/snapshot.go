// Package migration provides offline, test-database-only migration rehearsals.
package migration

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ErrSnapshot = errors.New("snapshot integrity or compatibility check failed")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type fileInfo struct {
	SHA   string `json:"sha256"`
	Bytes int64  `json:"bytes"`
}
type manifest struct {
	Format   string              `json:"format"`
	Complete bool                `json:"complete"`
	Tables   map[string]int64    `json:"source_tables"`
	Files    map[string]fileInfo `json:"files"`
}
type snapshot struct {
	dir, hash string
	manifest  manifest
	config    []byte
}
type record struct {
	Table string          `json:"table"`
	DDL   *string         `json:"ddl"`
	Row   json.RawMessage `json:"row"`
	Count *int64          `json:"count"`
}

func privateFile(path string) error {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return ErrSnapshot
	}
	return nil
}
func readLimited(path string, max int64) ([]byte, error) {
	if e := privateFile(path); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, ErrSnapshot
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(data)) > max {
		return nil, ErrSnapshot
	}
	return data, nil
}
func openSnapshot(dir string) (snapshot, error) {
	s := snapshot{dir: dir}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return s, ErrSnapshot
	}
	raw, e := readLimited(filepath.Join(dir, "manifest.private.json"), 16<<20)
	if e != nil {
		return s, e
	}
	if json.Unmarshal(raw, &s.manifest) != nil || s.manifest.Format != "unibot-preparation-v1" || !s.manifest.Complete || s.manifest.Tables == nil || s.manifest.Files == nil {
		return s, ErrSnapshot
	}
	for table, count := range s.manifest.Tables {
		if !namePattern.MatchString(table) || count < 0 {
			return s, ErrSnapshot
		}
	}
	for _, required := range []string{"rows.ndjson.gz", "resolved-config.private.json"} {
		if _, ok := s.manifest.Files[required]; !ok {
			return s, ErrSnapshot
		}
	}
	seen := map[string]bool{}
	e = filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
		if e != nil || d.Type()&os.ModeSymlink != 0 {
			return ErrSnapshot
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return ErrSnapshot
		}
		if rel == "manifest.private.json" {
			return nil
		}
		wanted, ok := s.manifest.Files[filepath.ToSlash(rel)]
		if !ok || wanted.Bytes < 0 || len(wanted.SHA) != 64 {
			return ErrSnapshot
		}
		if e = privateFile(path); e != nil {
			return e
		}
		f, e := os.Open(path)
		if e != nil {
			return ErrSnapshot
		}
		hash := sha256.New()
		n, e := io.Copy(hash, f)
		f.Close()
		if e != nil || n != wanted.Bytes || hex.EncodeToString(hash.Sum(nil)) != wanted.SHA {
			return ErrSnapshot
		}
		seen[filepath.ToSlash(rel)] = true
		return nil
	})
	if e != nil || len(seen) != len(s.manifest.Files) {
		return s, ErrSnapshot
	}
	for path := range s.manifest.Files {
		if filepath.IsAbs(path) || strings.Contains(path, "\\") || filepath.ToSlash(filepath.Clean(path)) != path || path == ".." || strings.HasPrefix(path, "../") {
			return s, ErrSnapshot
		}
	}
	s.config, e = readLimited(filepath.Join(dir, "resolved-config.private.json"), 1<<20)
	if e != nil {
		return s, e
	}
	sum := sha256.Sum256(raw)
	configSum := sha256.Sum256(s.config)
	if hex.EncodeToString(configSum[:]) != s.manifest.Files["resolved-config.private.json"].SHA {
		return s, ErrSnapshot
	}
	s.hash = hex.EncodeToString(sum[:])
	return s, nil
}

// Cells keeps raw cell bytes (including JSON whitespace and integers above 2^53).
func cells(raw []byte) (map[string]*string, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	first, e := dec.Token()
	if e != nil || first != json.Delim('{') {
		return nil, ErrSnapshot
	}
	out := map[string]*string{}
	for dec.More() {
		key, e := dec.Token()
		k, ok := key.(string)
		if e != nil || !ok || !namePattern.MatchString(k) {
			return nil, ErrSnapshot
		}
		if _, exists := out[k]; exists {
			return nil, ErrSnapshot
		}
		var encoded *string
		if e = dec.Decode(&encoded); e != nil {
			return nil, ErrSnapshot
		}
		if encoded == nil {
			out[k] = nil
			continue
		}
		value, e := base64.StdEncoding.Strict().DecodeString(*encoded)
		if e != nil {
			return nil, ErrSnapshot
		}
		s := string(value)
		out[k] = &s
	}
	if _, e = dec.Token(); e != nil {
		return nil, ErrSnapshot
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, ErrSnapshot
	}
	return out, nil
}
func (s snapshot) walk(fn func(record, int64) error) error {
	file, e := os.Open(filepath.Join(s.dir, "rows.ndjson.gz"))
	if e != nil {
		return ErrSnapshot
	}
	defer file.Close()
	hash := sha256.New()
	gz, e := gzip.NewReader(io.TeeReader(file, hash))
	if e != nil {
		return ErrSnapshot
	}
	defer gz.Close()
	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	if !scanner.Scan() {
		return ErrSnapshot
	}
	var header struct{ Format, Encoding string }
	if json.Unmarshal(scanner.Bytes(), &header) != nil || header.Format != "unibot-source-v1" || header.Encoding != "base64-per-cell" {
		return ErrSnapshot
	}
	seen := map[string]bool{}
	current := ""
	var count int64
	signature := ""
	for scanner.Scan() {
		var r record
		if json.Unmarshal(scanner.Bytes(), &r) != nil || !namePattern.MatchString(r.Table) {
			return ErrSnapshot
		}
		if _, ok := s.manifest.Tables[r.Table]; !ok {
			return ErrSnapshot
		}
		switch {
		case r.DDL != nil:
			if current != "" || seen[r.Table] || r.Row != nil || r.Count != nil {
				return ErrSnapshot
			}
			if _, e = base64.StdEncoding.Strict().DecodeString(*r.DDL); e != nil {
				return ErrSnapshot
			}
			current = r.Table
			seen[current] = true
			count = 0
			signature = ""
		case r.Row != nil:
			if r.Table != current || r.Count != nil {
				return ErrSnapshot
			}
			row, cellErr := cells(r.Row)
			if cellErr != nil {
				return cellErr
			}
			keys := []string{}
			for key := range row {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			currentSignature := strings.Join(keys, "\x1f")
			if count == 0 {
				signature = currentSignature
			} else if signature != currentSignature {
				return ErrSnapshot
			}
			count++
		case r.Count != nil:
			if r.Table != current || count != *r.Count || count != s.manifest.Tables[current] {
				return ErrSnapshot
			}
			current = ""
		default:
			return ErrSnapshot
		}
		if e = fn(r, count); e != nil {
			return e
		}
	}
	if scanner.Err() != nil || current != "" || len(seen) != len(s.manifest.Tables) || hex.EncodeToString(hash.Sum(nil)) != s.manifest.Files["rows.ndjson.gz"].SHA {
		return ErrSnapshot
	}
	return nil
}
