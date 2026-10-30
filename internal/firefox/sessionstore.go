package firefox

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Session cookies (no expiry — the shop's global_sid is one) never reach
// cookies.sqlite: Firefox keeps them in memory and in its session-restore file,
// which it rewrites every few seconds while running (recovery.jsonlz4) and on
// exit (sessionstore.jsonlz4). The file is JSON in Mozilla's "mozLz4" wrapper.

// sessionStoreFiles lists the session-restore files to try, freshest first.
func sessionStoreFiles(profileDir string) []string {
	return []string{
		filepath.Join(profileDir, "sessionstore-backups", "recovery.jsonlz4"),
		filepath.Join(profileDir, "sessionstore.jsonlz4"),
	}
}

// sessionCookies returns the session cookies for hosts matching hostSuffix from
// the first readable session-restore file (nil, nil when there is none).
func sessionCookies(profileDir, hostSuffix string) (map[string]string, error) {
	for _, f := range sessionStoreFiles(profileDir) {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		js, err := decodeMozLz4(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		return cookiesFromSessionJSON(js, hostSuffix)
	}
	return nil, nil
}

type ssCookie struct {
	Host  string `json:"host"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// cookiesFromSessionJSON pulls matching cookies out of a session-store
// document: the top-level "cookies" list, plus the per-window lists older
// Firefox versions used.
func cookiesFromSessionJSON(js []byte, hostSuffix string) (map[string]string, error) {
	var doc struct {
		Cookies []ssCookie `json:"cookies"`
		Windows []struct {
			Cookies []ssCookie `json:"cookies"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(js, &doc); err != nil {
		return nil, fmt.Errorf("parse session store: %w", err)
	}
	all := doc.Cookies
	for _, w := range doc.Windows {
		all = append(all, w.Cookies...)
	}
	out := map[string]string{}
	for _, c := range all {
		if hostMatches(c.Host, hostSuffix) && c.Name != "" {
			out[c.Name] = c.Value
		}
	}
	return out, nil
}

// hostMatches reports whether a cookie host ("www.x.es", ".www.x.es") belongs
// to suffix ("x.es") — exact or a subdomain, never a lookalike ("notx.es").
func hostMatches(host, suffix string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), ".")
	suffix = strings.TrimPrefix(strings.ToLower(suffix), ".")
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}

var mozLz4Magic = []byte("mozLz40\x00")

// decodeMozLz4 unwraps Mozilla's format: 8-byte magic, little-endian uint32
// decompressed size, then one raw LZ4 block.
func decodeMozLz4(b []byte) ([]byte, error) {
	if len(b) < 12 || !bytes.Equal(b[:8], mozLz4Magic) {
		return nil, errors.New("not a mozLz4 file")
	}
	size := binary.LittleEndian.Uint32(b[8:12])
	if size > 512<<20 {
		return nil, fmt.Errorf("implausible mozLz4 size %d", size)
	}
	return lz4Block(b[12:], int(size))
}

// lz4Block decompresses one raw LZ4 block (the format is a sequence of
// literal-run + back-reference pairs; see the LZ4 block format spec).
func lz4Block(src []byte, size int) ([]byte, error) {
	errCorrupt := errors.New("corrupt lz4 block")
	dst := make([]byte, 0, size)
	i := 0
	readLen := func(n int) (int, bool) {
		if n != 15 {
			return n, true
		}
		for {
			if i >= len(src) {
				return 0, false
			}
			b := src[i]
			i++
			n += int(b)
			if b != 255 {
				return n, true
			}
		}
	}
	for i < len(src) {
		token := src[i]
		i++
		lit, ok := readLen(int(token >> 4))
		if !ok || i+lit > len(src) {
			return nil, errCorrupt
		}
		dst = append(dst, src[i:i+lit]...)
		i += lit
		if i >= len(src) {
			break // the last sequence carries literals only
		}
		if i+2 > len(src) {
			return nil, errCorrupt
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		ml, ok := readLen(int(token & 15))
		if !ok || off == 0 || off > len(dst) {
			return nil, errCorrupt
		}
		ml += 4
		start := len(dst) - off
		for k := 0; k < ml; k++ { // byte-wise: matches may overlap their own output
			dst = append(dst, dst[start+k])
		}
	}
	if len(dst) != size {
		return nil, fmt.Errorf("lz4: got %d bytes, header says %d", len(dst), size)
	}
	return dst, nil
}
