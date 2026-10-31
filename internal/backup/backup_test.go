package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func archive(t *testing.T, files ...string) string {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		tw.WriteHeader(&tar.Header{Name: f, Mode: 0o600, Size: 3})
		tw.Write([]byte("abc"))
	}
	tw.Close()
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDecode(t *testing.T) {
	b64 := archive(t, "etc/config/network", "etc/passwd")
	if _, err := Decode(b64[:60] + "\n" + b64[60:]); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(archive(t, "etc/passwd")); err == nil {
		t.Fatal("accepted an archive without /etc/config")
	}
	for _, bad := range []string{"", "!!!", base64.StdEncoding.EncodeToString([]byte("not gzip"))} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSaveIsPrivateAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	p, err := Save(root, "root@192.168.1.1", []byte("x"), now)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Dir(p)); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	if !strings.HasPrefix(p, root) || !strings.HasSuffix(p, "owrt-backup-20261029-120000.tar.gz") {
		t.Fatal(p)
	}
	if _, err := Save(root, "root@192.168.1.1", []byte("y"), now); err == nil {
		t.Fatal("overwrote an existing backup")
	}
	if q, err := Save(root, "../../evil", []byte("x"), now); err != nil || !strings.HasPrefix(q, root) {
		t.Fatalf("%v %s", err, q)
	}
}
