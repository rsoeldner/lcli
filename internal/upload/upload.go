// Package upload puts local files into Linear's storage and formats the
// markdown that embeds them in comments.
package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"

	"github.com/rsoeldner/lcli/internal/linear"
)

// File is a local file validated for upload.
type File struct {
	Path        string
	Name        string
	ContentType string
	Size        int64
}

// Stat validates path as an uploadable regular file and determines its
// content type from the extension, falling back to content sniffing.
func Stat(path string) (File, error) {
	st, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	if !st.Mode().IsRegular() {
		return File{}, fmt.Errorf("%s is not a regular file", path)
	}
	if st.Size() > math.MaxInt32 {
		return File{}, fmt.Errorf("%s is too large (%d bytes)", path, st.Size())
	}
	ctype := mime.TypeByExtension(filepath.Ext(path))
	if ctype == "" {
		ctype, err = sniff(path)
		if err != nil {
			return File{}, err
		}
	}
	if mt, _, err := mime.ParseMediaType(ctype); err == nil {
		ctype = mt
	}
	return File{Path: path, Name: filepath.Base(path), ContentType: ctype, Size: st.Size()}, nil
}

func sniff(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

// Snippet is the markdown embedding an uploaded file: an inline image for
// images, a link otherwise (Linear renders uploaded video links as players).
func Snippet(f File, assetURL string) string {
	name := strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(f.Name)
	if strings.HasPrefix(f.ContentType, "image/") {
		return fmt.Sprintf("![%s](%s)", name, assetURL)
	}
	return fmt.Sprintf("[%s](%s)", name, assetURL)
}

// Uploader performs Linear's two-step upload: the fileUpload mutation
// returns a signed URL and headers, then the file is PUT there.
type Uploader struct {
	Client graphql.Client
	HTTP   *http.Client
}

// Upload stores f and returns its permanent asset URL. The file is not
// visible on any issue until that URL is embedded in a comment or description.
func (u *Uploader) Upload(ctx context.Context, f File) (string, error) {
	resp, err := linear.FileUpload(ctx, u.Client, f.ContentType, f.Name, int(f.Size))
	if err != nil {
		return "", fmt.Errorf("prepare upload of %s: %w", f.Name, err)
	}
	target := resp.FileUpload.UploadFile
	if !resp.FileUpload.Success || target == nil || target.UploadUrl == "" || target.AssetUrl == "" {
		return "", fmt.Errorf("prepare upload of %s: Linear returned no upload URL", f.Name)
	}

	file, err := os.Open(f.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target.UploadUrl, file)
	if err != nil {
		return "", err
	}
	req.ContentLength = f.Size
	req.Header.Set("Content-Type", f.ContentType)
	req.Header.Set("Cache-Control", "public, max-age=31536000")
	for _, h := range target.Headers {
		req.Header.Set(h.Key, h.Value)
	}
	put, err := u.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", f.Name, err)
	}
	defer put.Body.Close()
	if put.StatusCode < 200 || put.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(put.Body, 300))
		return "", fmt.Errorf("upload %s: storage returned %s: %s", f.Name, put.Status, strings.TrimSpace(string(body)))
	}
	return target.AssetUrl, nil
}
