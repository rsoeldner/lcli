// Package ident parses Linear issue identifiers and issue URLs.
package ident

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var identRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)-([0-9]+)$`)

// ID is a human-readable issue identifier such as ENG-123.
type ID struct {
	Team   string
	Number string
}

func (id ID) String() string { return id.Team + "-" + id.Number }

// Parse accepts "ENG-123" (any case) or a linear.app issue URL such as
// https://linear.app/acme/issue/ENG-123/some-title.
func Parse(s string) (ID, error) {
	s = strings.TrimSpace(s)
	raw := s
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil || (u.Host != "linear.app" && u.Host != "www.linear.app") {
			return ID{}, fmt.Errorf("%q is not a linear.app issue URL", raw)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 3 || parts[1] != "issue" {
			return ID{}, fmt.Errorf("%q is not a linear.app issue URL", raw)
		}
		s = parts[2]
	}
	m := identRe.FindStringSubmatch(s)
	if m == nil {
		return ID{}, fmt.Errorf("%q is not an issue identifier like ENG-123 or a linear.app issue URL", raw)
	}
	return ID{Team: strings.ToUpper(m[1]), Number: m[2]}, nil
}
