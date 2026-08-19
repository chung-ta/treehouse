package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePoolDir_EmptyRoot(t *testing.T) {
	// With empty root, pool dir should be $HOME/.treehouse/{repoName}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	repoDir := setupGitRepo(t)

	poolDir, err := ResolvePoolDir(repoDir, "")
	if err != nil {
		t.Fatalf("ResolvePoolDir failed: %v", err)
	}

	repoName := filepath.Base(repoDir)
	expected := filepath.Join(home, ".treehouse", repoName)
	if poolDir != expected {
		t.Errorf("expected pool dir %s, got %s", expected, poolDir)
	}
}

func TestResolvePoolDir_RelativeRoot(t *testing.T) {
	repoDir := setupGitRepo(t)

	poolDir, err := ResolvePoolDir(repoDir, ".worktrees")
	if err != nil {
		t.Fatalf("ResolvePoolDir failed: %v", err)
	}

	repoName := filepath.Base(repoDir)
	expected := filepath.Join(repoDir, ".worktrees", repoName)
	if poolDir != expected {
		t.Errorf("expected pool dir %s, got %s", expected, poolDir)
	}
}

func TestResolvePoolDir_AbsoluteRoot(t *testing.T) {
	repoDir := setupGitRepo(t)
	absRoot := t.TempDir()

	poolDir, err := ResolvePoolDir(repoDir, absRoot)
	if err != nil {
		t.Fatalf("ResolvePoolDir failed: %v", err)
	}

	repoName := filepath.Base(repoDir)
	expected := filepath.Join(absRoot, repoName)
	if poolDir != expected {
		t.Errorf("expected pool dir %s, got %s", expected, poolDir)
	}
}

func TestResolvePoolDir_DotSlashRoot(t *testing.T) {
	repoDir := setupGitRepo(t)

	poolDir, err := ResolvePoolDir(repoDir, "./")
	if err != nil {
		t.Fatalf("ResolvePoolDir failed: %v", err)
	}

	repoName := filepath.Base(repoDir)
	expected := filepath.Join(repoDir, repoName)
	if poolDir != expected {
		t.Errorf("expected pool dir %s, got %s", expected, poolDir)
	}
}

func TestResolvePoolDir_EnvVarExpansion(t *testing.T) {
	repoDir := setupGitRepo(t)
	absRoot := t.TempDir()

	t.Setenv("TEST_TREEHOUSE_ROOT", absRoot)

	poolDir, err := ResolvePoolDir(repoDir, "$TEST_TREEHOUSE_ROOT")
	if err != nil {
		t.Fatalf("ResolvePoolDir failed: %v", err)
	}

	repoName := filepath.Base(repoDir)
	expected := filepath.Join(absRoot, repoName)
	if poolDir != expected {
		t.Errorf("expected pool dir %s, got %s", expected, poolDir)
	}
}

func TestResolvePoolRoot_EmptyRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	poolRoot, err := ResolvePoolRoot("", "")
	if err != nil {
		t.Fatalf("ResolvePoolRoot failed: %v", err)
	}

	expected := filepath.Join(home, ".treehouse")
	if poolRoot != expected {
		t.Fatalf("expected pool root %s, got %s", expected, poolRoot)
	}
}

func TestResolvePoolRoot_RelativeRootRequiresRepo(t *testing.T) {
	if _, err := ResolvePoolRoot("", ".worktrees"); err == nil {
		t.Fatal("expected relative root without repo to fail")
	}
}
