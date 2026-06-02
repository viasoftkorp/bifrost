package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// MCPLibraryEntry is the JSON shape a single server has in the remote MCP
// library catalog (the payload fetched from DefaultMCPLibraryURL / custom URL).
// It maps 1:1 to TableMCPLibrary minus the DB-only fields (ID, CreatedAt,
// UpdatedAt) which are managed by the upsert.
type MCPLibraryEntry struct {
	Slug               string                   `json:"slug"`
	Name               string                   `json:"name"`
	Description        string                   `json:"description,omitempty"`
	Category           string                   `json:"category,omitempty"`
	ConnectionType     schemas.MCPConnectionType `json:"connection_type"`
	ConnectionURL      string                   `json:"connection_url,omitempty"`
	StdioConfig        *schemas.MCPStdioConfig   `json:"stdio_config,omitempty"`
	AuthType           schemas.MCPAuthType       `json:"auth_type,omitempty"`
	RequiredHeaderKeys []string                  `json:"required_header_keys,omitempty"`
	IconURL            string                   `json:"icon_url,omitempty"`
	DocsURL            string                   `json:"docs_url,omitempty"`
	Publisher          string                   `json:"publisher,omitempty"`
	Version            string                   `json:"version,omitempty"`
	Tags               []string                 `json:"tags,omitempty"`
	Metadata           map[string]any           `json:"metadata,omitempty"`
}

// MCPLibraryPayload is the top-level JSON envelope returned by the remote
// MCP library catalog endpoint.
type MCPLibraryPayload struct {
	Servers       []MCPLibraryEntry `json:"servers"`
	LastUpdatedAt string            `json:"lastUpdatedAt,omitempty"`
}

// SyncMCPLibrary fetches the MCP server catalog from url, parses it as a JSON
// array of MCPLibraryEntry, and upserts each row into the mcp_library table
// keyed by slug. Returns the number of rows upserted.
//
// The function is intentionally stateless and operates directly on the
// ConfigStore so it can be called from both the force-sync handler and the
// background worker without needing a dedicated manager struct.
func SyncMCPLibrary(ctx context.Context, url string, store configstore.ConfigStore) (int, error) {
	if url == "" {
		url = DefaultMCPLibraryURL
	}

	entries, err := WithRetries(ctx, urlFetchMaxRetries, urlFetchMaxBackoff, func() ([]MCPLibraryEntry, error) {
		return fetchMCPLibrary(ctx, url)
	})
	if err != nil {
		return 0, fmt.Errorf("failed to fetch MCP library from %s: %w", url, err)
	}

	if len(entries) == 0 {
		return 0, nil
	}

	// Upsert all entries in a single transaction.
	count := 0
	err = store.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		seen := make(map[string]bool, len(entries))
		for i := range entries {
			e := &entries[i]
			if e.Slug == "" || e.Name == "" {
				continue // skip malformed entries
			}
			if seen[e.Slug] {
				continue // deduplicate within the payload
			}
			seen[e.Slug] = true

			now := time.Now()
			row := &configstoreTables.TableMCPLibrary{
				Slug:               e.Slug,
				Name:               e.Name,
				Description:        e.Description,
				Category:           e.Category,
				ConnectionType:     e.ConnectionType,
				ConnectionURL:      e.ConnectionURL,
				StdioConfig:        e.StdioConfig,
				AuthType:           e.AuthType,
				RequiredHeaderKeys: e.RequiredHeaderKeys,
				IconURL:            e.IconURL,
				DocsURL:            e.DocsURL,
				Publisher:          e.Publisher,
				Version:            e.Version,
				Tags:               e.Tags,
				Metadata:           e.Metadata,
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if err := store.UpsertMCPLibraryEntry(ctx, row, tx); err != nil {
				return fmt.Errorf("failed to upsert MCP library entry %q: %w", e.Slug, err)
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to sync MCP library to database: %w", err)
	}

	return count, nil
}

// syncMCPLibrary is the ModelCatalog method called by the background sync worker
// and ForceReloadPricing. It delegates to the stateless SyncMCPLibrary function
// using the catalog's configured URL and config store.
func (mc *ModelCatalog) syncMCPLibrary(ctx context.Context) error {
	if mc.configStore == nil {
		return nil
	}
	url := mc.getMCPLibraryURL()
	count, err := SyncMCPLibrary(ctx, url, mc.configStore)
	if err != nil {
		return err
	}
	mc.logger.Info("MCP library sync completed: %d entries synced from %s", count, url)
	return nil
}

// getMCPLibraryURL returns a copy of the MCP library URL under mutex protection.
func (mc *ModelCatalog) getMCPLibraryURL() string {
	mc.syncMu.RLock()
	defer mc.syncMu.RUnlock()
	return mc.mcpLibraryURL
}

// fetchMCPLibrary downloads and parses the MCP library JSON from the given URL.
func fetchMCPLibrary(ctx context.Context, url string) ([]MCPLibraryEntry, error) {
	client := &http.Client{Timeout: DefaultMCPLibraryTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download MCP library data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download MCP library data: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP library response: %w", err)
	}

	var payload MCPLibraryPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal MCP library data: %w", err)
	}

	return payload.Servers, nil
}
