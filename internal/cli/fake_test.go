package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gqlCall records one GraphQL request received by fakeLinear.
type gqlCall struct {
	Op   string
	Auth string
	Vars map[string]any
}

// gqlResult is what a fake operation handler returns: data, or GraphQL
// errors, optionally with a non-200 status.
type gqlResult struct {
	Data   any
	Errors []map[string]any
	Status int
}

type fakeFile struct {
	ContentType string
	Body        []byte
}

type fakePut struct {
	Path    string
	Header  http.Header
	Body    []byte
	HasAuth bool
}

// fakeLinear serves Linear's GraphQL endpoint at /graphql, uploaded files at
// /files/<name> (requiring the API key) and signed upload targets at
// /upload/<name>.
type fakeLinear struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	ops      map[string]func(vars map[string]any) gqlResult
	calls    []gqlCall
	files    map[string]fakeFile
	fileGets []string
	puts     []fakePut
}

func newFakeLinear(t *testing.T) *fakeLinear {
	f := &fakeLinear{t: t, ops: map[string]func(map[string]any) gqlResult{}, files: map[string]fakeFile{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", f.serveGraphQL)
	mux.HandleFunc("/files/", f.serveFile)
	mux.HandleFunc("/upload/", f.serveUpload)
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLinear) on(op string, h func(vars map[string]any) gqlResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops[op] = h
}

func (f *fakeLinear) callsTo(op string) []gqlCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gqlCall
	for _, c := range f.calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeLinear) host() string {
	u, _ := url.Parse(f.srv.URL)
	return u.Host
}

func (f *fakeLinear) serveGraphQL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, gqlCall{Op: req.OperationName, Auth: r.Header.Get("Authorization"), Vars: req.Variables})
	h := f.ops[req.OperationName]
	f.mu.Unlock()
	if h == nil {
		f.t.Errorf("fake linear: unexpected operation %s", req.OperationName)
		http.Error(w, "unexpected operation", http.StatusInternalServerError)
		return
	}
	res := h(req.Variables)
	w.Header().Set("Content-Type", "application/json")
	if res.Status != 0 {
		w.WriteHeader(res.Status)
	}
	body := map[string]any{}
	if res.Errors != nil {
		body["errors"] = res.Errors
	} else {
		body["data"] = res.Data
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeLinear) serveFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/files/")
	f.mu.Lock()
	f.fileGets = append(f.fileGets, name)
	file, ok := f.files[name]
	f.mu.Unlock()
	if r.Header.Get("Authorization") == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", file.ContentType)
	_, _ = w.Write(file.Body)
}

func (f *fakeLinear) serveUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "PUT only", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.puts = append(f.puts, fakePut{Path: r.URL.Path, Header: r.Header.Clone(), Body: body, HasAuth: r.Header.Get("Authorization") != ""})
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

const testConfig = `
[accounts.eng]
teams = ["ENG", "ops"]
key_env = "LCLI_TEST_ENG_KEY"

[accounts.acme]
teams = ["ACME"]
key_cmd = "printf 'acme-key\n'"
`

// newTestApp returns an App pointed at f with a two-account config.
func newTestApp(t *testing.T, f *fakeLinear) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCLI_TEST_ENG_KEY", "eng-key")
	app := &App{
		Stdin:       strings.NewReader(""),
		ConfigPath:  cfg,
		HTTP:        http.DefaultClient,
		UploadHosts: []string{"uploads.linear.app"},
		CacheDir:    filepath.Join(dir, "cache"),
	}
	if f != nil {
		app.Endpoint = f.srv.URL + "/graphql"
		app.HTTP = f.srv.Client()
		app.UploadHosts = []string{f.host()}
	}
	return app
}

type runResult struct {
	Code   int
	Stdout string
	Stderr string
}

func run(t *testing.T, app *App, args ...string) runResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app.Stdout, app.Stderr = &stdout, &stderr
	code := app.Run(context.Background(), args)
	return runResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

func wantCode(t *testing.T, r runResult, code int) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.Code, code, r.Stdout, r.Stderr)
	}
}

func wantContains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("output missing %q\n--- output ---\n%s", sub, s)
		}
	}
}
