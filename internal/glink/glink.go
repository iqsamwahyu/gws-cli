// Package glink parses the Google Workspace links a user pastes into gws.
package glink

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Kind string

const (
	KindDoc     Kind = "doc"
	KindSheet   Kind = "sheet"
	KindFile    Kind = "file"
	KindFolder  Kind = "folder"
	KindUnknown Kind = "unknown"
)

// Ref is one Google Workspace item named by a link or by a bare file id.
type Ref struct {
	Kind Kind
	ID   string
	GID  int64 // sheet tab id from the URL fragment, -1 when absent
	Raw  string
}

var (
	bareID = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	linkID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Parse reads a Docs, Sheets, Slides or Drive link, or a bare file id.
func Parse(s string) (Ref, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Ref{}, fmt.Errorf("empty link: give a Google Docs, Sheets or Drive URL, or a file id")
	}

	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		if !bareID.MatchString(raw) {
			return Ref{}, fmt.Errorf("%q is not a Google link and not a file id (a file id is 20 or more characters of A-Z a-z 0-9 _ -)", raw)
		}
		return Ref{Kind: KindUnknown, ID: raw, GID: -1, Raw: raw}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Ref{}, fmt.Errorf("parse link %q: %w", raw, err)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "docs.google.com" && host != "drive.google.com" {
		return Ref{}, fmt.Errorf("host %q is not docs.google.com or drive.google.com", u.Hostname())
	}

	segs := segments(u.Path)
	ref := Ref{Kind: KindUnknown, GID: gid(u), Raw: raw}

	switch {
	case has(segs, "document"):
		ref.Kind = KindDoc
	case has(segs, "spreadsheets"):
		ref.Kind = KindSheet
	case has(segs, "presentation"), has(segs, "file"):
		ref.Kind = KindFile
	case has(segs, "folders"):
		ref.Kind = KindFolder
	}

	if after(segs, "d") == "e" {
		return Ref{}, fmt.Errorf("%q is a published link and carries no file id: open the item in Google and copy the URL from the address bar", raw)
	}

	switch {
	case after(segs, "d") != "":
		ref.ID = after(segs, "d")
	case after(segs, "folders") != "":
		ref.ID = after(segs, "folders")
	default:
		ref.ID = u.Query().Get("id")
	}
	if ref.ID == "" || !linkID.MatchString(ref.ID) {
		return Ref{}, fmt.Errorf("no file id in link %q", raw)
	}
	return ref, nil
}

// KindFromMime maps a Drive mimeType to the kind gws works with.
func KindFromMime(mime string) Kind {
	switch mime {
	case "application/vnd.google-apps.document":
		return KindDoc
	case "application/vnd.google-apps.spreadsheet":
		return KindSheet
	case "application/vnd.google-apps.folder":
		return KindFolder
	case "":
		return KindUnknown
	default:
		return KindFile
	}
}

func segments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func has(segs []string, want string) bool {
	for _, s := range segs {
		if s == want {
			return true
		}
	}
	return false
}

func after(segs []string, want string) string {
	for i, s := range segs {
		if s == want && i+1 < len(segs) {
			return segs[i+1]
		}
	}
	return ""
}

// gid reads the sheet tab id. Google puts it in the fragment, older links put
// it in the query.
func gid(u *url.URL) int64 {
	for _, s := range []string{u.Fragment, u.RawQuery} {
		if s == "" {
			continue
		}
		v, err := url.ParseQuery(s)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(v.Get("gid"), 10, 64)
		if err == nil {
			return n
		}
	}
	return -1
}
