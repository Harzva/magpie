package appdir

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The folders Codex is run in with CODEX_HOME set, one account each, are
// found beside ~/.codex: ~/.codex-work, one folder down in
// ~/.codex-profiles, and not a folder Codex never signed in or kept a
// session in, nor ~/.codex a second time through a link.
func TestCodexHomesIn(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return filepath.Dir(p)
	}
	main := mk(".codex/auth.json")
	work := mk(".codex-work/auth.json")
	nested := filepath.Join(home, ".codex-profiles", "b")
	if err := os.MkdirAll(filepath.Join(nested, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk(".codex-notes/todo.txt")
	mk("projects/auth.json")
	if err := os.Symlink(main, filepath.Join(home, ".codex-link")); err != nil {
		t.Skip(err)
	}
	got := CodexHomesIn(home)
	if want := []string{main, nested, work}; !slices.Equal(got[:1], want[:1]) || len(got) != 3 ||
		!slices.Contains(got, nested) || !slices.Contains(got, work) {
		t.Fatalf("CodexHomesIn = %v, want %v", got, want)
	}
}

// Account managers keep homes a level below their accounts container,
// including when the manager's root itself has a Codex sign-in.
func TestCodexCatalogHomesInAccountContainers(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	root := filepath.Join(home, ".codex-managed-agent")
	want := []string{root, filepath.Join(root, "accounts", "personal"), filepath.Join(root, "accounts", "work")}
	for _, dir := range want {
		if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Other app folders and arbitrarily deep trees are not searched.
	for _, dir := range []string{filepath.Join(home, "projects", "accounts", "no"), filepath.Join(root, "notes", "nested")} {
		if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := CodexCatalogHomesIn(home)
	if len(got) != len(want)+1 {
		t.Fatalf("homes = %v, want default and %v", got, want)
	}
	for _, dir := range want {
		if !slices.Contains(got, dir) {
			t.Errorf("account home %s was not found: %v", dir, got)
		}
	}
	// Credential and session discovery keep their existing scope.
	if got := CodexHomesIn(home); len(got) != 2 || got[1] != root {
		t.Fatalf("catalog discovery expanded sign-in discovery too: %v", got)
	}
}
