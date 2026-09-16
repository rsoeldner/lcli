package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func issueData(f *fakeLinear, identifier string) map[string]any {
	return map[string]any{"issue": map[string]any{
		"id":            "issue-uuid-1",
		"identifier":    identifier,
		"title":         "Login button broken",
		"url":           "https://linear.app/eng/issue/" + identifier,
		"description":   "Steps:\n\n![screenshot.png](" + f.srv.URL + "/files/shot-uuid)\n\nSee also https://example.com/not-linear.png",
		"priorityLabel": "High",
		"createdAt":     "2026-09-01T10:00:00.000Z",
		"updatedAt":     "2026-09-02T11:30:00.000Z",
		"state":         map[string]any{"name": "In Progress"},
		"team":          map[string]any{"key": "ENG"},
		"assignee":      map[string]any{"displayName": "robert"},
		"creator":       nil,
		"labels":        map[string]any{"nodes": []any{map[string]any{"name": "Bug"}, map[string]any{"name": "Frontend"}}},
		"attachments": map[string]any{"nodes": []any{map[string]any{
			"id": "att-1", "title": "PR #42", "subtitle": "open", "url": "https://github.com/x/y/pull/42", "sourceType": "github",
		}}},
	}}
}

func comment(id, body, created string, author map[string]any, parent any) map[string]any {
	c := map[string]any{
		"id": id, "body": body, "url": "https://linear.app/c/" + id, "createdAt": created,
		"editedAt": nil, "parentId": parent, "user": nil, "botActor": nil, "externalUser": nil,
	}
	for k, v := range author {
		c[k] = v
	}
	return c
}

// serveIssue installs an issue with two pages of comments (returned newest
// page first to exercise sorting and pagination).
func serveIssue(f *fakeLinear) {
	f.files["shot-uuid"] = fakeFile{ContentType: "image/png", Body: []byte("PNGDATA")}
	f.files["video-uuid"] = fakeFile{ContentType: "video/mp4", Body: []byte("not really a video")}
	f.on("IssueDetails", func(vars map[string]any) gqlResult {
		return gqlResult{Data: issueData(f, "ENG-123")}
	})
	f.on("IssueComments", func(vars map[string]any) gqlResult {
		if vars["after"] == nil {
			edited := comment("c-2", "Fixed in [repro.mp4]("+f.srv.URL+"/files/video-uuid) and ![again]("+f.srv.URL+"/files/shot-uuid)",
				"2026-09-03T09:00:00.000Z", map[string]any{"botActor": map[string]any{"name": "GitHub"}}, "c-1")
			edited["editedAt"] = "2026-09-03T09:05:00.000Z"
			return gqlResult{Data: map[string]any{"issue": map[string]any{"comments": map[string]any{
				"nodes":    []any{edited},
				"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "cursor-1"},
			}}}}
		}
		return gqlResult{Data: map[string]any{"issue": map[string]any{"comments": map[string]any{
			"nodes": []any{
				comment("c-1", "Can reproduce ![missing]("+f.srv.URL+"/files/gone-uuid)", "2026-09-02T08:00:00.000Z",
					map[string]any{"user": map[string]any{"displayName": "anna"}}, nil),
			},
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": "cursor-2"},
		}}}}
	})
}

func TestIssueMarkdownDownloadsMedia(t *testing.T) {
	f := newFakeLinear(t)
	serveIssue(f)
	app := newTestApp(t, f)
	r := run(t, app, "issue", "eng-123")
	wantCode(t, r, ExitOK)

	dir := filepath.Join(app.CacheDir, "lcli", "ENG-123")
	wantContains(t, r.Stdout,
		"# ENG-123: Login button broken",
		"- Account: eng · Team: ENG",
		"- State: In Progress · Priority: High",
		"- Assignee: robert · Creator: (none)",
		"- Labels: Bug, Frontend",
		"## Description\n\nSteps:",
		"## Attachments (1)\n\n- [PR #42](https://github.com/x/y/pull/42) (github) — open",
		"## Comments (2)",
		"- Author: GitHub (bot)",
		"- Reply to: c-1",
		"· Edited: 2026-09-03 09:05 UTC",
		"## Media (3)",
		"-> "+filepath.Join(dir, "shot-uuid.png")+" (image/png)",
		"-> "+filepath.Join(dir, "video-uuid.mp4")+" (video/mp4)",
		"/files/gone-uuid\n  error: GET ",
	)
	// Oldest comment first despite the API returning it on the second page.
	if strings.Index(r.Stdout, "### Comment c-1") > strings.Index(r.Stdout, "### Comment c-2") {
		t.Errorf("comments not sorted oldest first:\n%s", r.Stdout)
	}
	if strings.Contains(r.Stdout, "not-linear.png\n  ->") {
		t.Error("downloaded a non-Linear URL")
	}
	got, err := os.ReadFile(filepath.Join(dir, "shot-uuid.png"))
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("downloaded image = %q, %v", got, err)
	}
	// The screenshot appears twice but is downloaded once.
	n := 0
	for _, g := range f.fileGets {
		if g == "shot-uuid" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("shot downloaded %d times, want 1", n)
	}
	for _, c := range f.calls {
		if c.Auth != "eng-key" {
			t.Errorf("%s used key %q, want eng-key", c.Op, c.Auth)
		}
	}
	if pages := f.callsTo("IssueComments"); len(pages) != 2 || pages[1].Vars["after"] != "cursor-1" {
		t.Errorf("comment pagination calls = %+v", pages)
	}
}

func TestIssueJSONNoDownload(t *testing.T) {
	f := newFakeLinear(t)
	serveIssue(f)
	r := run(t, newTestApp(t, f), "issue", "https://linear.app/eng/issue/ENG-123/login", "--json", "--no-download")
	wantCode(t, r, ExitOK)
	var v issueView
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, r.Stdout)
	}
	if v.Identifier != "ENG-123" || v.Account != "eng" || len(v.Comments) != 2 || v.Comments[0].ID != "c-1" ||
		v.Comments[1].EditedAt == nil || v.Comments[1].ParentID != "c-1" || len(v.Media) != 0 || len(v.Labels) != 2 {
		t.Errorf("unexpected view: %+v", v)
	}
	if len(f.fileGets) != 0 {
		t.Errorf("--no-download fetched files: %v", f.fileGets)
	}
}

func TestIssueOutDirAndRouting(t *testing.T) {
	f := newFakeLinear(t)
	serveIssue(f)
	app := newTestApp(t, f)
	out := filepath.Join(t.TempDir(), "media")
	r := run(t, app, "issue", "ACME-5", "--out", out)
	wantCode(t, r, ExitOK)
	if _, err := os.Stat(filepath.Join(out, "shot-uuid.png")); err != nil {
		t.Errorf("--out not used: %v", err)
	}
	if c := f.callsTo("IssueDetails"); len(c) != 1 || c[0].Auth != "acme-key" || c[0].Vars["id"] != "ACME-5" {
		t.Errorf("IssueDetails calls = %+v, want acme-key for ACME-5", c)
	}
}

func TestIssueErrors(t *testing.T) {
	t.Run("unknown team", func(t *testing.T) {
		r := run(t, newTestApp(t, nil), "issue", "XYZ-1")
		wantCode(t, r, ExitUsage)
		wantContains(t, r.Stderr, `unknown team key "XYZ"; known: ACME (acme); ENG, OPS (eng)`)
	})
	t.Run("bad identifier", func(t *testing.T) {
		r := run(t, newTestApp(t, nil), "issue", "123")
		wantCode(t, r, ExitUsage)
	})
	t.Run("flag conflict", func(t *testing.T) {
		r := run(t, newTestApp(t, nil), "issue", "ENG-1", "--no-download", "--frames", "2")
		wantCode(t, r, ExitUsage)
	})
	t.Run("not found", func(t *testing.T) {
		f := newFakeLinear(t)
		f.on("IssueDetails", func(map[string]any) gqlResult {
			return gqlResult{Status: http.StatusOK, Errors: []map[string]any{{
				"message":    "Entity not found: Issue",
				"extensions": map[string]any{"code": "INVALID_INPUT", "userPresentableMessage": "Could not find referenced Issue."},
			}}}
		})
		r := run(t, newTestApp(t, f), "issue", "ENG-999")
		wantCode(t, r, ExitAPI)
		wantContains(t, r.Stderr, "linear (account eng): Entity not found: Issue (Could not find referenced Issue.)")
	})
	t.Run("missing key env", func(t *testing.T) {
		app := newTestApp(t, nil)
		t.Setenv("LCLI_TEST_ENG_KEY", "")
		r := run(t, app, "issue", "ENG-1")
		wantCode(t, r, ExitConfig)
	})
}

func TestCommentList(t *testing.T) {
	f := newFakeLinear(t)
	serveIssue(f)
	app := newTestApp(t, f)
	r := run(t, app, "comment", "list", "ENG-123")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "# Comments on ENG-123 (2)", "### Comment c-1", "- Author: anna", "### Comment c-2")
	if len(f.callsTo("IssueDetails")) != 0 {
		t.Error("comment list should not fetch issue details")
	}

	r = run(t, app, "comment", "list", "ENG-123", "--json")
	wantCode(t, r, ExitOK)
	var cs []commentView
	if err := json.Unmarshal([]byte(r.Stdout), &cs); err != nil || len(cs) != 2 {
		t.Fatalf("comment list --json = %v, %v", cs, err)
	}
}

func TestIssueFramesWiring(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	f := newFakeLinear(t)
	serveIssue(f)
	video := filepath.Join(t.TempDir(), "clip.mp4")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=1:size=32x32:rate=10", "-pix_fmt", "yuv420p", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate video: %v: %s", err, out)
	}
	body, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	f.files["video-uuid"] = fakeFile{ContentType: "video/mp4", Body: body}
	app := newTestApp(t, f)
	r := run(t, app, "issue", "ENG-123", "--frames", "2")
	wantCode(t, r, ExitOK)
	dir := filepath.Join(app.CacheDir, "lcli", "ENG-123")
	wantContains(t, r.Stdout,
		"frame: "+filepath.Join(dir, "video-uuid.mp4.frame-1.jpg"),
		"frame: "+filepath.Join(dir, "video-uuid.mp4.frame-2.jpg"))
	if strings.Contains(r.Stdout, "shot-uuid.png.frame") {
		t.Error("extracted frames from an image")
	}
}
