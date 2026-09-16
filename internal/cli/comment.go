package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rsoeldner/lcli/internal/linear"
	"github.com/rsoeldner/lcli/internal/upload"
)

func (a *App) commentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "List, add or edit comments on an issue",
	}
	cmd.AddCommand(a.commentListCmd(), a.commentAddCmd(), a.commentEditCmd())
	return cmd
}

func (a *App) commentListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list <ID|URL>",
		Short: "List an issue's comments with their IDs, oldest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, id, err := a.open(cmd, args[0])
			if err != nil {
				return err
			}
			comments, err := fetchComments(cmd.Context(), s, id.String())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), comments)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# Comments on %s (%d)\n", id, len(comments))
			renderComments(cmd.OutOrStdout(), comments)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of markdown")
	return cmd
}

// writeFlags are shared by the commands that post content.
type writeFlags struct {
	message  string
	bodyFile string
	attach   []string
	dryRun   bool
}

func (w *writeFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&w.message, "message", "m", "", "comment text (markdown); prefer --body-file for multi-line text")
	cmd.Flags().StringVar(&w.bodyFile, "body-file", "", "read the comment text (markdown) from this file, or '-' for stdin")
	cmd.Flags().StringArrayVar(&w.attach, "attach", nil, "upload this image/video/file and append it to the comment (repeatable)")
	cmd.Flags().BoolVar(&w.dryRun, "dry-run", false, "print what would be posted without uploading or posting anything")
}

// body returns the text given via -m or --body-file and whether either was set.
func (w *writeFlags) body(cmd *cobra.Command, rawArgs []string) (string, bool, error) {
	hasMsg, hasFile := cmd.Flags().Changed("message"), cmd.Flags().Changed("body-file")
	switch {
	case hasMsg && hasFile:
		return "", false, usageErr("use either -m or --body-file, not both")
	case hasMsg:
		if looksLikeFlag(cmd, rawArgs, w.message) {
			return "", false, usageErr("-m value %q is one of this command's flags; the text is probably missing (use -m=TEXT to post it literally)", w.message)
		}
		return w.message, true, nil
	case hasFile && w.bodyFile == "-":
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", false, usageErr("read stdin: %v", err)
		}
		return string(b), true, nil
	case hasFile:
		b, err := os.ReadFile(w.bodyFile)
		if err != nil {
			return "", false, usageErr("read --body-file: %v", err)
		}
		return string(b), true, nil
	}
	return "", false, nil
}

// looksLikeFlag reports whether v names a flag of cmd, e.g. "--dry-run" in
// "-m --dry-run", where pflag would take the flag as the message text.
// A value given as -m=--dry-run is taken literally.
func looksLikeFlag(cmd *cobra.Command, rawArgs []string, v string) bool {
	for _, arg := range rawArgs {
		if strings.HasPrefix(arg, "-m=") || strings.HasPrefix(arg, "--message=") {
			return false
		}
	}
	name, ok := strings.CutPrefix(v, "--")
	if ok {
		name, _, _ = strings.Cut(name, "=")
		return cmd.Flags().Lookup(name) != nil
	}
	if short, ok := strings.CutPrefix(v, "-"); ok && len(short) == 1 {
		return cmd.Flags().ShorthandLookup(short) != nil
	}
	return false
}

func statFiles(paths []string) ([]upload.File, error) {
	files := make([]upload.File, 0, len(paths))
	for _, p := range paths {
		f, err := upload.Stat(p)
		if err != nil {
			return nil, usageErr("cannot upload %v", err)
		}
		files = append(files, f)
	}
	return files, nil
}

// uploadFiles uploads files in order and returns their markdown snippets;
// on failure it returns the snippets of the files already uploaded. With
// dryRun it returns placeholder snippets and uploads nothing.
func (a *App) uploadFiles(ctx context.Context, s *session, files []upload.File, dryRun bool) ([]string, error) {
	u := &upload.Uploader{Client: s.client, HTTP: a.HTTP}
	snippets := make([]string, 0, len(files))
	for _, f := range files {
		assetURL := "ASSET_URL_AFTER_UPLOAD"
		if !dryRun {
			var err error
			if assetURL, err = u.Upload(ctx, f); err != nil {
				return snippets, apiErr(s.account.Name, err)
			}
		}
		snippets = append(snippets, upload.Snippet(f, assetURL))
	}
	return snippets, nil
}

func joinBody(body string, snippets []string) string {
	body = strings.TrimRight(body, " \t\r\n")
	if len(snippets) == 0 {
		return body
	}
	if body == "" {
		return strings.Join(snippets, "\n")
	}
	return body + "\n\n" + strings.Join(snippets, "\n")
}

func (a *App) issueRef(ctx context.Context, s *session, id string) (*linear.IssueRefIssue, error) {
	resp, err := linear.IssueRef(ctx, s.client, id)
	if err != nil {
		return nil, apiErr(s.account.Name, err)
	}
	return &resp.Issue, nil
}

// commentOnIssue fetches a comment and checks that it belongs to issue.
func commentOnIssue(ctx context.Context, s *session, commentID string, issue *linear.IssueRefIssue) (*linear.CommentByIDComment, error) {
	resp, err := linear.CommentByID(ctx, s.client, commentID)
	if err != nil {
		return nil, apiErr(s.account.Name, err)
	}
	c := &resp.Comment
	if c.Issue == nil || c.Issue.Id != issue.Id {
		where := "no issue"
		if c.Issue != nil {
			where = c.Issue.Identifier
		}
		return nil, usageErr("comment %s belongs to %s, not %s", commentID, where, issue.Identifier)
	}
	return c, nil
}

// reportUploaded lists files that were uploaded before a later step failed,
// so they can be embedded with 'comment edit' instead of uploaded again.
func reportUploaded(w io.Writer, snippets []string) {
	if len(snippets) == 0 {
		return
	}
	fmt.Fprintln(w, "Already uploaded (embed these instead of uploading again):")
	for _, s := range snippets {
		fmt.Fprintln(w, s)
	}
}

func printDryRun(w io.Writer, action string, files []upload.File, body string) {
	fmt.Fprintf(w, "Dry run: %s. Nothing was uploaded or posted.\n", action)
	for _, f := range files {
		fmt.Fprintf(w, "- would upload %s (%s, %d bytes)\n", f.Path, f.ContentType, f.Size)
	}
	fmt.Fprintf(w, "\n--- body ---\n%s\n--- end ---\n", body)
}

func (a *App) commentAddCmd() *cobra.Command {
	var (
		w       writeFlags
		replyTo string
	)
	cmd := &cobra.Command{
		Use:   "add <ID|URL>",
		Short: "Add a comment, optionally with uploaded images/videos and as a reply",
		Long: `Add a comment to an issue. Text comes from -m or --body-file (use '-' for
stdin). Each --attach file is uploaded to Linear and appended to the comment
as markdown (images inline, other files as links). Prints the new comment's
ID and URL; use 'lcli comment edit' to fix a comment instead of posting again.`,
		Example: `  lcli comment add ENG-123 --body-file /tmp/analysis.md --attach before.png --attach repro.mp4
  lcli comment add ENG-123 -m "Deployed to staging." --reply-to 0f5c...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _, err := w.body(cmd, a.args)
			if err != nil {
				return err
			}
			files, err := statFiles(w.attach)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" && len(files) == 0 {
				return usageErr("nothing to post: give text with -m or --body-file, or files with --attach")
			}
			s, id, err := a.open(cmd, args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			issue, err := a.issueRef(ctx, s, id.String())
			if err != nil {
				return err
			}
			var parentID *string
			if replyTo != "" {
				if _, err := commentOnIssue(ctx, s, replyTo, issue); err != nil {
					return err
				}
				parentID = &replyTo
			}
			snippets, err := a.uploadFiles(ctx, s, files, w.dryRun)
			if err != nil {
				reportUploaded(cmd.ErrOrStderr(), snippets)
				return err
			}
			full := joinBody(body, snippets)
			out := cmd.OutOrStdout()
			if w.dryRun {
				action := fmt.Sprintf("would add a comment to %s (account %s)", issue.Identifier, s.account.Name)
				if parentID != nil {
					action += " as a reply to " + replyTo
				}
				printDryRun(out, action, files, full)
				return nil
			}
			resp, err := linear.CreateComment(ctx, s.client, issue.Id, full, parentID)
			if err == nil && !resp.CommentCreate.Success {
				err = fmt.Errorf("commentCreate reported no success")
			}
			if err != nil {
				reportUploaded(cmd.ErrOrStderr(), snippets)
				return apiErr(s.account.Name, err)
			}
			c := resp.CommentCreate.Comment
			fmt.Fprintf(out, "Created comment %s on %s\nURL: %s\n", c.Id, issue.Identifier, c.Url)
			return nil
		},
	}
	w.register(cmd)
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "post as a reply to this comment ID (must be on the same issue)")
	return cmd
}

func (a *App) commentEditCmd() *cobra.Command {
	var (
		w        writeFlags
		appendTo bool
	)
	cmd := &cobra.Command{
		Use:   "edit <ID|URL> <COMMENT_ID>",
		Short: "Replace or append to an existing comment (e.g. to fix a mistake)",
		Long: `Edit a comment on an issue. By default the new text REPLACES the whole
comment body, so pass the complete corrected text. With --append the text
and any --attach files are added to the end of the existing body instead.
The comment must belong to the given issue. Use --dry-run to preview the
resulting body.`,
		Example: `  lcli comment edit ENG-123 0f5c... --body-file /tmp/fixed.md
  lcli comment edit ENG-123 0f5c... --append -m "Update: fixed in v1.2" --attach after.png`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, hasBody, err := w.body(cmd, a.args)
			if err != nil {
				return err
			}
			if !appendTo && !hasBody {
				return usageErr("give the complete new text with -m or --body-file, or use --append to add to the existing comment")
			}
			files, err := statFiles(w.attach)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" && len(files) == 0 {
				if appendTo {
					return usageErr("nothing to append: give text with -m or --body-file, or files with --attach")
				}
				return usageErr("refusing to replace the comment with an empty body")
			}
			s, id, err := a.open(cmd, args[0])
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			issue, err := a.issueRef(ctx, s, id.String())
			if err != nil {
				return err
			}
			existing, err := commentOnIssue(ctx, s, args[1], issue)
			if err != nil {
				return err
			}
			snippets, err := a.uploadFiles(ctx, s, files, w.dryRun)
			if err != nil {
				reportUploaded(cmd.ErrOrStderr(), snippets)
				return err
			}
			addition := joinBody(body, snippets)
			full := addition
			if appendTo {
				full = joinBody(existing.Body, []string{addition})
			}
			out := cmd.OutOrStdout()
			if w.dryRun {
				printDryRun(out, fmt.Sprintf("would update comment %s on %s (account %s)", existing.Id, issue.Identifier, s.account.Name), files, full)
				return nil
			}
			resp, err := linear.UpdateComment(ctx, s.client, existing.Id, full)
			if err == nil && !resp.CommentUpdate.Success {
				err = fmt.Errorf("commentUpdate reported no success")
			}
			if err != nil {
				reportUploaded(cmd.ErrOrStderr(), snippets)
				return apiErr(s.account.Name, err)
			}
			c := resp.CommentUpdate.Comment
			fmt.Fprintf(out, "Updated comment %s on %s\nURL: %s\n", c.Id, issue.Identifier, c.Url)
			return nil
		},
	}
	w.register(cmd)
	cmd.Flags().BoolVar(&appendTo, "append", false, "append the text and attachments to the existing body instead of replacing it")
	return cmd
}
