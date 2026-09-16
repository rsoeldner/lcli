package linear

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// DefaultEndpoint is Linear's GraphQL API.
const DefaultEndpoint = "https://api.linear.app/graphql"

// NewClient returns a client authenticating with a personal API key.
// Linear expects the raw key in the Authorization header, without "Bearer".
func NewClient(endpoint, apiKey string, httpClient *http.Client) graphql.Client {
	return graphql.NewClient(endpoint, &authDoer{key: apiKey, next: httpClient})
}

type authDoer struct {
	key  string
	next *http.Client
}

func (d *authDoer) Do(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", d.key)
	return d.next.Do(r)
}

// IsAuthError reports whether err means the API key was rejected.
func IsAuthError(err error) bool {
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden {
			return true
		}
		if hasAuthCode(httpErr.Response.Errors) {
			return true
		}
	}
	var list gqlerror.List
	if errors.As(err, &list) {
		return hasAuthCode(list)
	}
	return false
}

func hasAuthCode(list gqlerror.List) bool {
	for _, e := range list {
		for _, k := range []string{"code", "type"} {
			if v, ok := e.Extensions[k].(string); ok && strings.Contains(strings.ToLower(v), "authentication") {
				return true
			}
		}
	}
	return false
}

// Message extracts the human-readable GraphQL error messages from err.
func Message(err error) string {
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) && len(httpErr.Response.Errors) > 0 {
		return messages(httpErr.Response.Errors)
	}
	var list gqlerror.List
	if errors.As(err, &list) {
		return messages(list)
	}
	return err.Error()
}

func messages(list gqlerror.List) string {
	msgs := make([]string, 0, len(list))
	for _, e := range list {
		msg := e.Message
		if upm, ok := e.Extensions["userPresentableMessage"].(string); ok && upm != "" && upm != msg {
			msg += " (" + upm + ")"
		}
		msgs = append(msgs, msg)
	}
	return strings.Join(msgs, "; ")
}
