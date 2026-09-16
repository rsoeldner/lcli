package cli

import (
	"fmt"
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
		issue := map[string]any{"id": "uuid-ENG-123", "identifier": "ENG-123"}
		switch id {
		case "c-other":
			issue = map[string]any{"id": "uuid-ENG-9", "identifier": "ENG-9"}
		case "c-same-key-other-uuid":
			issue = map[string]any{"id": "uuid-elsewhere", "identifier": "ENG-123"}
		}
		return gqlResult{Data: map[string]any{"comment": map[string]any{"id": id, "body": "Original text.\n", "issue": issue}}}
	})
	n := 0
	f.on("FileUpload", func(vars map[string]any) gqlResult {
		n++
		return gqlResult{Data: map[string]any{"fileUpload": map[string]any{
			"success": true,
			"uploadFile": map[string]any{
				"uploadUrl": fmt.Sprintf("%s/upload/%d", f.srv.URL, n),
				"assetUrl":  fmt.Sprintf("https://uploads.linear.app/org-uuid/file-%d", n),
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
	for i, want := range []struct{ body, ctype string }{{"\x89PNG\r\n\x1a\nimage", "image/png"}, {"videobytes", "video/mp4"}} {
		put := f.puts[i]
		if string(put.Body) != want.body || put.Header.Get("Content-Type") != want.ctype || put.Path != fmt.Sprintf("/upload/%d", i+1) ||
			put.Header.Get("X-Goog-Meta-Trace") != "t1" || put.Header.Get("Cache-Control") != "public, max-age=31536000" || put.HasAuth {
			t.Errorf("PUT %d = %+v", i, put)
		}
	}
	creates := f.callsTo("CreateComment")
	if len(creates) != 1 {
		t.Fatalf("CreateComment calls = %d", len(creates))
	}
	wantBody := "Root cause found.\n\n![before \\[1\\].png](https://uploads.linear.app/org-uuid/file-1)\n[repro.mp4](https://uploads.linear.app/org-uuid/file-2)"
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
		"--- body ---\nhello\n\n![before \\[1\\].png](ASSET_URL_AFTER_UPLOAD)\n--- end ---")
	if len(f.callsTo("FileUpload"))+len(f.callsTo("CreateComment"))+len(f.puts) != 0 {
		t.Error("dry run uploaded or posted")
	}
}

func TestCommentAddErrors(t *testing.T) {
	png, _ := writeFiles(t)
	for name, tc := range map[string]struct {
		args  []string
		msg   string
		local bool // must fail before any API call
	}{
		"both body sources": {[]string{"-m", "x", "--body-file", "y", "--attach", png}, "either -m or --body-file", true},
		"nothing":           {[]string{"-m", "  "}, "nothing to post", true},
		"missing file":      {[]string{"-m", "x", "--attach", png, "--attach", "/nonexistent.png"}, "cannot upload", true},
		"directory":         {[]string{"-m", "x", "--attach", filepath.Dir(png)}, "not a regular file", true},
		"missing body file": {[]string{"--body-file", "/nonexistent.md", "--attach", png}, "read --body-file", true},
		"flag as message":   {[]string{"-m", "--dry-run", "--attach", png}, `-m value "--dry-run" is one of this command's flags`, true},
		"short flag as msg": {[]string{"-m", "-m"}, `-m value "-m" is one of this command's flags`, true},
		"reply other issue": {[]string{"-m", "x", "--attach", png, "--reply-to", "c-other"}, "comment c-other belongs to ENG-9, not ENG-123", false},
		"reply same key other uuid": {[]string{"-m", "x", "--attach", png, "--reply-to", "c-same-key-other-uuid"},
			"comment c-same-key-other-uuid belongs to ENG-123, not ENG-123", false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeLinear(t)
			serveWrites(f)
			r := run(t, newTestApp(t, f), append([]string{"comment", "add", "ENG-123"}, tc.args...)...)
			wantCode(t, r, ExitUsage)
			wantContains(t, r.Stderr, tc.msg)
			if len(f.callsTo("CreateComment"))+len(f.callsTo("FileUpload"))+len(f.puts) != 0 {
				t.Error("posted or uploaded despite error")
			}
			if tc.local && len(f.calls) != 0 {
				t.Errorf("API called before local validation failed: %+v", f.calls)
			}
		})
	}
}

func TestCommentAddLiteralFlagLikeMessage(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	r := run(t, newTestApp(t, f), "comment", "add", "ENG-123", "-m=--dry-run")
	wantCode(t, r, ExitOK)
	if c := f.callsTo("CreateComment"); len(c) != 1 || c[0].Vars["body"] != "--dry-run" {
		t.Errorf("CreateComment = %+v", c)
	}
}

func TestCommentAddReportsUploadsWhenPostingFails(t *testing.T) {
	png, mp4 := writeFiles(t)
	f := newFakeLinear(t)
	serveWrites(f)
	f.on("CreateComment", func(map[string]any) gqlResult {
		return gqlResult{Errors: []map[string]any{{"message": "comment too long"}}}
	})
	r := run(t, newTestApp(t, f), "comment", "add", "ENG-123", "-m", "x", "--attach", png, "--attach", mp4)
	wantCode(t, r, ExitAPI)
	wantContains(t, r.Stderr, "Already uploaded (embed these instead of uploading again):\n"+
		"![before \\[1\\].png](https://uploads.linear.app/org-uuid/file-1)\n[repro.mp4](https://uploads.linear.app/org-uuid/file-2)", "comment too long")

	f2 := newFakeLinear(t)
	serveWrites(f2)
	f2.on("UpdateComment", func(map[string]any) gqlResult {
		return gqlResult{Data: map[string]any{"commentUpdate": map[string]any{"success": false, "comment": map[string]any{"id": "c-1", "url": ""}}}}
	})
	r = run(t, newTestApp(t, f2), "comment", "edit", "ENG-123", "c-1", "--append", "--attach", png)
	wantCode(t, r, ExitAPI)
	wantContains(t, r.Stderr, "Already uploaded", "file-1)", "commentUpdate reported no success")
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
	t.Run("unsuccessful with upload file", func(t *testing.T) {
		f := newFakeLinear(t)
		serveWrites(f)
		f.on("FileUpload", func(map[string]any) gqlResult {
			return gqlResult{Data: map[string]any{"fileUpload": map[string]any{"success": false, "uploadFile": map[string]any{
				"uploadUrl": f.srv.URL + "/upload/x", "assetUrl": "https://uploads.linear.app/x", "headers": []any{},
			}}}}
		})
		r := run(t, newTestApp(t, f), "upload", "ENG-1", png)
		wantCode(t, r, ExitAPI)
		if len(f.puts) != 0 {
			t.Error("PUT despite unsuccessful fileUpload")
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
	want := "Original text.\n\nUpdate: fixed.\n\n![before \\[1\\].png](https://uploads.linear.app/org-uuid/file-1)"
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
	png, _ := writeFiles(t)
	for name, tc := range map[string]struct {
		args  []string
		msg   string
		local bool
	}{
		"no body no append": {[]string{"c-1", "--attach", png}, "give the complete new text", true},
		"empty replace":     {[]string{"c-1", "-m", " "}, "refusing to replace the comment with an empty body", true},
		"empty append":      {[]string{"c-1", "--append", "-m", ""}, "nothing to append", true},
		"flag as message":   {[]string{"c-1", "-m", "--append"}, `-m value "--append" is one of this command's flags`, true},
		"wrong issue":       {[]string{"c-other", "-m", "x", "--attach", png}, "comment c-other belongs to ENG-9, not ENG-123", false},
		"missing comment":   {[]string{}, "accepts 2 arg(s)", true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeLinear(t)
			serveWrites(f)
			r := run(t, newTestApp(t, f), append([]string{"comment", "edit", "ENG-123"}, tc.args...)...)
			wantCode(t, r, ExitUsage)
			wantContains(t, r.Stderr, tc.msg)
			if len(f.callsTo("UpdateComment"))+len(f.callsTo("FileUpload"))+len(f.puts) != 0 {
				t.Error("updated or uploaded despite error")
			}
			if tc.local && len(f.calls) != 0 {
				t.Errorf("API called before local validation failed: %+v", f.calls)
			}
		})
	}

	f := newFakeLinear(t)
	serveWrites(f)
	r := run(t, newTestApp(t, f), "comment", "edit", "ENG-123", "c-1", "--append", "-m", "more", "--attach", png, "--dry-run")
	wantCode(t, r, ExitOK)
	wantContains(t, r.Stdout, "Dry run: would update comment c-1 on ENG-123 (account eng)",
		"--- body ---\nOriginal text.\n\nmore\n\n![before \\[1\\].png](ASSET_URL_AFTER_UPLOAD)\n--- end ---")
	if len(f.callsTo("UpdateComment"))+len(f.callsTo("FileUpload"))+len(f.puts) != 0 {
		t.Error("dry run updated or uploaded")
	}
}

func TestUploadCommand(t *testing.T) {
	f := newFakeLinear(t)
	serveWrites(f)
	png, mp4 := writeFiles(t)
	app := newTestApp(t, f)
	r := run(t, app, "upload", "ENG-123", png, mp4)
	wantCode(t, r, ExitOK)
	if r.Stdout != "![before \\[1\\].png](https://uploads.linear.app/org-uuid/file-1)\n[repro.mp4](https://uploads.linear.app/org-uuid/file-2)\n" {
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
