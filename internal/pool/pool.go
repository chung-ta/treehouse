package pool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kunchenguid/treehouse/internal/git"
	"github.com/kunchenguid/treehouse/internal/hooks"
	"github.com/kunchenguid/treehouse/internal/process"
)

const (
	StatusAvailable = "available"
	StatusDirty     = "dirty"
	StatusInUse     = "in-use"
	StatusLeased    = "leased"
	StatusHere      = "you're here"
)

// WorktreeStatus describes one managed worktree as reported by List.
type WorktreeStatus struct {
	Name string
	Path string
	// Branch is the branch created for this worktree's task.
	Branch    string
	Status    string
	Processes []process.ProcessInfo
	// LeaseID identifies the current acquisition of a leased worktree.
	LeaseID string
	// LeaseHolder is the recorded holder for a leased worktree, if any.
	LeaseHolder string
	// LeasedAt records when the current lease was acquired.
	LeasedAt time.Time
}

// LeaseInfo is the stable machine-readable identity of one lease acquisition.
type LeaseInfo struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Resumed reports that Branch already existed and was checked out rather
	// than created, meaning this task carries earlier work.
	Resumed     bool      `json:"resumed"`
	LeaseID     string    `json:"lease_id"`
	LeaseHolder string    `json:"lease_holder"`
	LeasedAt    time.Time `json:"leased_at"`
}

// acquireOptions controls how Acquire creates the worktree it hands out.
type acquireOptions struct {
	// slug is the task name. It is used verbatim as both the worktree
	// directory name and the branch name, so it must already be sanitized
	// by the slug package.
	slug string
	// lease records a durable, process-independent reservation instead of the
	// default short-lived owner reservation.
	lease bool
	// leaseHolder is an optional label stored with a lease.
	leaseHolder string
	// hookStdout/hookStderr receive post-create hook output. Lease mode routes
	// hook stdout to stderr so it cannot contaminate machine-readable CLI output.
	hookStdout io.Writer
	hookStderr io.Writer
}

// Acquire creates a worktree named slug, checked out on a new branch named
// slug, with a short-lived owner reservation (the calling process). It is the
// backing call for the interactive `treehouse get` subshell.
func Acquire(repoRoot, poolDir, slug string, poolSize int, postCreate []string) (string, error) {
	acquired, err := AcquireInfo(repoRoot, poolDir, slug, poolSize, postCreate)
	return acquired.Path, err
}

// AcquireInfo creates a worktree exactly like Acquire and returns the whole
// allocation, including whether it resumed an existing branch.
func AcquireInfo(repoRoot, poolDir, slug string, poolSize int, postCreate []string) (LeaseInfo, error) {
	return acquire(repoRoot, poolDir, poolSize, postCreate, acquireOptions{
		slug:       slug,
		hookStdout: os.Stdout,
		hookStderr: os.Stderr,
	})
}

// AcquireLease creates a worktree exactly like Acquire and marks it durably
// LEASED so the reservation survives with zero processes running inside it. The lease persists
// until it is released by Release. holder is an optional label recorded with the
// lease for diagnostics. Post-create hook stdout is routed to stderr so callers
// can emit machine-readable allocation output without hook output on stdout.
func AcquireLease(repoRoot, poolDir, slug string, poolSize int, postCreate []string, holder string) (string, error) {
	lease, err := AcquireLeaseInfo(repoRoot, poolDir, slug, poolSize, postCreate, holder)
	return lease.Path, err
}

// AcquireLeaseInfo creates a worktree exactly like AcquireLease and returns
// the immutable identity and metadata for that acquisition.
func AcquireLeaseInfo(repoRoot, poolDir, slug string, poolSize int, postCreate []string, holder string) (LeaseInfo, error) {
	return acquire(repoRoot, poolDir, poolSize, postCreate, acquireOptions{
		slug:        slug,
		lease:       true,
		leaseHolder: holder,
		hookStdout:  os.Stderr,
		hookStderr:  os.Stderr,
	})
}

// acquire creates a new worktree named for the task and checks it out on a new
// branch of the same name. Worktrees are never recycled between tasks: the
// directory name and the branch name both encode the task, so a worktree can
// only ever serve the task it was created for.
func acquire(repoRoot, poolDir string, poolSize int, postCreate []string, opts acquireOptions) (LeaseInfo, error) {
	if opts.slug == "" {
		return LeaseInfo{}, fmt.Errorf("a task name is required to create a worktree")
	}

	base, err := git.GetDefaultBranch(repoRoot)
	if err != nil {
		return LeaseInfo{}, err
	}

	fmt.Fprintf(os.Stderr, "🌳 Setting up worktree...\n")
	if git.HasRemote(repoRoot, "origin") {
		if err := git.Fetch(repoRoot); err != nil {
			return LeaseInfo{}, fmt.Errorf("fetch failed: %w", err)
		}
	}

	// The state lock lives inside poolDir, so the directory has to exist
	// before the lock is taken.
	if err := os.MkdirAll(poolDir, 0755); err != nil {
		return LeaseInfo{}, err
	}

	var acquired LeaseInfo
	var runPostCreate bool

	err = WithStateLock(poolDir, func() error {
		state, err := ReadState(poolDir)
		if err != nil {
			return err
		}

		state = healState(state)

		wtPath := filepath.Join(poolDir, opts.slug)

		for _, wt := range state.Worktrees {
			if wt.Name == opts.slug || wt.Path == wtPath {
				return fmt.Errorf("a worktree for %q already exists at %s. Run 'treehouse enter %s' to resume it, 'treehouse return %s' to finish it, or use a different description", opts.slug, wt.Path, wt.Path, wt.Path)
			}
		}

		if len(state.Worktrees) >= poolSize {
			return fmt.Errorf("%d worktrees already exist (max_trees = %d). Run 'treehouse status' to see them, return the ones you have finished, or increase max_trees in treehouse.toml", len(state.Worktrees), poolSize)
		}

		// Fail before touching git if either name is already taken, so the
		// user gets an explanation instead of a raw git error.
		if _, err := os.Stat(wtPath); err == nil {
			return fmt.Errorf("%s already exists but is not managed by treehouse. Remove it or use a different description", wtPath)
		} else if !os.IsNotExist(err) {
			return err
		}
		// A branch outlives the worktree it was created in, so an existing
		// branch means this task was worked on before. Resume it rather than
		// refusing the name or starting a second branch for the same task.
		resumed := git.BranchExists(repoRoot, opts.slug)
		if resumed {
			if err := git.AddWorktreeOnBranch(repoRoot, wtPath, opts.slug); err != nil {
				return fmt.Errorf("failed to create worktree on existing branch %s: %w", opts.slug, err)
			}
		} else if err := git.AddWorktreeWithBranch(repoRoot, wtPath, opts.slug, base); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		entry := WorktreeEntry{
			Name:      opts.slug,
			Path:      wtPath,
			Branch:    opts.slug,
			CreatedAt: time.Now(),
		}
		if err := markAcquired(&entry, opts); err != nil {
			return err
		}
		state.Worktrees = append(state.Worktrees, entry)

		acquired = leaseInfoFromEntry(entry)
		acquired.Resumed = resumed
		if err := WriteState(poolDir, state); err != nil {
			return err
		}
		runPostCreate = true
		return nil
	})
	if err != nil {
		return LeaseInfo{}, err
	}
	if runPostCreate {
		hooks.Run(postCreate, acquired.Path, opts.hookStdout, opts.hookStderr)
	}

	return acquired, nil
}

func leaseInfoFromEntry(wt WorktreeEntry) LeaseInfo {
	return LeaseInfo{
		Path:        wt.Path,
		Branch:      wt.Branch,
		LeaseID:     wt.LeaseID,
		LeaseHolder: wt.LeaseHolder,
		LeasedAt:    wt.LeasedAt,
	}
}

// markAcquired stamps an acquired worktree entry: a durable lease in lease mode,
// otherwise the default short-lived owner reservation.
func markAcquired(wt *WorktreeEntry, opts acquireOptions) error {
	if opts.lease {
		leaseID, err := newLeaseID()
		if err != nil {
			return err
		}
		wt.Leased = true
		wt.LeaseID = leaseID
		wt.LeaseHolder = opts.leaseHolder
		wt.LeasedAt = time.Now()
		// A lease is process-independent, so it carries no owner reservation.
		wt.OwnerPID = 0
		wt.OwnerStartedAt = 0
		return nil
	}
	return reserveOwner(wt)
}

// ErrLeasePreconditionFailed reports that a conditional release no longer
// identifies the worktree's current lease.
var ErrLeasePreconditionFailed = errors.New("lease precondition failed")

// ReleasePreconditions optionally constrain a release to the current lease.
// Pointer fields distinguish an omitted condition from an expected empty value.
type ReleasePreconditions struct {
	ExpectedLeaseID     *string
	ExpectedLeaseHolder *string
}

// Release finishes a managed worktree: it removes the worktree directory and
// drops its state entry. The branch is deliberately left in the repository, so
// committed work and any open PR survive the return. It retains the legacy
// unconditional behavior of releasing by path.
func Release(poolDir, worktreePath string) error {
	return ReleaseConditional(poolDir, worktreePath, ReleasePreconditions{}, nil)
}

// ValidateReleasePreconditions checks that a managed worktree still matches
// the requested lease without performing any release effects.
func ValidateReleasePreconditions(poolDir, worktreePath string, preconditions ReleasePreconditions) error {
	return WithStateLock(poolDir, func() error {
		state, err := ReadState(poolDir)
		if err != nil {
			return err
		}
		_, err = releasableWorktree(&state, worktreePath, preconditions)
		return err
	})
}

// ReleaseConditional verifies any lease preconditions, runs beforeRemove,
// removes the worktree, and drops its state entry while holding one state lock.
// The callback is invoked only after all preconditions match and runs under that
// lock so caller-side termination cannot race another command.
//
// The worktree's branch is NOT deleted. Removing the directory reclaims the
// checkout; the commits on the branch stay in the repository.
func ReleaseConditional(poolDir, worktreePath string, preconditions ReleasePreconditions, beforeRemove func() error) error {
	repoRoot, err := git.FindRepoRootFrom(worktreePath)
	if err != nil {
		return err
	}
	return WithStateLock(poolDir, func() error {
		state, err := ReadState(poolDir)
		if err != nil {
			return err
		}

		index, err := releasableWorktree(&state, worktreePath, preconditions)
		if err != nil {
			return err
		}
		if beforeRemove != nil {
			if err := beforeRemove(); err != nil {
				return err
			}
		}
		if err := git.RemoveWorktree(repoRoot, worktreePath); err != nil {
			return err
		}

		state.Worktrees = append(state.Worktrees[:index], state.Worktrees[index+1:]...)
		return WriteState(poolDir, state)
	})
}

// releasableWorktree returns the index of the managed entry for worktreePath
// once it satisfies preconditions. An index (not a pointer) is returned because
// the caller removes the entry from the slice.
func releasableWorktree(state *State, worktreePath string, preconditions ReleasePreconditions) (int, error) {
	for i := range state.Worktrees {
		wt := &state.Worktrees[i]
		if wt.Path != worktreePath {
			continue
		}
		if wt.Destroying {
			return 0, fmt.Errorf("worktree %s is being destroyed", worktreePath)
		}
		if err := validateReleasePreconditions(*wt, preconditions); err != nil {
			return 0, err
		}
		return i, nil
	}
	return 0, fmt.Errorf("worktree %s is not managed by treehouse", worktreePath)
}

func validateReleasePreconditions(wt WorktreeEntry, preconditions ReleasePreconditions) error {
	if preconditions.ExpectedLeaseID == nil && preconditions.ExpectedLeaseHolder == nil {
		return nil
	}
	if !wt.Leased {
		return fmt.Errorf("%w: worktree %s is not leased", ErrLeasePreconditionFailed, wt.Path)
	}
	if preconditions.ExpectedLeaseID != nil && wt.LeaseID != *preconditions.ExpectedLeaseID {
		return fmt.Errorf("%w: lease identity does not match worktree %s", ErrLeasePreconditionFailed, wt.Path)
	}
	if preconditions.ExpectedLeaseHolder != nil && wt.LeaseHolder != *preconditions.ExpectedLeaseHolder {
		return fmt.Errorf("%w: lease holder does not match worktree %s", ErrLeasePreconditionFailed, wt.Path)
	}
	return nil
}

// List returns the current status of managed worktrees in poolDir.
// Leased worktrees are reported with StatusLeased and their optional holder.
func List(poolDir string) ([]WorktreeStatus, error) {
	var result []WorktreeStatus

	err := WithStateLock(poolDir, func() error {
		state, err := ReadState(poolDir)
		if err != nil {
			return err
		}

		state = healState(state)
		if err := WriteState(poolDir, state); err != nil {
			return err
		}

		cwd, _ := os.Getwd()

		for _, wt := range state.Worktrees {
			if wt.Destroying {
				continue
			}
			ws := WorktreeStatus{
				Name:   wt.Name,
				Path:   wt.Path,
				Branch: wt.Branch,
				Status: StatusAvailable,
			}

			procs, _ := process.FindProcessesInWorktree(wt.Path)
			ws.Processes = procs

			if wt.Leased {
				ws.Status = StatusLeased
				ws.LeaseID = wt.LeaseID
				ws.LeaseHolder = wt.LeaseHolder
				ws.LeasedAt = wt.LeasedAt
			} else if ownerAlive(wt) {
				ws.Status = StatusInUse
			} else if len(procs) > 0 {
				ws.Status = StatusInUse
				if cwdInWorktree(cwd, wt.Path) {
					ws.Status = StatusHere
				}
			} else if dirty, _ := git.IsDirty(wt.Path); dirty {
				ws.Status = StatusDirty
			}

			result = append(result, ws)
		}
		return nil
	})

	return result, err
}

func FindByPath(poolDir, path string) (*WorktreeEntry, error) {
	state, err := ReadState(poolDir)
	if err != nil {
		return nil, err
	}
	for _, wt := range state.Worktrees {
		if wt.Path == path {
			return &wt, nil
		}
	}
	return nil, nil
}

func healState(state State) State {
	var healed []WorktreeEntry
	for _, wt := range state.Worktrees {
		if _, err := os.Stat(wt.Path); err == nil {
			if wt.OwnerPID != 0 && !ownerAlive(wt) {
				wt.OwnerPID = 0
				wt.OwnerStartedAt = 0
				wt.Destroying = false
			}
			healed = append(healed, wt)
		}
	}
	state.Worktrees = healed
	return state
}

func ownerAlive(wt WorktreeEntry) bool {
	if wt.OwnerPID == 0 || wt.OwnerStartedAt == 0 {
		return false
	}
	startedAt, ok := process.StartedAt(wt.OwnerPID)
	return ok && startedAt == wt.OwnerStartedAt
}

func reserveOwner(wt *WorktreeEntry) error {
	pid := int32(os.Getpid())
	startedAt, ok := process.StartedAt(pid)
	if !ok {
		return fmt.Errorf("failed to determine owner process identity")
	}
	wt.OwnerPID = pid
	wt.OwnerStartedAt = startedAt
	return nil
}

// clearLease removes any durable lease from a worktree entry.
func clearLease(wt *WorktreeEntry) {
	wt.Leased = false
	wt.LeaseID = ""
	wt.LeaseHolder = ""
	wt.LeasedAt = time.Time{}
}

func sameDestroyReservation(current, reserved WorktreeEntry) bool {
	return current.Path == reserved.Path &&
		current.Destroying &&
		current.OwnerPID == reserved.OwnerPID &&
		current.OwnerStartedAt == reserved.OwnerStartedAt
}

func cwdInWorktree(cwd, worktreePath string) bool {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	absWt, err := filepath.Abs(worktreePath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absWt, absCwd)
	if err != nil {
		return false
	}
	return rel == "." || !filepath.IsAbs(rel) && len(rel) >= 1 && rel[0] != '.'
}

func nextName(state State) string {
	max := 0
	for _, wt := range state.Worktrees {
		if n, err := strconv.Atoi(wt.Name); err == nil && n > max {
			max = n
		}
	}
	return strconv.Itoa(max + 1)
}
