package upload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStat(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"a.png", []byte("x"), "image/png"},
		{"a.JPG", []byte("x"), "image/jpeg"},
		{"clip.mp4", []byte("x"), "video/mp4"},
		{"notes.txt", []byte("hello"), "text/plain"},
		{"noext", []byte("\x89PNG\r\n\x1a\n0000"), "image/png"},
		{"empty-noext", nil, "text/plain"},
	} {
		f, err := Stat(write(tc.name, tc.data))
		if err != nil || f.ContentType != tc.want || f.Name != tc.name || f.Size != int64(len(tc.data)) {
			t.Errorf("Stat(%s) = %+v, %v; want type %s", tc.name, f, err, tc.want)
		}
	}
	if _, err := Stat(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("Stat(dir) error = %v", err)
	}
	if _, err := Stat(filepath.Join(dir, "missing")); err == nil {
		t.Error("Stat(missing) succeeded")
	}
}

func TestSnippet(t *testing.T) {
	if got := Snippet(File{Name: "shot [a].png", ContentType: "image/png"}, "https://u/1"); got != `![shot \[a\].png](https://u/1)` {
		t.Errorf("image snippet = %s", got)
	}
	if got := Snippet(File{Name: "repro.mov", ContentType: "video/quicktime"}, "https://u/2"); got != "[repro.mov](https://u/2)" {
		t.Errorf("video snippet = %s", got)
	}
}
