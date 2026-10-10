package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Source files are untrusted data: they are only downloaded into the cache
// directory (outside the repository), size-limited and parsed as XML.

const (
	maxDownload = 64 << 20 // an .atpack can be ~40 MB
	maxXML      = 32 << 20 // one SVD / ATDF file
)

var httpClient = &http.Client{Timeout: 10 * time.Minute}

func cachePath(cacheDir, u string) string {
	pu, err := url.Parse(u)
	base := "file"
	if err == nil {
		base = path.Base(pu.Path)
	}
	var safe strings.Builder
	for _, c := range base {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			safe.WriteRune(c)
		} else {
			safe.WriteByte('_')
		}
	}
	h := sha256.Sum256([]byte(u))
	return filepath.Join(cacheDir, hex.EncodeToString(h[:6])+"-"+safe.String())
}

// fetch returns the content of u, downloading it into the cache if needed.
func fetch(cacheDir, u string) ([]byte, error) {
	p := cachePath(cacheDir, u)
	if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
		if fi.Size() > maxDownload {
			return nil, fmt.Errorf("%s: cached file too large", u)
		}
		return os.ReadFile(p)
	}
	if !strings.HasPrefix(u, "https://") {
		return nil, fmt.Errorf("%s: only https sources are allowed", u)
	}
	fmt.Fprintf(os.Stderr, "download %s\n", u)
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, fmt.Errorf("%s: larger than %d bytes", u, maxDownload)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	return data, os.Rename(tmp, p)
}

// fetchSource returns the XML file of a source: a plain URL, or
// "URL#inner/path" for a member of a zip archive (a Microchip .atpack).
func fetchSource(cacheDir, src string) ([]byte, error) {
	u, inner, _ := strings.Cut(src, "#")
	data, err := fetch(cacheDir, u)
	if err != nil {
		return nil, err
	}
	if inner == "" {
		if len(data) > maxXML {
			return nil, fmt.Errorf("%s: XML larger than %d bytes", src, maxXML)
		}
		return data, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", u, err)
	}
	for _, f := range zr.File {
		if f.Name != inner {
			continue
		}
		if f.UncompressedSize64 > maxXML {
			return nil, fmt.Errorf("%s: member too large", src)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxXML+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxXML {
			return nil, fmt.Errorf("%s: member too large", src)
		}
		return b, nil
	}
	return nil, fmt.Errorf("%s: member %s not found", u, inner)
}
