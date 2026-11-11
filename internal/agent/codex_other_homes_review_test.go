package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

func otherHomeReviewConfig(dir, model string) string {
	return fmt.Sprintf("model = %q\nmodel_reasoning_effort = \"high\"\nopenai_base_url = %q\nmodel_catalog_json = %q\n", model, gateway.URL()+gateway.CodexPath, filepath.Join(dir, "magpie-desktop-models.json"))
}

// A large catalog keeps this account's native models and selected model
// within the page the desktop picker actually reads.
func TestCodexOtherHomesReviewLargeCatalog(t *testing.T) {
	home, _ := codexHome(t, "", "")
	var names []string
	for i := range 130 {
		names = append(names, fmt.Sprintf("m%03d", i))
	}
	if err := provider.Save(provider.Provider{ID: "fake", Name: "Fake", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: names}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".codex-work")
	config := otherHomeReviewConfig(dir, "fake/m129")
	writeFile(t, filepath.Join(dir, "config.toml"), config)
	path := filepath.Join(dir, "magpie-desktop-models.json")
	writeFile(t, path, `{"models":[{"slug":"native-only","priority":900,"base_instructions":"account-owned","opaque_number":9007199254740993},{"slug":"native-hidden","priority":1,"visibility":"hide"}]}`)
	if err := syncCodexCatalogIn(dir); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Models []struct {
			Slug         string      `json:"slug"`
			Priority     int         `json:"priority"`
			Instructions string      `json:"base_instructions"`
			Opaque       json.Number `json:"opaque_number"`
			Visibility   string      `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(readFile(path)), &list); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, m := range list.Models {
		found[m.Slug] = true
		if (m.Slug == "native-only" || m.Slug == "fake/m129") && m.Priority > codexcat.AppList {
			t.Errorf("%s is outside the desktop's first page: %d", m.Slug, m.Priority)
		}
		if m.Slug == "native-only" && (m.Instructions != "account-owned" || m.Opaque.String() != "9007199254740993") {
			t.Errorf("native metadata changed: %+v", m)
		}
		if m.Slug == "native-hidden" && m.Visibility != "hide" {
			t.Errorf("native hidden model became visible: %+v", m)
		}
	}
	for _, slug := range []string{"native-only", "native-hidden", "fake/m129"} {
		if !found[slug] {
			t.Errorf("model missing: %s", slug)
		}
	}
	if readFile(filepath.Join(dir, "config.toml")) != config {
		t.Fatal("selected model or config changed")
	}
}

// Explicit CODEX_HOME and another spelling of it never turn the primary
// account into a secondary update target.
func TestCodexOtherHomesReviewExplicitHome(t *testing.T) {
	home, _ := codexHome(t, "", "")
	primary := filepath.Join(home, ".codex-primary")
	secondary := filepath.Join(home, ".codex")
	old := `{"models":[{"slug":"native-only"}]}`
	for _, dir := range []string{primary, secondary} {
		writeFile(t, filepath.Join(dir, "auth.json"), "{}")
		writeFile(t, filepath.Join(dir, "config.toml"), otherHomeReviewConfig(dir, "fake/m1"))
		writeFile(t, filepath.Join(dir, "magpie-desktop-models.json"), old)
	}
	alias := filepath.Join(home, ".codex-alias")
	if err := os.Symlink(primary, alias); err != nil {
		t.Skip(err)
	}
	t.Setenv("CODEX_HOME", alias)
	syncOtherCodexCatalogs()
	if readFile(filepath.Join(primary, "magpie-desktop-models.json")) != old {
		t.Fatal("primary alias was treated as another account")
	}
	if readFile(filepath.Join(secondary, "magpie-desktop-models.json")) == old {
		t.Fatal("default home did not update with explicit CODEX_HOME elsewhere")
	}
	if os.Getenv("CODEX_HOME") != alias {
		t.Fatal("sync changed CODEX_HOME")
	}
}

func TestCodexOtherHomesReviewUnknownOrUnmanaged(t *testing.T) {
	for _, kind := range []string{"manual-provider", "wrong-key", "profile", "null-list", "object-list", "null-entry", "missing-slug", "redirected", "hardlinked"} {
		t.Run(kind, func(t *testing.T) {
			home, _ := codexHome(t, "", "")
			dir := filepath.Join(home, ".codex-work")
			path := filepath.Join(dir, "magpie-desktop-models.json")
			config := otherHomeReviewConfig(dir, "fake/m1")
			old := `{"models":[{"slug":"native-only"}]}`
			switch kind {
			case "manual-provider", "wrong-key":
				path = filepath.Join(dir, "magpie-models.json")
				base, token := "https://relay.invalid/v1", "magpie"
				if kind == "wrong-key" {
					base, token = gateway.URL()+"/v1", "someone-elses-key"
				}
				config = fmt.Sprintf("model_provider = \"magpie\"\nmodel_catalog_json = \"magpie-models.json\"\n[model_providers.magpie]\nbase_url = %q\nexperimental_bearer_token = %q\n", base, token)
			case "profile":
				config += "profile = \"work\"\n[profiles.work]\nmodel_provider = \"custom\"\n"
			case "null-list":
				old = `{"models":null}`
			case "object-list":
				old = `{"models":{}}`
			case "null-entry":
				old = `{"models":[null]}`
			case "missing-slug":
				old = `{"models":[{"description":"unknown"}]}`
			}
			writeFile(t, filepath.Join(dir, "config.toml"), config)
			writeFile(t, path, old)
			if kind == "redirected" {
				outside := filepath.Join(home, "other-owner.json")
				writeFile(t, outside, old)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip(err)
				}
			}
			outside := filepath.Join(home, "other-owner-hardlink.json")
			if kind == "hardlinked" {
				if err := os.Link(path, outside); err != nil {
					t.Skip(err)
				}
			}
			if err := syncCodexCatalogIn(dir); err != nil {
				t.Fatal(err)
			}
			if readFile(path) != old || readFile(filepath.Join(dir, "config.toml")) != config {
				t.Fatal("unknown or unmanaged state was overwritten")
			}
			if kind == "hardlinked" && readFile(outside) != old {
				t.Fatal("another owner's hardlinked catalog was overwritten")
			}
		})
	}
}

func TestCodexOtherHomesReviewUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions are not Windows file access controls")
	}
	for _, name := range []string{"config.toml", "magpie-desktop-models.json"} {
		t.Run(name, func(t *testing.T) {
			home, _ := codexHome(t, "", "")
			dir := filepath.Join(home, ".codex-work")
			config := otherHomeReviewConfig(dir, "fake/m1")
			old := `{"models":[{"slug":"native-only"}]}`
			writeFile(t, filepath.Join(dir, "config.toml"), config)
			writeFile(t, filepath.Join(dir, "magpie-desktop-models.json"), old)
			locked := filepath.Join(dir, name)
			if err := os.Chmod(locked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
			if _, err := os.ReadFile(locked); err == nil {
				t.Skip("this user can read mode-000 files")
			}
			if err := syncCodexCatalogIn(dir); err == nil {
				t.Fatal("unreadable file was treated as known")
			}
			if err := os.Chmod(locked, 0o600); err != nil {
				t.Fatal(err)
			}
			if readFile(filepath.Join(dir, "magpie-desktop-models.json")) != old || readFile(filepath.Join(dir, "config.toml")) != config {
				t.Fatal("unreadable state was overwritten")
			}
		})
	}
}
