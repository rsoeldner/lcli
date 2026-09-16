package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rsoeldner/lcli/internal/ident"
	"github.com/rsoeldner/lcli/internal/linear"
	"github.com/rsoeldner/lcli/internal/media"
)

// issueView is the stable shape lcli prints for an issue; it is decoupled
// from the generated GraphQL types so --json output does not change when a
// query does.
type issueView struct {
	Account     string           `json:"account"`
	Identifier  string           `json:"identifier"`
	Title       string           `json:"title"`
	URL         string           `json:"url"`
	Team        string           `json:"team"`
	State       string           `json:"state"`
	Priority    string           `json:"priority"`
	Assignee    string           `json:"assignee,omitempty"`
	Creator     string           `json:"creator,omitempty"`
	Labels      []string         `json:"labels"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	Description string           `json:"description"`
	Attachments []attachmentView `json:"attachments"`
	Comments    []commentView    `json:"comments"`
	Media       []mediaView      `json:"media"`
}

type attachmentView struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	URL        string `json:"url"`
	SourceType string `json:"sourceType,omitempty"`
}

type commentView struct {
	ID        string     `json:"id"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"createdAt"`
	EditedAt  *time.Time `json:"editedAt,omitempty"`
	ParentID  string     `json:"parentId,omitempty"`
	URL       string     `json:"url"`
	Body      string     `json:"body"`
}

type mediaView struct {
	URL         string   `json:"url"`
	Path        string   `json:"path,omitempty"`
	ContentType string   `json:"contentType,omitempty"`
	Frames      []string `json:"frames,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func (a *App) issueCmd() *cobra.Command {
	var (
		noDownload bool
		outDir     string
		frames     int
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "issue <ID|URL>",
		Short: "Show an issue with its comments and attachments, downloading uploaded images and videos",
		Long: `Show an issue: title, state, assignee, labels, description, attachments and
all comments (with comment IDs for 'comment edit' and '--reply-to').

Files uploaded to Linear and referenced in the description or comments are
downloaded (with the account's API key) into --out, default
<user cache dir>/lcli/<ID>/, and their local paths are listed under "Media".`,
		Example: `  lcli issue ENG-123
  lcli issue https://linear.app/acme/issue/ACME-9/title --frames 4
  lcli issue ENG-123 --json --no-download`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if frames < 0 {
				return usageErr("--frames must be >= 0")
			}
			if noDownload && (frames > 0 || outDir != "") {
				return usageErr("--no-download cannot be combined with --frames or --out")
			}
			s, id, err := a.open(cmd, args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			view, err := a.fetchIssue(ctx, s, id)
			if err != nil {
				return err
			}
			if !noDownload {
				if outDir == "" {
					outDir = filepath.Join(a.CacheDir, "lcli", view.Identifier)
				}
				view.Media = a.downloadMedia(ctx, s, view, outDir, frames)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), view)
			}
			renderIssue(cmd.OutOrStdout(), view, noDownload)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noDownload, "no-download", false, "do not download uploaded files")
	cmd.Flags().StringVar(&outDir, "out", "", "directory for downloaded files (default <user cache dir>/lcli/<ID>)")
	cmd.Flags().IntVar(&frames, "frames", 0, "extract this many still frames from each downloaded video (needs ffmpeg)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of markdown")
	return cmd
}

func (a *App) fetchIssue(ctx context.Context, s *session, id ident.ID) (*issueView, error) {
	resp, err := linear.IssueDetails(ctx, s.client, id.String())
	if err != nil {
		return nil, apiErr(s.account.Name, err)
	}
	is := resp.Issue
	view := &issueView{
		Account:     s.account.Name,
		Identifier:  is.Identifier,
		Title:       is.Title,
		URL:         is.Url,
		Team:        is.Team.Key,
		State:       is.State.Name,
		Priority:    is.PriorityLabel,
		Labels:      []string{},
		CreatedAt:   is.CreatedAt,
		UpdatedAt:   is.UpdatedAt,
		Description: is.Description,
		Attachments: []attachmentView{},
		Media:       []mediaView{},
	}
	if is.Assignee != nil {
		view.Assignee = is.Assignee.DisplayName
	}
	if is.Creator != nil {
		view.Creator = is.Creator.DisplayName
	}
	for _, l := range is.Labels.Nodes {
		view.Labels = append(view.Labels, l.Name)
	}
	for _, at := range is.Attachments.Nodes {
		view.Attachments = append(view.Attachments, attachmentView{
			ID: at.Id, Title: at.Title, Subtitle: at.Subtitle, URL: at.Url, SourceType: at.SourceType,
		})
	}
	view.Comments, err = fetchComments(ctx, s, is.Identifier)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// fetchComments returns every comment on the issue, oldest first.
func fetchComments(ctx context.Context, s *session, issueID string) ([]commentView, error) {
	comments := []commentView{}
	var after *string
	for {
		resp, err := linear.IssueComments(ctx, s.client, issueID, after)
		if err != nil {
			return nil, apiErr(s.account.Name, err)
		}
		conn := resp.Issue.Comments
		for _, c := range conn.Nodes {
			comments = append(comments, newCommentView(c.CommentFields))
		}
		if !conn.PageInfo.HasNextPage || conn.PageInfo.EndCursor == "" {
			break
		}
		cursor := conn.PageInfo.EndCursor
		after = &cursor
	}
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	return comments, nil
}

func newCommentView(c linear.CommentFields) commentView {
	v := commentView{ID: c.Id, CreatedAt: c.CreatedAt, ParentID: c.ParentId, URL: c.Url, Body: c.Body, Author: "unknown"}
	if !c.EditedAt.IsZero() {
		edited := c.EditedAt
		v.EditedAt = &edited
	}
	switch {
	case c.User != nil:
		v.Author = c.User.DisplayName
	case c.BotActor != nil:
		v.Author = c.BotActor.Name + " (bot)"
	case c.ExternalUser != nil:
		v.Author = c.ExternalUser.Name + " (external)"
	}
	return v
}

func (a *App) downloadMedia(ctx context.Context, s *session, view *issueView, dir string, frames int) []mediaView {
	var urls []string
	seen := map[string]bool{}
	add := func(markdown string) {
		for _, u := range media.Extract(markdown, a.UploadHosts) {
			if !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
	}
	add(view.Description)
	for _, c := range view.Comments {
		add(c.Body)
	}
	d := &media.Downloader{HTTP: a.HTTP, APIKey: s.key, Hosts: a.UploadHosts}
	out := []mediaView{}
	for _, u := range urls {
		mv := mediaView{URL: u}
		f, err := d.Download(ctx, u, dir)
		if err != nil {
			mv.Error = err.Error()
			out = append(out, mv)
			continue
		}
		mv.Path, mv.ContentType = f.Path, f.ContentType
		if frames > 0 && media.IsVideo(f.ContentType) {
			mv.Frames, err = media.Frames(ctx, f.Path, frames)
			if err != nil {
				mv.Error = err.Error()
			}
		}
		out = append(out, mv)
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

const timeLayout = "2006-01-02 15:04 MST"

func renderIssue(w io.Writer, v *issueView, noDownload bool) {
	fmt.Fprintf(w, "# %s: %s\n\n", v.Identifier, v.Title)
	fmt.Fprintf(w, "- URL: %s\n", v.URL)
	fmt.Fprintf(w, "- Account: %s · Team: %s\n", v.Account, v.Team)
	fmt.Fprintf(w, "- State: %s · Priority: %s\n", v.State, v.Priority)
	fmt.Fprintf(w, "- Assignee: %s · Creator: %s\n", orNone(v.Assignee), orNone(v.Creator))
	fmt.Fprintf(w, "- Labels: %s\n", orNone(strings.Join(v.Labels, ", ")))
	fmt.Fprintf(w, "- Created: %s · Updated: %s\n", v.CreatedAt.Format(timeLayout), v.UpdatedAt.Format(timeLayout))

	fmt.Fprintf(w, "\n## Description\n\n%s\n", orNone(strings.TrimSpace(v.Description)))

	fmt.Fprintf(w, "\n## Attachments (%d)\n\n", len(v.Attachments))
	if len(v.Attachments) == 0 {
		fmt.Fprintln(w, "(none)")
	}
	for _, at := range v.Attachments {
		fmt.Fprintf(w, "- [%s](%s)", at.Title, at.URL)
		if at.SourceType != "" {
			fmt.Fprintf(w, " (%s)", at.SourceType)
		}
		if at.Subtitle != "" {
			fmt.Fprintf(w, " — %s", at.Subtitle)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "\n## Comments (%d)\n", len(v.Comments))
	renderComments(w, v.Comments)

	fmt.Fprintf(w, "\n## Media (%d)\n\n", len(v.Media))
	switch {
	case noDownload:
		fmt.Fprintln(w, "(download skipped)")
	case len(v.Media) == 0:
		fmt.Fprintln(w, "(no uploaded files referenced)")
	}
	for _, m := range v.Media {
		if m.Path != "" {
			fmt.Fprintf(w, "- %s\n  -> %s (%s)\n", m.URL, m.Path, m.ContentType)
		} else {
			fmt.Fprintf(w, "- %s\n", m.URL)
		}
		for _, f := range m.Frames {
			fmt.Fprintf(w, "  frame: %s\n", f)
		}
		if m.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", m.Error)
		}
	}
}

func renderComments(w io.Writer, comments []commentView) {
	if len(comments) == 0 {
		fmt.Fprintln(w, "\n(none)")
	}
	for _, c := range comments {
		fmt.Fprintf(w, "\n### Comment %s\n\n", c.ID)
		fmt.Fprintf(w, "- Author: %s · Created: %s", c.Author, c.CreatedAt.Format(timeLayout))
		if c.EditedAt != nil {
			fmt.Fprintf(w, " · Edited: %s", c.EditedAt.Format(timeLayout))
		}
		fmt.Fprintln(w)
		if c.ParentID != "" {
			fmt.Fprintf(w, "- Reply to: %s\n", c.ParentID)
		}
		fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(c.Body))
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
