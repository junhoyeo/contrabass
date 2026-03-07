package main

import (
	"context"
	"fmt"
	"io"
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

var boardAssignCmd = &cobra.Command{
	Use:   "assign <issue-id> <assignee>",
	Short: "Assign an internal board issue",
	Args:  cobra.ExactArgs(2),
	RunE:  runBoardAssign,
}

var boardDispatchCmd = &cobra.Command{
	Use:   "dispatch",
	Short: "Dispatch the next runnable internal board issue into a team run",
	RunE:  runBoardDispatch,
}

type boardDispatchOptions struct {
	ConfigPath string
	TeamName   string
	MaxWorkers int
	UntilEmpty bool
}

var runBoardDispatchTeam = runTeamWithOptions

func init() {
	for _, command := range []*cobra.Command{
		boardInitCmd,
		boardListCmd,
		boardCreateCmd,
		boardShowCmd,
		boardMoveCmd,
		boardCommentCmd,
		boardAssignCmd,
		boardDispatchCmd,
	} {
		command.Flags().String("config", "", "path to WORKFLOW.md file")
		command.Flags().String("dir", "", "override internal board directory")
	}

	boardInitCmd.Flags().String("prefix", "", "override local issue prefix")

	boardListCmd.Flags().String("state", "", "filter issues by state (todo, in_progress, retry, done)")

	boardCreateCmd.Flags().String("title", "", "issue title")
	boardCreateCmd.Flags().String("description", "", "issue description")
	boardCreateCmd.Flags().String("parent", "", "parent issue ID")
	boardCreateCmd.Flags().String("assignee", "", "assign the issue to a worker or team")
	boardCreateCmd.Flags().StringSlice("labels", nil, "issue labels")
	boardCreateCmd.Flags().StringSlice("blocked-by", nil, "board issue IDs that block this issue")
	_ = boardCreateCmd.MarkFlagRequired("title")

	boardCommentCmd.Flags().String("body", "", "comment body")
	_ = boardCommentCmd.MarkFlagRequired("body")

	boardDispatchCmd.Flags().String("team-name", "", "override the team name used for dispatch")
	boardDispatchCmd.Flags().IntP("max-workers", "w", 0, "override max workers from config")
	boardDispatchCmd.Flags().Bool("until-empty", false, "keep dispatching runnable issues until the internal board is drained")

	boardCmd.AddCommand(
		boardInitCmd,
		boardListCmd,
		boardCreateCmd,
		boardShowCmd,
		boardMoveCmd,
		boardCommentCmd,
		boardAssignCmd,
		boardDispatchCmd,
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
	_, _ = fmt.Fprintln(w, "ID\tSTATE\tASSIGNEE\tPARENT\tTITLE\tLABELS")
	for _, issue := range issues {
		if filter != "" && issue.State != filter {
			continue
		}
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			issue.ID,
			issue.State,
			issue.Assignee,
			issue.ParentID,
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

	parentID, err := cmd.Flags().GetString("parent")
	if err != nil {
		return fmt.Errorf("getting parent flag: %w", err)
	}

	assignee, err := cmd.Flags().GetString("assignee")
	if err != nil {
		return fmt.Errorf("getting assignee flag: %w", err)
	}

	labels, err := cmd.Flags().GetStringSlice("labels")
	if err != nil {
		return fmt.Errorf("getting labels flag: %w", err)
	}

	blockedBy, err := cmd.Flags().GetStringSlice("blocked-by")
	if err != nil {
		return fmt.Errorf("getting blocked-by flag: %w", err)
	}

	issue, err := localTracker.CreateIssueWithOptions(context.Background(), tracker.LocalIssueCreateOptions{
		Title:       title,
		Description: description,
		ParentID:    parentID,
		Assignee:    assignee,
		Labels:      labels,
		BlockedBy:   blockedBy,
	})
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
	_, _ = fmt.Fprintf(out, "Assignee: %s\n", issue.Assignee)
	_, _ = fmt.Fprintf(out, "Parent: %s\n", issue.ParentID)
	_, _ = fmt.Fprintf(out, "Children: %s\n", strings.Join(issue.ChildIDs, ","))
	_, _ = fmt.Fprintf(out, "BlockedBy: %s\n", strings.Join(issue.BlockedBy, ","))
	_, _ = fmt.Fprintf(out, "ClaimedBy: %s\n", issue.ClaimedBy)
	if teamName, ok := issue.TrackerMeta["team_name"].(string); ok && teamName != "" {
		_, _ = fmt.Fprintf(out, "Team: %s\n", teamName)
	}
	if teamStatus, ok := issue.TrackerMeta["team_status"].(string); ok && teamStatus != "" {
		_, _ = fmt.Fprintf(out, "TeamStatus: %s\n", teamStatus)
	}
	if teamPhase, ok := issue.TrackerMeta["team_phase"].(string); ok && teamPhase != "" {
		_, _ = fmt.Fprintf(out, "TeamPhase: %s\n", teamPhase)
	}
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

func runBoardAssign(cmd *cobra.Command, args []string) error {
	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	issue, err := localTracker.AssignIssue(context.Background(), args[0], args[1])
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s -> %s\n", issue.ID, issue.Assignee)
	return nil
}

func runBoardDispatch(cmd *cobra.Command, _ []string) error {
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("getting config flag: %w", err)
	}
	if strings.TrimSpace(cfgPath) == "" {
		return fmt.Errorf("board dispatch requires --config")
	}

	localTracker, err := loadLocalBoardTracker(cmd, false)
	if err != nil {
		return err
	}

	teamName, err := cmd.Flags().GetString("team-name")
	if err != nil {
		return fmt.Errorf("getting team-name flag: %w", err)
	}

	maxWorkers, err := cmd.Flags().GetInt("max-workers")
	if err != nil {
		return fmt.Errorf("getting max-workers flag: %w", err)
	}

	untilEmpty, err := cmd.Flags().GetBool("until-empty")
	if err != nil {
		return fmt.Errorf("getting until-empty flag: %w", err)
	}

	return dispatchBoardIssues(
		context.Background(),
		cmd.OutOrStdout(),
		localTracker,
		boardDispatchOptions{
			ConfigPath: cfgPath,
			TeamName:   strings.TrimSpace(teamName),
			MaxWorkers: maxWorkers,
			UntilEmpty: untilEmpty,
		},
		runBoardDispatchTeam,
	)
}

func dispatchBoardIssues(
	ctx context.Context,
	out io.Writer,
	localTracker *tracker.LocalTracker,
	opts boardDispatchOptions,
	runTeam func(teamRunOptions) error,
) error {
	dispatched := 0
	for {
		issueID, resolvedTeamName, found, err := dispatchNextBoardIssue(ctx, localTracker, opts, runTeam)
		if err != nil {
			return err
		}
		if !found {
			if !opts.UntilEmpty {
				return fmt.Errorf("no dispatchable internal board issue found")
			}
			if dispatched == 0 {
				_, _ = fmt.Fprintln(out, "board already drained")
				return nil
			}
			_, _ = fmt.Fprintf(out, "drained board after %d dispatches\n", dispatched)
			return nil
		}

		dispatched++
		_, _ = fmt.Fprintf(out, "dispatched %s to %s\n", issueID, resolvedTeamName)
		if !opts.UntilEmpty {
			return nil
		}
	}
}

func dispatchNextBoardIssue(
	ctx context.Context,
	localTracker *tracker.LocalTracker,
	opts boardDispatchOptions,
	runTeam func(teamRunOptions) error,
) (string, string, bool, error) {
	issue, found, err := localTracker.FindDispatchableIssue(ctx, opts.TeamName)
	if err != nil {
		return "", "", false, err
	}
	if !found {
		return "", "", false, nil
	}

	resolvedTeamName := resolveTeamNameForIssue(issue, opts.TeamName)
	if _, err := localTracker.AssignIssue(ctx, issue.ID, resolvedTeamName); err != nil {
		return "", "", false, err
	}
	if err := localTracker.PostComment(
		ctx,
		issue.ID,
		fmt.Sprintf("dispatch requested for team %s", resolvedTeamName),
	); err != nil {
		return "", "", false, err
	}

	if err := runTeam(teamRunOptions{
		ConfigPath: opts.ConfigPath,
		TeamName:   resolvedTeamName,
		IssueID:    issue.ID,
		MaxWorkers: opts.MaxWorkers,
	}); err != nil {
		return "", "", false, fmt.Errorf("dispatching %s to %s: %w", issue.ID, resolvedTeamName, err)
	}

	return issue.ID, resolvedTeamName, true, nil
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
