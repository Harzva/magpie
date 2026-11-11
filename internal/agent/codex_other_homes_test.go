package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// A desktop magpie started without CODEX_HOME used to refresh ~/.codex
// alone. A second account's picker kept the old list, even after Sync.
// Each account keeps its own native entries, settings and sign-in.
func TestCodexOtherHomesCatalogSync(t *testing.T) {
	home, _ := codexHome(t, `{"tokens":{"access_token":"x","id_token":"x.e30.x"}}`, "model = \"gpt-6-sol\"\n")
	app := true
	codexAppReads(t, &app)
	mainList := `{"models":[{"slug":"main-account-only","base_instructions":"main account"}]}`
	deskListIs(t, &mainList)
	if _, err := codex(home).Join(); err != nil {
		t.Fatal(err)
	}

	type account struct{ dir, config, catalog, auth, native string }
	var accounts []account
	for i, rel := range []string{".codex-work", ".codex-managed-agent/accounts/work"} {
		dir := filepath.Join(home, filepath.FromSlash(rel))
		native := fmt.Sprintf("account-%d-only", i)
		config := fmt.Sprintf("# this account\nmodel = \"fake/m1\"\nmodel_reasoning_effort = \"high\"\nopenai_base_url = %q\nmodel_catalog_json = %q\n[desktop]\ntheme = \"dark\"\n", gateway.URL()+gateway.CodexPath, filepath.Join(dir, "magpie-desktop-models.json"))
		catalog := fmt.Sprintf(`{"account_note":"keep me","models":[{"slug":%q,"base_instructions":"this account","context_window":123456,"priority":1},{"slug":"retired/m0","description":"Old via magpie"}]}`, native)
		auth := fmt.Sprintf("{\"account-marker\":%q}", native)
		writeFile(t, filepath.Join(dir, "config.toml"), config)
		writeFile(t, filepath.Join(dir, "magpie-desktop-models.json"), catalog)
		writeFile(t, filepath.Join(dir, "auth.json"), auth)
		accounts = append(accounts, account{dir, config, catalog, auth, native})
	}
	SyncCatalog()
	for _, a := range accounts {
		path := filepath.Join(a.dir, "magpie-desktop-models.json")
		got := readFile(path)
		if !strings.Contains(got, `"fake/m1"`) || strings.Contains(got, `"retired/m0"`) {
			t.Errorf("%s: catalog did not follow magpie's models: %s", a.dir, got)
		}
		var list struct {
			Note   string           `json:"account_note"`
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal([]byte(got), &list); err != nil {
			t.Fatal(err)
		}
		if list.Note != "keep me" || !strings.Contains(got, a.native) || strings.Contains(got, "main-account-only") {
			t.Errorf("%s: native account metadata changed: %s", a.dir, got)
		}
		for _, m := range list.Models {
			if m["slug"] == a.native && (m["base_instructions"] != "this account" || m["context_window"] != float64(123456)) {
				t.Errorf("native model changed: %v", m)
			}
			if m["slug"] == "fake/m1" && m["default_reasoning_level"] != "high" {
				t.Errorf("%s: this account's effort was not used: %v", a.dir, m["default_reasoning_level"])
			}
		}
		if readFile(filepath.Join(a.dir, "config.toml")) != a.config || readFile(filepath.Join(a.dir, "auth.json")) != a.auth {
			t.Errorf("%s: sync wrote account settings or sign-in", a.dir)
		}
		// Identical syncs do not tell the app its catalog changed again.
		old := time.Unix(1_600_000_000, 0)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		SyncCatalog()
		st, err := os.Stat(path)
		if err != nil || !st.ModTime().Equal(old) || readFile(path) != got {
			t.Errorf("%s: unchanged catalog was rewritten: %v", a.dir, err)
		}
	}
}

func TestCodexOtherHomesProviderCatalog(t *testing.T) {
	home, _ := codexHome(t, "", "")
	dir := filepath.Join(home, ".codex-provider")
	path := filepath.Join(dir, "magpie-models.json")
	config := fmt.Sprintf("model = \"fake/m1\"\nmodel_reasoning_effort = \"low\"\nmodel_provider = \"magpie\"\nmodel_catalog_json = \"magpie-models.json\"\n[model_providers.magpie]\nbase_url = %q\nexperimental_bearer_token = \"magpie\"\n", gateway.URL()+"/v1")
	writeFile(t, filepath.Join(dir, "config.toml"), config)
	writeFile(t, filepath.Join(dir, "auth.json"), "{\"leave\":true}")
	writeFile(t, path, `{"models":[{"slug":"fake/m1","supports_reasoning_summaries":true},{"slug":"retired/m0"}]}`)
	SyncCatalog()
	got := readFile(path)
	if !strings.Contains(got, `"base_instructions"`) || !strings.Contains(got, `"default_reasoning_level": "low"`) || !strings.Contains(got, `"supports_reasoning_summaries": true`) || strings.Contains(got, "retired/m0") {
		t.Fatalf("provider catalog was not refreshed with its own defaults: %s", got)
	}
	if readFile(filepath.Join(dir, "config.toml")) != config || readFile(filepath.Join(dir, "auth.json")) != "{\"leave\":true}" {
		t.Fatal("provider settings or sign-in changed")
	}
	// A newly fetched vendor list reaches every connected home too.
	if err := provider.Save(provider.Provider{ID: "fake", Name: "Fake", Key: "k", Chat: "http://127.0.0.1:1/v1", Models: []string{"m2"}}); err != nil {
		t.Fatal(err)
	}
	SyncCatalog()
	if got = readFile(path); !strings.Contains(got, "fake/m2") || strings.Contains(got, `"fake/m1"`) {
		t.Fatalf("vendor change did not reach the other home: %s", got)
	}
}

func TestCodexOtherHomesKeepUnmanagedCatalogs(t *testing.T) {
	home, _ := codexHome(t, "", "")
	old := `{"models":[{"slug":"retired/m0","description":"Old via magpie"}]}`
	for _, kind := range []string{"custom", "disconnected", "remote", "broken-config", "broken-catalog", "missing", "other-home"} {
		dir := filepath.Join(home, ".codex-profiles", kind)
		catalog := filepath.Join(dir, "magpie-desktop-models.json")
		ref, base, content := catalog, gateway.URL()+gateway.CodexPath, old
		switch kind {
		case "custom":
			ref = filepath.Join(dir, "mine.json")
		case "disconnected":
			base = ""
		case "remote":
			base = "http://127.0.0.1:1" + gateway.CodexPath
		case "broken-catalog":
			content = "{"
		case "other-home":
			ref = filepath.Join(home, ".codex", "magpie-desktop-models.json")
		}
		config := fmt.Sprintf("openai_base_url = %q\nmodel_catalog_json = %q\n", base, ref)
		if kind == "broken-config" {
			config += "[broken\n"
		}
		writeFile(t, filepath.Join(dir, "config.toml"), config)
		writeFile(t, filepath.Join(dir, "auth.json"), "{}")
		if kind != "missing" {
			writeFile(t, catalog, content)
		}
		SyncCatalog()
		if readFile(filepath.Join(dir, "config.toml")) != config {
			t.Errorf("%s: config changed", kind)
		}
		if kind == "missing" {
			if _, err := os.Stat(catalog); !os.IsNotExist(err) {
				t.Errorf("missing catalog recreated: %v", err)
			}
		} else if readFile(catalog) != content {
			t.Errorf("%s: unmanaged or unknown catalog changed", kind)
		}
	}
}
