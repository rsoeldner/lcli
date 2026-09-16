package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	md := `Look: ![a](https://uploads.linear.app/org/1/2) and [video.mp4](https://uploads.linear.app/org/3/4).
Again https://uploads.linear.app/org/1/2, https://UPLOADS.linear.app/org/1/2, plus <https://uploads.linear.app/org/5/6> and
https://example.com/x.png and "https://uploads.linear.app.evil.com/7" and cleartext http://uploads.linear.app/org/8/9
**https://uploads.linear.app/org/10/11** _https://uploads.linear.app/org/12/13_ ~~https://uploads.linear.app/org/14/15~~`
	got := Extract(md, []string{"uploads.linear.app"})
	want := []string{
		"https://uploads.linear.app/org/1/2",
		"https://uploads.linear.app/org/3/4",
		"https://uploads.linear.app/org/5/6",
		"https://uploads.linear.app/org/10/11",
		"https://uploads.linear.app/org/12/13",
		"https://uploads.linear.app/org/14/15",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Extract = %q\nwant %q", got, want)
	}
}

func TestFileName(t *testing.T) {
	for _, tc := range []struct{ path, ctype, want string }{
		{"/org/a/b", "image/png", "b.png"},
		{"/org/a/b", "image/jpeg", "b.jpg"},
		{"/org/a/b", "video/quicktime", "b.mov"},
		{"/org/a/shot.webp", "image/png", "shot.webp"},
		{"/org/a/b", "", "b"},
		{"/", "image/png", "file.png"},
		{"/org/..", "", "file"},
		{"/org/.hidden", "", "_.hidden"},
	} {
		if got := fileName(&url.URL{Path: tc.path}, tc.ctype); got != tc.want {
			t.Errorf("fileName(%q, %q) = %q, want %q", tc.path, tc.ctype, got, tc.want)
		}
	}
}

func TestDownload(t *testing.T) {
	var gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/gif")
		w.Write([]byte("GIF89a"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	dir := t.TempDir()
	d := &Downloader{HTTP: srv.Client(), APIKey: "k", Hosts: []string{host}}

	f, err := d.Download(context.Background(), srv.URL+"/org/x/y", dir)
	if err != nil || f.Path != filepath.Join(dir, "y.gif") || f.ContentType != "image/gif" || gotAuth != "k" {
		t.Fatalf("Download = %+v, %v (auth %q)", f, err, gotAuth)
	}
	if b, _ := os.ReadFile(f.Path); string(b) != "GIF89a" {
		t.Errorf("content = %q", b)
	}

	if _, err := d.Download(context.Background(), srv.URL+"/missing", dir); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing file error = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files after failed download: %v", entries)
	}

	gotAuth = ""
	other := &Downloader{HTTP: srv.Client(), APIKey: "k", Hosts: []string{"uploads.linear.app"}}
	if _, err := other.Download(context.Background(), srv.URL+"/org/x/y", dir); err == nil || gotAuth != "" {
		t.Errorf("sent credentials to non-allowed host: err=%v auth=%q", err, gotAuth)
	}
}

func TestFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10", "-pix_fmt", "yuv420p", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate test video: %v: %s", err, out)
	}
	frames, err := Frames(context.Background(), video, 3)
	if err != nil || len(frames) != 3 {
		t.Fatalf("Frames = %v, %v", frames, err)
	}
	for _, f := range frames {
		if st, err := os.Stat(f); err != nil || st.Size() == 0 {
			t.Errorf("frame %s missing or empty: %v", f, err)
		}
	}
	if _, err := Frames(context.Background(), filepath.Join(dir, "nope.mp4"), 1); err == nil {
		t.Error("Frames on missing file succeeded")
	}
	if !IsVideo("video/mp4") || !IsVideo(" Video/QuickTime") || IsVideo("image/png") {
		t.Error("IsVideo misclassifies")
	}
}

func TestDownloadRefusesCleartext(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	d := &Downloader{HTTP: srv.Client(), APIKey: "k", Hosts: []string{strings.TrimPrefix(srv.URL, "http://")}}
	_, err := d.Download(context.Background(), srv.URL+"/org/x", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "refusing to send credentials to http://") || hits != 0 {
		t.Errorf("cleartext download: err=%v hits=%d", err, hits)
	}
}

func TestDownloadRedirectCredentials(t *testing.T) {
	var otherAuth string
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth = r.Header.Get("Authorization")
		w.Write([]byte("data"))
	}))
	defer other.Close()
	var sameAuth string
	var allowed *httptest.Server
	allowed = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-other":
			http.Redirect(w, r, other.URL+"/file", http.StatusFound)
		case "/to-self":
			http.Redirect(w, r, allowed.URL+"/final", http.StatusFound)
		default:
			sameAuth = r.Header.Get("Authorization")
			w.Write([]byte("data"))
		}
	}))
	defer allowed.Close()
	d := &Downloader{HTTP: allowed.Client(), APIKey: "secret", Hosts: []string{strings.TrimPrefix(allowed.URL, "https://")}}
	dir := t.TempDir()

	// Same hostname (127.0.0.1), different port: net/http alone would forward the key.
	if _, err := d.Download(context.Background(), allowed.URL+"/to-other", dir); err != nil {
		t.Fatal(err)
	}
	if otherAuth != "" {
		t.Errorf("key forwarded to non-allowed redirect target: %q", otherAuth)
	}
	if _, err := d.Download(context.Background(), allowed.URL+"/to-self", dir); err != nil {
		t.Fatal(err)
	}
	if sameAuth != "secret" {
		t.Errorf("key dropped on redirect within allowed host: %q", sameAuth)
	}
}

func TestDownloadTruncatedBodyLeavesNothing(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("short"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	d := &Downloader{HTTP: srv.Client(), APIKey: "k", Hosts: []string{strings.TrimPrefix(srv.URL, "https://")}}
	if _, err := d.Download(context.Background(), srv.URL+"/org/img", dir); err == nil {
		t.Fatal("truncated download succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left after truncated download: %v", entries)
	}
}

func TestDownloadNameCollision(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte(r.URL.Path))
	}))
	defer srv.Close()
	dir := t.TempDir()
	d := &Downloader{HTTP: srv.Client(), APIKey: "k", Hosts: []string{strings.TrimPrefix(srv.URL, "https://")}}
	a, err1 := d.Download(context.Background(), srv.URL+"/a/img", dir)
	b, err2 := d.Download(context.Background(), srv.URL+"/b/img", dir)
	again, err3 := d.Download(context.Background(), srv.URL+"/a/img", dir)
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	if a.Path != filepath.Join(dir, "img.png") || b.Path != filepath.Join(dir, "2-img.png") || again.Path != a.Path {
		t.Errorf("paths = %s, %s, %s", a.Path, b.Path, again.Path)
	}
	if got, _ := os.ReadFile(a.Path); string(got) != "/a/img" {
		t.Errorf("first file overwritten: %q", got)
	}
}
