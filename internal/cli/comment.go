package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) commentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "List, add or edit comments on an issue",
	}
	cmd.AddCommand(a.commentListCmd())
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
