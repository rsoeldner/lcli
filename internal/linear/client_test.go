package linear

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve returns a client whose every request gets status and body.
func serve(t *testing.T, status int, body string, gotAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantAuth bool
		wantMsg  string
	}{
		{"401", 401, `{"errors":[{"message":"nope"}]}`, true, "nope"},
		{"403 non-json", 403, `forbidden`, true, "forbidden"},
		{"400 auth code", 400, `{"errors":[{"message":"Authentication required, not authenticated","extensions":{"code":"AUTHENTICATION_ERROR"}}]}`, true, "Authentication required"},
		{"200 auth type", 200, `{"errors":[{"message":"bad key","extensions":{"type":"authentication error"}}],"data":null}`, true, "bad key"},
		{"200 not found", 200, `{"errors":[{"message":"Entity not found: Issue","extensions":{"code":"INVALID_INPUT","userPresentableMessage":"Could not find referenced Issue."}}],"data":null}`, false, "Entity not found: Issue (Could not find referenced Issue.)"},
		{"500", 500, `{"errors":[{"message":"boom"}]}`, false, "boom"},
		{"502 html", 502, "<html>" + strings.Repeat("x", 2000) + "</html>", false, "(truncated)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var auth string
			srv := serve(t, tc.status, tc.body, &auth)
			_, err := Viewer(context.Background(), NewClient(srv.URL, "lin_api_k", srv.Client()))
			if err == nil {
				t.Fatal("expected error")
			}
			if auth != "lin_api_k" {
				t.Errorf("Authorization = %q, want raw key without Bearer", auth)
			}
			if got := IsAuthError(err); got != tc.wantAuth {
				t.Errorf("IsAuthError = %v, want %v (err: %v)", got, tc.wantAuth, err)
			}
			msg := Message(err)
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("Message = %q, want containing %q", msg, tc.wantMsg)
			}
			if len(msg) > 700 {
				t.Errorf("Message not bounded: %d bytes", len(msg))
			}
		})
	}
	if IsAuthError(errors.New("dial tcp: connection refused")) {
		t.Error("network error classified as auth")
	}
}
