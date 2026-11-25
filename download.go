package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func fetch(e *Env, url string) ([]byte, error) {
	resp, err := get(e, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func get(e *Env, url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hostfix/"+version)
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: %s returned %s", url, resp.Status)
	}
	return resp, nil
}

func downloadVerified(e *Env, url, wantSHA256, dest string) error {
	resp, err := get(e, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, strings.TrimSpace(wantSHA256)) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA256)
	}
	return os.Rename(tmp.Name(), dest)
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	root := filepath.Clean(dest) + string(os.PathSeparator)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	realRoot = filepath.Clean(realRoot) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, rest, found := strings.Cut(strings.TrimPrefix(hdr.Name, "./"), "/")
		if !found || rest == "" {
			continue
		}
		path := filepath.Join(dest, filepath.FromSlash(rest))
		if !strings.HasPrefix(path, root) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err != nil || !strings.HasPrefix(parent+string(os.PathSeparator), realRoot) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, hdr.FileInfo().Mode().Perm()|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode().Perm()|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) || !strings.HasPrefix(filepath.Join(filepath.Dir(path), hdr.Linkname), root) {
				return fmt.Errorf("unsafe link in archive: %s -> %s", hdr.Name, hdr.Linkname)
			}
			os.Remove(path)
			if err := os.Symlink(hdr.Linkname, path); err != nil {
				return err
			}
		}
	}
}

func checksumFor(sums []byte, file string) string {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == file {
			return fields[0]
		}
	}
	return ""
}
