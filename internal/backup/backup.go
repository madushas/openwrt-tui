// Package backup stores a router configuration archive (the output of
// sysupgrade -b) on the PC. The archive contains passwords and keys, so it is
// written only to a private directory with a private mode.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxSize bounds what is accepted from the router.
const MaxSize = 20 << 20

// Decode turns the router's base64 output into the archive and checks that it
// is a gzip'd tar that holds /etc/config.
func Decode(b64 string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		return nil, fmt.Errorf("the router's backup is not valid base64: %w", err)
	}
	if len(data) == 0 || len(data) > MaxSize {
		return nil, fmt.Errorf("backup has an unexpected size (%d bytes)", len(data))
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("the backup is not a gzip archive")
	}
	tr := tar.NewReader(zr)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the backup archive is damaged: %w", err)
		}
		if strings.Contains(h.Name, "etc/config/") {
			found = true
		}
		if _, err := io.Copy(io.Discard, io.LimitReader(tr, MaxSize)); err != nil {
			return nil, fmt.Errorf("the backup archive is damaged: %w", err)
		}
	}
	if !found {
		return nil, errors.New("the backup does not contain /etc/config")
	}
	return data, nil
}

var safe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Save writes the archive under root/backups/<router>/ and returns its path.
func Save(root, router string, data []byte, now time.Time) (string, error) {
	name := strings.Trim(safe.ReplaceAllString(router, "_"), "._")
	if name == "" {
		name = "router"
	}
	dir := filepath.Join(root, "backups", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	p := filepath.Join(dir, "owrt-backup-"+now.UTC().Format("20060102-150405")+".tar.gz")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return "", err
	}
	return p, f.Close()
}
