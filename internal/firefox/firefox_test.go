package firefox

import (
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
