package firefox

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestProfileCandidatesOrder(t *testing.T) {
	ini := `[Profile1]
Name=other
IsRelative=1
Path=abc.other

[Profile0]
Name=default
IsRelative=1
Path=xyz.default
Default=1

[Install4F96D1932A9F858E]
Default=qrs.default-release
Locked=1
`
	got := profileCandidates(ini)
	want := []string{"qrs.default-release", "xyz.default", "abc.other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profileCandidates = %v, want %v", got, want)
	}
}

// Builds a minimal moz_cookies table and checks host matching, expiry in
// seconds and milliseconds, session cookies, and freshest-wins ordering.
func TestQuery(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "cookies.sqlite")
	now := time.Unix(1_800_000_000, 0)
	future, past := now.Add(time.Hour).Unix(), now.Add(-time.Hour).Unix()
	sql := `CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, expiry INTEGER, lastAccessed INTEGER);
INSERT INTO moz_cookies VALUES
 ('global_sid','old','.www.compraonline.alcampo.es',` + itoa(future) + `,1),
 ('global_sid','new','.www.compraonline.alcampo.es',` + itoa(future) + `,2),
 ('aws-waf-token','tok','www.compraonline.alcampo.es',` + itoa(future*1000) + `,1),
 ('VISITORID','v','.compraonline.alcampo.es',0,1),
 ('expired','x','.www.compraonline.alcampo.es',` + itoa(past) + `,1),
 ('other','y','.example.com',` + itoa(future) + `,1),
 ('evil','z','notcompraonline.alcampo.es',` + itoa(future) + `,1);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("build db: %v %s", err, out)
	}
	got, err := query(db, "compraonline.alcampo.es", now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"global_sid": "new", "aws-waf-token": "tok", "VISITORID": "v"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cookies = %v, want %v", got, want)
	}
}

func TestCookiesCopiesProfileDB(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "cookies.sqlite")
	sql := `CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, expiry INTEGER, lastAccessed INTEGER);
INSERT INTO moz_cookies VALUES ('global_sid','s','.www.compraonline.alcampo.es',0,1);`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("build db: %v %s", err, out)
	}
	before, _ := os.ReadFile(db)
	got, err := Cookies(dir, "compraonline.alcampo.es")
	if err != nil || got["global_sid"] != "s" {
		t.Fatalf("Cookies = %v, %v", got, err)
	}
	after, _ := os.ReadFile(db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("the profile's cookies.sqlite must not be modified")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// "abc" + back-reference (offset 3, length 9) + final literal "x".
var lz4Sample = []byte{0x35, 'a', 'b', 'c', 0x03, 0x00, 0x10, 'x'}

func TestLZ4Block(t *testing.T) {
	got, err := lz4Block(lz4Sample, 13)
	if err != nil || string(got) != "abcabcabcabcx" {
		t.Fatalf("lz4Block = %q, %v", got, err)
	}
	if _, err := lz4Block([]byte{0x35, 'a', 'b', 'c', 0x09, 0x00}, 13); err == nil {
		t.Fatal("an offset past the output must be rejected")
	}
	if _, err := lz4Block(lz4Sample, 99); err == nil {
		t.Fatal("a size mismatch must be rejected")
	}
}

// Long literal runs use the 15+255+… length extension.
func TestLZ4BlockLongLiteral(t *testing.T) {
	lit := bytes.Repeat([]byte("z"), 300)
	src := append([]byte{0xF0, 255, 30}, lit...) // 15 + 255 + 30 = 300
	got, err := lz4Block(src, 300)
	if err != nil || !bytes.Equal(got, lit) {
		t.Fatalf("long literal: %d bytes, %v", len(got), err)
	}
}

// literalLZ4 wraps data as a literals-only LZ4 block (valid, uncompressed).
func literalLZ4(data []byte) []byte {
	n := len(data)
	out := []byte{}
	if n < 15 {
		out = append(out, byte(n<<4))
	} else {
		out = append(out, 0xF0)
		for r := n - 15; ; r -= 255 {
			if r < 255 {
				out = append(out, byte(r))
				break
			}
			out = append(out, 255)
		}
	}
	return append(out, data...)
}

func TestSessionStoreCookiesMergedIntoCookies(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	sql := `CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, expiry INTEGER, lastAccessed INTEGER);
INSERT INTO moz_cookies VALUES ('aws-waf-token','tok','.www.compraonline.alcampo.es',0,1),('VISITORID','old','.www.compraonline.alcampo.es',0,1);`
	if out, err := exec.Command("sqlite3", filepath.Join(dir, "cookies.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("build db: %v %s", err, out)
	}
	js := []byte(`{"windows":[{"cookies":[{"host":"www.example.com","name":"x","value":"1"}]}],
	  "cookies":[{"host":".www.compraonline.alcampo.es","name":"global_sid","value":"sid"},
	             {"host":"www.compraonline.alcampo.es","name":"VISITORID","value":"new"},
	             {"host":"notcompraonline.alcampo.es","name":"evil","value":"z"}]}`)
	file := append(append([]byte("mozLz40\x00"), 0, 0, 0, 0), literalLZ4(js)...)
	binary.LittleEndian.PutUint32(file[8:12], uint32(len(js)))
	if err := os.MkdirAll(filepath.Join(dir, "sessionstore-backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessionstore-backups", "recovery.jsonlz4"), file, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Cookies(dir, "compraonline.alcampo.es")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"global_sid": "sid", "aws-waf-token": "tok", "VISITORID": "new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cookies = %v, want %v", got, want)
	}
}
