package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) uploadCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "upload <ID|URL> <FILE>...",
		Short: "Upload files and print markdown to embed them (prefer 'comment add --attach')",
		Long: `Upload files to the Linear account that owns the issue and print one markdown
snippet per file. The issue ID selects the account and is checked to exist.
Uploaded files are NOT visible on the issue until a snippet is embedded in a
comment ('lcli comment add' / 'lcli comment edit'). Use this only when a file
must appear in the middle of the comment text; otherwise use --attach.`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := statFiles(args[1:])
			if err != nil {
				return err
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
			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintf(out, "Dry run: would upload to account %s for %s. Nothing was uploaded.\n", s.account.Name, issue.Identifier)
				for _, f := range files {
					fmt.Fprintf(out, "- %s (%s, %d bytes)\n", f.Path, f.ContentType, f.Size)
				}
				return nil
			}
			snippets, err := a.uploadFiles(ctx, s, files, false)
			for _, sn := range snippets {
				fmt.Fprintln(out, sn)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Note: not visible on %s until embedded via 'lcli comment add' or 'lcli comment edit'.\n", issue.Identifier)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate files and the issue without uploading")
	return cmd
}
