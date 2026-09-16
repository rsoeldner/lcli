package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveWrites installs handlers for IssueRef, CommentByID, FileUpload,
// CreateComment and UpdateComment. Comment "c-other" belongs to ENG-9.
func serveWrites(f *fakeLinear) {
	f.on("IssueRef", func(vars map[string]any) gqlResult {
		id := strings.ToUpper(vars["id"].(string))
		return gqlResult{Data: map[string]any{"issue": map[string]any{
			"id": "uuid-" + id, "identifier": id, "url": "https://linear.app/eng/issue/" + id,
		}}}
	})
	f.on("CommentByID", func(vars map[string]any) gqlResult {
		id := vars["id"].(string)
		issue := map[string]any{"identifier": "ENG-123"}
		if id == "c-other" {
			issue = map[string]any{"identifier": "ENG-9"}
		}
		return gqlResult{Data: map[string]any{"comment": map[string]any{"id": id, "body": "Original text.\n", "issue": issue}}}
	})
	n := 0
	f.on("FileUpload", func(vars map[string]any) gqlResult {
		n++
		name := vars["filename"].(string)
		return gqlResult{Data: map[string]any{"fileUpload": map[string]any{
			"success": true,
			"uploadFile": map[string]any{
				"uploadUrl": f.srv.URL + "/upload/" + name,
				"assetUrl":  "https://uploads.linear.app/org/asset-" + name,
				"headers":   []any{map[string]any{"key": "x-goog-meta-trace", "value": "t1"}},
			},
		}}}
	})
	f.on("CreateComment", func(vars map[string]any) gqlResult {
		return gqlResult{Data: map[string]any{"commentCreate": map[string]any{
			"success": true, "comment": map[string]any{"id": "c-new", "url": "https://linear.app/c/c-new"},
		}}}
	})
	f.on("UpdateComment", func(vars map[string]any) gqlResult {
		return gqlResult{Data: map[string]any{"commentUpdate": map[string]any{
			"success": true, "comment": map[string]any{"id": vars["id"], "url": "https://linear.app/c/" + vars["id"].(string)},
		}}}
	})
}

func writeFiles(t *testing.T) (png, mp4 string) {
	t.Helper()
	dir := t.TempDir()
	png = filepath.Join(dir, "before [1].png")
	mp4 = filepath.Join(dir, "repro.mp4")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nimage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp4, []byte("videobytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return png, mp4
}

func TestCommentAddWithAttachmentsAndReply(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	png, mp4 := writeFiles(t)
	r := run(t, newTestApp(t, f), "comment", "add", "eng-123", "-m", "Root cause found.\n", "--attach", png, "--attach", mp4, "--reply-to", "c-1")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Created comment c-new on ENG-123\nURL: https://linear.app/c/c-new")

	ups := f.callsTo("FileUpload")
	if len(ups) != 2 || ups[0].Vars["filename"] != "before [1].png" || ups[0].Vars["contentType"] != "image/png" ||
		ups[0].Vars["size"] != float64(13) || ups[1].Vars["contentType"] != "video/mp4" {
		t.Errorf("FileUpload calls = %+v", ups)
	}
	if len(f.puts) != 2 {
		t.Fatalf("PUTs = %d, want 2", len(f.puts))
	}
	put := f.puts[0]
	if string(put.Body) != "\x89PNG\r\n\x1a\nimage" || put.Header.Get("Content-Type") != "image/png" ||
		put.Header.Get("X-Goog-Meta-Trace") != "t1" || put.Header.Get("Cache-Control") != "public, max-age=31536000" || put.HasAuth {
		t.Errorf("PUT = %+v", put)
	}
	creates := f.callsTo("CreateComment")
	if len(creates) != 1 {
		t.Fatalf("CreateComment calls = %d", len(creates))
	}
	wantBody := "Root cause found.\n\n![before \\[1\\].png](https://uploads.linear.app/org/asset-before [1].png)\n[repro.mp4](https://uploads.linear.app/org/asset-repro.mp4)"
	if v := creates[0].Vars; v["issueId"] != "uuid-ENG-123" || v["body"] != wantBody || v["parentId"] != "c-1" {
		t.Errorf("CreateComment vars = %#v\nwant body %q", v, wantBody)
	}
	for _, c := range f.calls {
		if c.Auth != "eng-key" {
			t.Errorf("%s used key %q", c.Op, c.Auth)
		}
	}
}

func TestCommentAddBodyFromStdin(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	app := newTestApp(t, f)
	app.Stdin = strings.NewReader("## Analysis\n\nIt's the `$PATH`.\n")
	r := run(t, app, "comment", "add", "ACME-4", "--body-file", "-")
	wantCode(t, r, ExitOK)
	c := f.callsTo("CreateComment")
	if len(c) != 1 || c[0].Vars["body"] != "## Analysis\n\nIt's the `$PATH`." || c[0].Vars["parentId"] != nil || c[0].Auth != "acme-key" {
		t.Errorf("CreateComment = %+v", c)
	}
}

func TestCommentAddBodyFromFile(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	path := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(path, []byte("from file"), 0o600)
	r := run(t, newTestApp(t, f), "comment", "add", "ENG-1", "--body-file", path)
	wantCode(t, r, ExitOK)
	if c := f.callsTo("CreateComment"); len(c) != 1 || c[0].Vars["body"] != "from file" {
		t.Errorf("CreateComment = %+v", c)
	}
}

func TestCommentAddDryRun(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	png, _ := writeFiles(t)
	r := run(t, newTestApp(t, f), "comment", "add", "ENG-123", "-m", "hello", "--attach", png, "--dry-run")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Dry run: would add a comment to ENG-123 (account eng). Nothing was uploaded or posted.",
		"- would upload "+png+" (image/png, 13 bytes)",
		"--- body ---\nhello\n\n![before \\[1\\].png](<asset URL after upload>)\n--- end ---")
	if len(f.callsTo("FileUpload"))+len(f.callsTo("CreateComment"))+len(f.puts) != 0 {
		t.Error("dry run uploaded or posted")
	}
}

func TestCommentAddErrors(t *testing.T) {
	png, _ := writeFiles(t)
	for name, tc := range map[string]struct {
		args []string
		code int
		msg  string
	}{
		"both body sources": {[]string{"-m", "x", "--body-file", "y"}, ExitUsage, "either -m or --body-file"},
		"nothing":           {[]string{"-m", "  "}, ExitUsage, "nothing to post"},
		"missing file":      {[]string{"-m", "x", "--attach", "/nonexistent.png"}, ExitUsage, "cannot upload"},
		"directory":         {[]string{"-m", "x", "--attach", filepath.Dir(png)}, ExitUsage, "not a regular file"},
		"missing body file": {[]string{"--body-file", "/nonexistent.md"}, ExitUsage, "read --body-file"},
		"reply other issue": {[]string{"-m", "x", "--reply-to", "c-other"}, ExitUsage, "comment c-other belongs to ENG-9, not ENG-123"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeLinear(t)
			serveWrites(f)
			r := run(t, newTestApp(t, f), append([]string{"comment", "add", "ENG-123"}, tc.args...)...)
			wantCode(t, r, tc.code)
			wantContains(t, r.Stderr, tc.msg)
			if len(f.callsTo("CreateComment"))+len(f.callsTo("FileUpload")) != 0 {
				t.Error("posted or uploaded despite error")
			}
		})
	}
}

func TestUploadFailures(t *testing.T) {
	png, mp4 := writeFiles(t)
	t.Run("no upload url", func(t *testing.T) {
		f := newFakeLinear(t)
		serveWrites(f)
		f.on("FileUpload", func(map[string]any) gqlResult {
			return gqlResult{Data: map[string]any{"fileUpload": map[string]any{"success": false, "uploadFile": nil}}}
		})
		r := run(t, newTestApp(t, f), "comment", "add", "ENG-1", "-m", "x", "--attach", png)
		wantCode(t, r, ExitAPI)
		wantContains(t, r.Stderr, "Linear returned no upload URL")
		if len(f.callsTo("CreateComment")) != 0 || len(f.puts) != 0 {
			t.Error("continued after failed upload preparation")
		}
	})
	t.Run("storage rejects put", func(t *testing.T) {
		f := newFakeLinear(t)
		serveWrites(f)
		f.on("FileUpload", func(vars map[string]any) gqlResult {
			return gqlResult{Data: map[string]any{"fileUpload": map[string]any{"success": true, "uploadFile": map[string]any{
				"uploadUrl": f.srv.URL + "/files/not-a-put-target", "assetUrl": "https://uploads.linear.app/x", "headers": []any{},
			}}}}
		})
		r := run(t, newTestApp(t, f), "upload", "ENG-1", png)
		wantCode(t, r, ExitAPI)
		wantContains(t, r.Stderr, "storage returned 401")
	})
	t.Run("partial upload prints finished snippets", func(t *testing.T) {
		f := newFakeLinear(t)
		serveWrites(f)
		calls := 0
		f.on("FileUpload", func(vars map[string]any) gqlResult {
			calls++
			if calls == 2 {
				return gqlResult{Status: http.StatusInternalServerError, Errors: []map[string]any{{"message": "quota exceeded"}}}
			}
			return gqlResult{Data: map[string]any{"fileUpload": map[string]any{"success": true, "uploadFile": map[string]any{
				"uploadUrl": f.srv.URL + "/upload/a", "assetUrl": "https://uploads.linear.app/a", "headers": []any{},
			}}}}
		})
		r := run(t, newTestApp(t, f), "upload", "ENG-1", png, mp4)
		wantCode(t, r, ExitAPI)
		wantContains(t, r.Stdout, "![before \\[1\\].png](https://uploads.linear.app/a)")
		wantContains(t, r.Stderr, "quota exceeded")
	})
}

func TestCommentEditReplace(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	path := filepath.Join(t.TempDir(), "fixed.md")
	os.WriteFile(path, []byte("Corrected analysis.\n"), 0o600)
	r := run(t, newTestApp(t, f), "comment", "edit", "ENG-123", "c-1", "--body-file", path)
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Updated comment c-1 on ENG-123\nURL: https://linear.app/c/c-1")
	if u := f.callsTo("UpdateComment"); len(u) != 1 || u[0].Vars["id"] != "c-1" || u[0].Vars["body"] != "Corrected analysis." {
		t.Errorf("UpdateComment = %+v", u)
	}
}

func TestCommentEditAppendWithAttachment(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	png, _ := writeFiles(t)
	r := run(t, newTestApp(t, f), "comment", "edit", "https://linear.app/eng/issue/ENG-123/x", "c-1", "--append", "-m", "Update: fixed.", "--attach", png)
	wantCode(t, r, ExitOK)
	want := "Original text.\n\nUpdate: fixed.\n\n![before \\[1\\].png](https://uploads.linear.app/org/asset-before [1].png)"
	if u := f.callsTo("UpdateComment"); len(u) != 1 || u[0].Vars["body"] != want {
		t.Errorf("UpdateComment = %+v\nwant body %q", u, want)
	}

	f2 := newFakeLinear(t)
	serveWrites(f2)
	r = run(t, newTestApp(t, f2), "comment", "edit", "ENG-123", "c-1", "--append", "--attach", png)
	wantCode(t, r, ExitOK)
	if u := f2.callsTo("UpdateComment"); len(u) != 1 || !strings.HasPrefix(u[0].Vars["body"].(string), "Original text.\n\n![") {
		t.Errorf("attachment-only append = %+v", u)
	}
}

func TestCommentEditErrorsAndDryRun(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		msg  string
	}{
		"no body no append": {[]string{"c-1"}, "give the complete new text"},
		"empty replace":     {[]string{"c-1", "-m", " "}, "refusing to replace the comment with an empty body"},
		"empty append":      {[]string{"c-1", "--append", "-m", ""}, "nothing to append"},
		"wrong issue":       {[]string{"c-other", "-m", "x"}, "comment c-other belongs to ENG-9, not ENG-123"},
		"missing comment":   {[]string{}, "accepts 2 arg(s)"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeLinear(t)
			serveWrites(f)
			r := run(t, newTestApp(t, f), append([]string{"comment", "edit", "ENG-123"}, tc.args...)...)
			wantCode(t, r, ExitUsage)
			wantContains(t, r.Stderr, tc.msg)
			if len(f.callsTo("UpdateComment")) != 0 {
				t.Error("updated despite error")
			}
		})
	}

	f := newFakeLinear(t)
	serveWrites(f)
	r := run(t, newTestApp(t, f), "comment", "edit", "ENG-123", "c-1", "--append", "-m", "more", "--dry-run")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Dry run: would update comment c-1 on ENG-123 (account eng)", "--- body ---\nOriginal text.\n\nmore\n--- end ---")
	if len(f.callsTo("UpdateComment")) != 0 {
		t.Error("dry run updated")
	}
}

func TestUploadCommand(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	png, mp4 := writeFiles(t)
	app := newTestApp(t, f)
	r := run(t, app, "upload", "ENG-123", png, mp4)
	wantCode(t, r, ExitOK)
	if r.Stdout != "![before \\[1\\].png](https://uploads.linear.app/org/asset-before [1].png)\n[repro.mp4](https://uploads.linear.app/org/asset-repro.mp4)\n" {
		t.Errorf("stdout = %q", r.Stdout)
	}
	wantContains(t, r.Stderr, "Note: not visible on ENG-123 until embedded")
	if len(f.callsTo("IssueRef")) != 1 || len(f.puts) != 2 {
		t.Error("expected issue check and two uploads")
	}

	f2 := newFakeLinear(t)
	serveWrites(f2)
	r = run(t, newTestApp(t, f2), "upload", "ENG-123", png, "--dry-run")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Dry run: would upload to account eng for ENG-123", png+" (image/png, 13 bytes)")
	if len(f2.callsTo("FileUpload")) != 0 || len(f2.puts) != 0 {
		t.Error("dry run uploaded")
	}

	r = run(t, newTestApp(t, nil), "upload", "ENG-123")
	wantCode(t, r, ExitUsage)

	f3 := newFakeLinear(t)
	f3.on("IssueRef", func(map[string]any) gqlResult {
		return gqlResult{Errors: []map[string]any{{"message": "Entity not found: Issue"}}}
	})
	r = run(t, newTestApp(t, f3), "upload", "ENG-404", png)
	wantCode(t, r, ExitAPI)
	if len(f3.callsTo("FileUpload")) != 0 {
		t.Error("uploaded for a missing issue")
	}
}
