// Package media finds files uploaded to Linear in markdown, downloads them
// with the account's API key, and extracts still frames from videos.
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://[^\s()<>\[\]"'` + "`" + `]+`)

// Extract returns the distinct URLs in markdown whose host is one of hosts,
// in order of first appearance.
func Extract(markdown string, hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range urlRe.FindAllString(markdown, -1) {
		raw = strings.TrimRight(raw, ".,;:!?")
		u, err := url.Parse(raw)
		if err != nil || !hostAllowed(u.Host, hosts) || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out
}

func hostAllowed(host string, hosts []string) bool {
	for _, h := range hosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// Downloader fetches uploaded files. The API key is sent only to Hosts.
type Downloader struct {
	HTTP   *http.Client
	APIKey string
	Hosts  []string
}

// File is a downloaded file.
type File struct {
	Path        string
	ContentType string
}

// Download fetches rawURL into dir and returns where it was written. The file
// name is the URL's last path segment, plus an extension derived from the
// response content type when the segment has none.
func (d *Downloader) Download(ctx context.Context, rawURL, dir string) (File, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return File{}, err
	}
	if !hostAllowed(u.Host, d.Hosts) {
		return File{}, fmt.Errorf("refusing to send credentials to %s", u.Host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return File{}, err
	}
	req.Header.Set("Authorization", d.APIKey)
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return File{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return File{}, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	ctype := resp.Header.Get("Content-Type")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return File{}, err
	}
	name := fileName(u, ctype)
	dest := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return File{}, err
	}
	_, err = io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dest)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return File{}, fmt.Errorf("download %s: %w", rawURL, err)
	}
	return File{Path: dest, ContentType: ctype}, nil
}

func fileName(u *url.URL, contentType string) string {
	name := path.Base(u.Path)
	if name == "." || name == ".." || name == "/" || name == "" {
		name = "file"
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < ' ' {
			return '_'
		}
		return r
	}, name)
	if strings.HasPrefix(name, ".") {
		name = "_" + name
	}
	if path.Ext(name) == "" {
		if mt, _, err := mime.ParseMediaType(contentType); err == nil {
			if ext := preferredExt(mt); ext != "" {
				name += ext
			}
		}
	}
	return name
}

// preferredExt avoids platform-dependent picks like ".jfif" for image/jpeg.
func preferredExt(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// IsVideo reports whether contentType is a video type.
func IsVideo(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "video/")
}

// ErrNoFFmpeg is returned by Frames when ffmpeg or ffprobe is not installed.
var ErrNoFFmpeg = errors.New("ffmpeg/ffprobe not found in PATH; install ffmpeg to extract video frames")

// Frames extracts n still frames spread evenly across the video at
// videoPath, writing <video>.frame-<i>.jpg next to it.
func Frames(ctx context.Context, videoPath string, n int) ([]string, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, ErrNoFFmpeg
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, ErrNoFFmpeg
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", videoPath).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", videoPath, err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || duration <= 0 {
		return nil, fmt.Errorf("ffprobe %s: unknown duration %q", videoPath, strings.TrimSpace(string(out)))
	}
	var frames []string
	for i := range n {
		at := duration * (float64(i) + 0.5) / float64(n)
		dest := fmt.Sprintf("%s.frame-%d.jpg", videoPath, i+1)
		cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-y", "-ss", strconv.FormatFloat(at, 'f', 3, 64),
			"-i", videoPath, "-frames:v", "1", "-q:v", "3", dest)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return frames, fmt.Errorf("ffmpeg frame %d of %s: %v: %s", i+1, videoPath, err, strings.TrimSpace(string(msg)))
		}
		frames = append(frames, dest)
	}
	return frames, nil
}
