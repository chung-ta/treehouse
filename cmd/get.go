package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kunchenguid/treehouse/internal/config"
	"github.com/kunchenguid/treehouse/internal/git"
	"github.com/kunchenguid/treehouse/internal/pool"
	"github.com/kunchenguid/treehouse/internal/process"
	"github.com/kunchenguid/treehouse/internal/shell"
	"github.com/kunchenguid/treehouse/internal/slug"
	"github.com/kunchenguid/treehouse/internal/ui"
)

var (
	getLease       bool
	getLeaseHolder string
	getJSON        bool
)

var getCmd = &cobra.Command{
	Use:   "get <description>",
	Short: "Create a worktree for a task and open a subshell",
	Long: `Create a worktree for a task and open a subshell in it.

The description is required and must be at least 10 characters. Its first 10
characters, with spaces turned into dashes, become both the worktree directory
name and the branch name:

  treehouse get "fix login redirect"
  -> <root>/<repo>/fix-login  on branch  fix-login

The worktree is checked out on that branch, so the work can be committed,
pushed, and turned into a PR without any further setup. Worktrees are not
recycled between tasks; run 'treehouse return <path>' when a task is done, which
removes the directory and keeps the branch.

Pass --lease for a non-interactive, durable acquire: treehouse creates the
worktree and marks it leased in persistent state. By default it prints only the
absolute path to stdout; add --json for the lease identity and metadata. All
banners go to stderr. A leased worktree is never removed by prune, even with no
process running inside it, until you release it with 'treehouse return <path>'.`,
	Args: cobra.MinimumNArgs(1),
	RunE: getRunE,
}

func init() {
	getCmd.Flags().BoolVar(&getLease, "lease", false, "Durably lease a worktree without opening a subshell; print only its path to stdout")
	getCmd.Flags().StringVar(&getLeaseHolder, "lease-holder", "", "Optional label recorded as the lease holder (defaults to $TREEHOUSE_LEASE_HOLDER)")
	getCmd.Flags().BoolVar(&getJSON, "json", false, "Print lease allocation as JSON (requires --lease)")
	rootCmd.AddCommand(getCmd)
}

func getRunE(cmd *cobra.Command, args []string) error {
	if getJSON && !getLease {
		return fmt.Errorf("--json requires --lease")
	}

	// The bare `treehouse` form routes here too, so it needs its own hint
	// rather than cobra's argument error.
	if len(args) == 0 {
		return fmt.Errorf("a task description is required, e.g. treehouse get %q", "fix login redirect")
	}

	// Accept both `get "fix login redirect"` and `get fix login redirect`.
	taskSlug, err := slug.From(strings.Join(args, " "))
	if err != nil {
		return err
	}

	repoRoot, err := git.FindRepoRoot()
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	cfg, err := config.Load(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	poolDir, err := config.ResolvePoolDir(repoRoot, cfg.Root)
	if err != nil {
		return fmt.Errorf("failed to resolve pool directory: %w", err)
	}

	if err := config.EnsureGitignore(filepath.Dir(poolDir)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update .gitignore: %v\n", err)
	}

	if getLease {
		return getLeaseRunE(repoRoot, poolDir, cfg, taskSlug)
	}

	acquired, err := pool.AcquireInfo(repoRoot, poolDir, taskSlug, cfg.MaxTrees, cfg.Hooks.PostCreate)
	if err != nil {
		return err
	}
	wtPath := acquired.Path

	fmt.Fprintf(os.Stderr, "🌳 Entered worktree at %s. Type 'exit' to leave.\n", ui.PrettyPath(wtPath))
	if acquired.Resumed {
		fmt.Fprintf(os.Stderr, "🌳 On branch %s, resuming earlier work on it.\n", taskSlug)
	} else {
		fmt.Fprintf(os.Stderr, "🌳 On branch %s.\n", taskSlug)
	}

	env := []string{
		"TREEHOUSE_DIR=" + wtPath,
		"TREEHOUSE_BRANCH=" + taskSlug,
	}
	_, err = shell.Spawn(wtPath, env)

	// Leaving the subshell does not end the task: the worktree is named for it
	// and stays put so the work can be resumed with 'treehouse enter'. The
	// owner reservation is process-derived, so it clears itself on exit.
	fmt.Fprintf(os.Stderr, "🌳 Worktree kept at %s (branch %s).\n", ui.PrettyPath(wtPath), taskSlug)
	fmt.Fprintf(os.Stderr, "🌳 Resume it with 'treehouse enter %s', or finish it with 'treehouse return %s'.\n",
		ui.PrettyPath(wtPath), ui.PrettyPath(wtPath))

	return nil
}

// getLeaseRunE performs a non-interactive, durable acquire. It writes either the
// worktree path or the requested JSON allocation to stdout and routes every
// human-facing message to stderr, keeping both output modes machine-readable.
func getLeaseRunE(repoRoot, poolDir string, cfg config.Config, taskSlug string) error {
	holder := getLeaseHolder
	if holder == "" {
		holder = os.Getenv("TREEHOUSE_LEASE_HOLDER")
	}

	lease, err := pool.AcquireLeaseInfo(repoRoot, poolDir, taskSlug, cfg.MaxTrees, cfg.Hooks.PostCreate, holder)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "🌳 Leased worktree at %s (branch %s). Run 'treehouse return %s' to release it.\n",
		ui.PrettyPath(lease.Path), lease.Branch, ui.PrettyPath(lease.Path))
	if getJSON {
		return json.NewEncoder(os.Stdout).Encode(lease)
	}
	// The bare path is the only thing on stdout, so callers can capture it.
	fmt.Fprintln(os.Stdout, lease.Path)
	return nil
}

// killLingeringProcesses terminates any process whose cwd is within the given
// worktree. Called before returning a worktree to the pool so detached tools
// (e.g. opencode servers that ignore SIGHUP) don't keep holding the worktree.
func killLingeringProcesses(wtPath string) {
	killed, err := process.TerminateWorktreeProcesses(wtPath, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "🌳 Warning: failed to scan for lingering processes: %v\n", err)
		return
	}
	if len(killed) == 0 {
		return
	}
	names := make([]string, len(killed))
	for i, p := range killed {
		names[i] = p.String()
	}
	fmt.Fprintf(os.Stderr, "🌳 Terminated lingering processes: %s\n", strings.Join(names, ", "))
}
