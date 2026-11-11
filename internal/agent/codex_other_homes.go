package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
)

// syncOtherCodexCatalogs refreshes existing lists in other local Codex
// homes. Their full Agent.Sync would also switch providers, move threads
// and change sign-ins, all of which belong to that home's owner.
func syncOtherCodexCatalogs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, dir := range appdir.CodexCatalogHomesIn(home)[1:] {
		if err := syncCodexCatalogIn(dir); err != nil {
			log.Printf("codex: model catalog in %s: %v", dir, err)
		}
	}
}

// syncCodexCatalogIn writes only an existing managed catalog named by a
// config still pointing at this gateway. Custom lists, remote magpies,
// profiles and files whose contents are unknown are left alone. It reads
// no secondary home's auth.json or keychain, and never edits config.toml.
func syncCodexCatalogIn(dir string) error {
	data, err := edit.Read(filepath.Join(dir, "config.toml"))
	if err != nil {
		return err
	}
	var cfg struct {
		Model     string `toml:"model"`
		Provider  string `toml:"model_provider"`
		Catalog   string `toml:"model_catalog_json"`
		Effort    string `toml:"model_reasoning_effort"`
		BaseURL   string `toml:"openai_base_url"`
		Profile   string `toml:"profile"`
		Providers map[string]struct {
			BaseURL string `toml:"base_url"`
			Token   string `toml:"experimental_bearer_token"`
		} `toml:"model_providers"`
	}
	if toml.Unmarshal(edit.TOMLWithout(edit.TrimBOM(data), "projects"), &cfg) != nil || cfg.Profile != "" {
		return nil
	}
	name := "magpie-desktop-models.json"
	switch cfg.Provider {
	case "", "openai":
		if strings.TrimSuffix(cfg.BaseURL, "/") != gateway.URL()+gateway.CodexPath {
			return nil
		}
	case magpieID:
		p := cfg.Providers[magpieID]
		if strings.TrimSuffix(p.BaseURL, "/") != gateway.URL()+"/v1" || !ourKey(p.Token) {
			return nil
		}
		name = "magpie-models.json"
	default:
		return nil
	}
	path := cfg.Catalog
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if filepath.Clean(path) != filepath.Join(dir, name) {
		return nil
	}
	// A home may have aliases; a catalog redirected out of it is another
	// owner's file. Never create a missing catalog in a secondary home.
	realDir, dirErr := filepath.EvalSymlinks(dir)
	realPath, pathErr := filepath.EvalSymlinks(path)
	if dirErr != nil || pathErr != nil || realPath != filepath.Join(realDir, name) || edit.IsHardLink(path) {
		return nil
	}
	cur, err := edit.Read(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	var models []map[string]any
	if json.Unmarshal(cur, &doc) != nil || doc == nil {
		return nil
	}
	decode := json.NewDecoder(bytes.NewReader(doc["models"]))
	decode.UseNumber()
	if decode.Decode(&models) != nil || models == nil {
		return nil
	}
	for _, m := range models {
		if slug, _ := m["slug"].(string); slug == "" {
			return nil
		}
	}
	var b []byte
	if name == "magpie-models.json" {
		b = codexcat.CatalogUsed(magpieModels("codex"), cfg.Effort, []string{cfg.Model})
	} else {
		// The default home's live backend list belongs to its account.
		// Keep this account's native (bare) slugs and their metadata;
		// qualified slugs in this managed file are magpie's additions.
		native := map[string]bool{}
		var own []any
		for _, m := range models {
			slug := m["slug"].(string)
			description, _ := m["description"].(string)
			if strings.Contains(slug, "/") || catalog.MagpieAdded("", slug, description) {
				continue
			}
			if n, ok := m["priority"].(json.Number); ok {
				priority, err := strconv.Atoi(n.String())
				if err != nil {
					return nil
				}
				m["priority"] = priority
			}
			native[slug] = true
			own = append(own, m)
		}
		more := provider.CodexListedBeside(native)
		all := append(own, codexcat.EntriesAt(more, len(own)+100, cfg.Effort)...)
		if order, ok := provider.CodexOrder(); ok {
			codexcat.Order(all, order)
		}
		codexcat.FitOwn(all, []string{cfg.Model}, func(slug string) bool { return native[slug] || codexcat.ChatGPTOwn(slug) })
		b, err = json.Marshal(map[string]any{"models": all})
		if err != nil {
			return err
		}
	}
	// Keep preserves user-added model fields; merge its models back into
	// the original envelope so top-level metadata survives as well.
	var fresh map[string]json.RawMessage
	if err := json.Unmarshal(codexcat.Keep(cur, b), &fresh); err != nil {
		return err
	}
	doc["models"] = fresh["models"]
	b, err = json.MarshalIndent(doc, "", " ")
	if err != nil || bytes.Equal(cur, b) {
		return err
	}
	// The owner may have disconnected or edited the catalog while its new
	// entries were built. Leave that change for the next sync to inspect.
	if now, err := edit.Read(filepath.Join(dir, "config.toml")); err != nil || !bytes.Equal(now, data) {
		return err
	}
	if now, err := edit.Read(path); err != nil || !bytes.Equal(now, cur) {
		return err
	}
	return edit.WriteAtomic(path, b)
}
