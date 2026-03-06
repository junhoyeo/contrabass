package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/junhoyeo/contrabass/internal/config"
	"github.com/junhoyeo/contrabass/internal/tracker"
)

var boardCmd = &cobra.Command{
	Use:   "board",
	Short: "Manage the internal .contrabass issue board",
	Long:  "Manage the internal .contrabass issue board for tracker.type=internal workflows",
}

var boardInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the internal board storage",
	RunE:  runBoardInit,
}

var boardListCmd = &cobra.Command{
	Use:   "list",
	Short: "List internal board issues",
	RunE:  runBoardList,
}

var boardCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an internal board issue",
	RunE:  runBoardCreate,
}

var boardShowCmd = &cobra.Command{
	Use:   "show <issue-id>",
	Short: "Show an internal board issue",
	Args:  cobra.ExactArgs(1),
	RunE:  runBoardShow,
}

var boardMoveCmd = &cobra.Command{
	Use:   "move <issue-id> <state>",
	Short: "Move an internal board issue to a new state",
	Args:  cobra.ExactArgs(2),
	RunE:  runBoardMove,
}

var boardCommentCmd = &cobra.Command{
	Use:   "comment <issue-id>",
	Short: "Add a comment to an internal board issue",
	Args:  cobra.ExactArgs(1),
	RunE:  runBoardComment,
}

func init() {
	for _, command := range []*cobra.Command{
		boardInitCmd,
		boardListCmd,
		boardCreateCmd,
		boardShowCmd,
		boardMoveCmd,
		boardCommentCmd,
	} {
		command.Flags().String("config", "", "path to WORKFLOW.md file")
		command.Flags().String("dir", "", "override internal board directory")
	}

	boardInitCmd.Flags().String("prefix", "", "override local issue prefix")

	boardListCmd.Flags().String("state", "", "filter issues by state (todo, in_progress, retry, done)")

	boardCreateCmd.Flags().String("title", "", "issue title")
	boardCreateCmd.Flags().String("description", "", "issue description")
	boardCreateCmd.Flags().StringSlice("labels", nil, "issue labels")
	_ = boardCreateCmd.MarkFlagRequired("title")

	boardCommentCmd.Flags().String("body", "", "comment body")
	_ = boardCommentCmd.MarkFlagRequired("body")

	boardCmd.AddCommand(
		boardInitCmd,
		boardListCmd,
		boardCreateCmd,
		boardShowCmd,
		boardMoveCmd,
		boardCommentCmd,
	)
}

func runBoardInit(cmd *cobra.Command, _ []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, true)
	if err != nil {
		return err
	}

	manifest, err := localTracker.InitBoard(context.Background())
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(
		cmd.OutOrStdout(),
		"initialized board at %s (prefix %s)\n",
		localTracker.BoardDir(),
		manifest.IssuePrefix,
	)
	return nil
}

func runBoardList(cmd *cobra.Command, _ []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	filterRaw, err := cmd.Flags().GetString("state")
	if err != nil {
		return fmt.Errorf("getting state flag: %w", err)
	}

	var filter tracker.LocalBoardState
	if filterRaw != "" {
		filter, err = tracker.ParseLocalBoardState(filterRaw)
		if err != nil {
			return err
		}
	}

	issues, err := localTracker.ListIssues(context.Background(), true)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tSTATE\tTITLE\tLABELS")
	for _, issue := range issues {
		if filter != "" && issue.State != filter {
			continue
		}
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\n",
			issue.ID,
			issue.State,
			issue.Title,
			strings.Join(issue.Labels, ","),
		)
	}

	return w.Flush()
}

func runBoardCreate(cmd *cobra.Command, _ []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, true)
	if err != nil {
		return err
	}

	title, err := cmd.Flags().GetString("title")
	if err != nil {
		return fmt.Errorf("getting title flag: %w", err)
	}

	description, err := cmd.Flags().GetString("description")
	if err != nil {
		return fmt.Errorf("getting description flag: %w", err)
	}

	labels, err := cmd.Flags().GetStringSlice("labels")
	if err != nil {
		return fmt.Errorf("getting labels flag: %w", err)
	}

	issue, err := localTracker.CreateIssue(context.Background(), title, description, labels)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", issue.ID)
	return nil
}

func runBoardShow(cmd *cobra.Command, args []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	issue, err := localTracker.GetIssue(context.Background(), args[0])
	if err != nil {
		return err
	}

	comments, err := localTracker.ListComments(context.Background(), args[0])
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "ID: %s\n", issue.ID)
	_, _ = fmt.Fprintf(out, "State: %s\n", issue.State)
	_, _ = fmt.Fprintf(out, "Title: %s\n", issue.Title)
	_, _ = fmt.Fprintf(out, "Labels: %s\n", strings.Join(issue.Labels, ","))
	_, _ = fmt.Fprintf(out, "ClaimedBy: %s\n", issue.ClaimedBy)
	_, _ = fmt.Fprintf(out, "CreatedAt: %s\n", issue.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	_, _ = fmt.Fprintf(out, "UpdatedAt: %s\n", issue.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Description:")
	_, _ = fmt.Fprintln(out, issue.Description)
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Comments:")
	if len(comments) == 0 {
		_, _ = fmt.Fprintln(out, "(none)")
		return nil
	}

	for _, comment := range comments {
		_, _ = fmt.Fprintf(
			out,
			"- [%s] %s: %s\n",
			comment.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			comment.Author,
			comment.Body,
		)
	}

	return nil
}

func runBoardMove(cmd *cobra.Command, args []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	state, err := tracker.ParseLocalBoardState(args[1])
	if err != nil {
		return err
	}

	issue, err := localTracker.MoveIssue(context.Background(), args[0], state)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s -> %s\n", issue.ID, issue.State)
	return nil
}

func runBoardComment(cmd *cobra.Command, args []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	body, err := cmd.Flags().GetString("body")
	if err != nil {
		return fmt.Errorf("getting body flag: %w", err)
	}

	if err := localTracker.AddComment(context.Background(), args[0], body); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "commented on %s\n", args[0])
	return nil
}

func loadLocalBoardTracker(cmd *cobra.Command, allowPrefixOverride bool) (*tracker.LocalTracker, error) {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("getting config flag: %w", err)
	}

	dirOverride, err := cmd.Flags().GetString("dir")
	if err != nil {
		return nil, fmt.Errorf("getting dir flag: %w", err)
	}

	prefixOverride := ""
	if allowPrefixOverride && cmd.Flags().Lookup("prefix") != nil {
		prefixOverride, err = cmd.Flags().GetString("prefix")
		if err != nil {
			return nil, fmt.Errorf("getting prefix flag: %w", err)
		}
	}

	cfg := &config.WorkflowConfig{}
	if cfgPath != "" {
		parsed, err := config.ParseWorkflow(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("parsing workflow config: %w", err)
		}
		cfg = parsed
	}

	boardDir := cfg.LocalBoardDir()
	if dirOverride != "" {
		boardDir = dirOverride
	}

	issuePrefix := cfg.LocalIssuePrefix()
	if prefixOverride != "" {
		issuePrefix = prefixOverride
	}

	actor := os.Getenv("TRACKER_ACTOR")
	if actor == "" {
		actor = cfg.GitHubAssignee()
	}

	return tracker.NewLocalTracker(tracker.LocalConfig{
		BoardDir:    boardDir,
		IssuePrefix: issuePrefix,
		Actor:       actor,
	}), nil
}
