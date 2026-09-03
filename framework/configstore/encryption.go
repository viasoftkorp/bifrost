package configstore

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	encryptionStatusPlainText = "plain_text"
	encryptionStatusEncrypted = "encrypted"
	encryptionBatchSize       = 100
)

// claimUnlocked claims a batch on Postgres, skipping rows another node is
// already encrypting. SQLite has no row locks, so the query is left as-is.
func claimUnlocked(tx *gorm.DB) *gorm.DB {
	if tx.Dialector.Name() != "postgres" {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
}

// EncryptPlaintextRows encrypts all rows with encryption_status='plain_text'
// across all sensitive tables. Called during startup when encryption is enabled.
// Each table's GORM BeforeSave hook handles the actual encryption.
func (s *RDBConfigStore) EncryptPlaintextRows(ctx context.Context) error {
	if !encrypt.IsEnabled() {
		return nil
	}

	var totalEncrypted int

	// config_keys
	count, err := s.encryptPlaintextKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt config_keys: %w", err)
	}
	totalEncrypted += count

	// governance_virtual_keys
	count, err = s.encryptPlaintextVirtualKeys(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt virtual_keys: %w", err)
	}
	totalEncrypted += count

	// sessions
	count, err = s.encryptPlaintextSessions(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt sessions: %w", err)
	}
	totalEncrypted += count

	// temp_tokens
	count, err = s.encryptPlaintextTempTokens(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt temp_tokens: %w", err)
	}
	totalEncrypted += count

	// mcp_oauth_tokens
	count, err = s.encryptPlaintextOAuthTokens(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt mcp_oauth_tokens: %w", err)
	}
	totalEncrypted += count

	// oauth_configs
	count, err = s.encryptPlaintextOAuthConfigs(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt oauth_configs: %w", err)
	}
	totalEncrypted += count

	// config_mcp_clients
	count, err = s.encryptPlaintextMCPClients(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt mcp_clients: %w", err)
	}
	totalEncrypted += count

	// config_providers (proxy config)
	count, err = s.encryptPlaintextProviderProxies(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt provider proxy configs: %w", err)
	}
	totalEncrypted += count

	// config_vector_store
	count, err = s.encryptPlaintextVectorStoreConfigs(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt vector_store configs: %w", err)
	}
	totalEncrypted += count

	// config_plugins
	count, err = s.encryptPlaintextPlugins(ctx)
	if err != nil {
		return fmt.Errorf("failed to encrypt plugin configs: %w", err)
	}
	totalEncrypted += count

	if totalEncrypted > 0 && s.logger != nil {
		s.logger.Info(fmt.Sprintf("encrypted %d plaintext rows across all tables", totalEncrypted))
	}

	return nil
}

// encryptPlaintextKeys finds all config_keys rows with plaintext encryption status and
// re-saves them in batches. The TableKey.BeforeSave hook handles the actual encryption.
func (s *RDBConfigStore) encryptPlaintextKeys(ctx context.Context) (int, error) {
	var count int
	var cursor uint
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var keys []tables.TableKey
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&keys).Error; err != nil {
				return err
			}
			for i := range keys {
				if err := tx.Save(&keys[i]).Error; err != nil {
					return err
				}
				if keys[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(keys)
			if batch > 0 {
				cursor = keys[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextVirtualKeys finds all governance_virtual_keys rows with plaintext encryption
// status and re-saves them in batches. The TableVirtualKey.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextVirtualKeys(ctx context.Context) (int, error) {
	var count int
	var cursor string
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var vks []tables.TableVirtualKey
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND value != '' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&vks).Error; err != nil {
				return err
			}
			for i := range vks {
				if err := tx.Save(&vks[i]).Error; err != nil {
					return err
				}
				if vks[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(vks)
			if batch > 0 {
				cursor = vks[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextSessions finds all sessions rows with plaintext encryption status and
// re-saves them in batches. The SessionsTable.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextSessions(ctx context.Context) (int, error) {
	var count int
	var cursor int
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var sessions []tables.SessionsTable
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND token != '' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&sessions).Error; err != nil {
				return err
			}
			for i := range sessions {
				if err := tx.Save(&sessions[i]).Error; err != nil {
					return err
				}
				if sessions[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(sessions)
			if batch > 0 {
				cursor = sessions[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextTempTokens finds all temp_tokens rows with plaintext encryption status
// and re-saves them in batches. The TempToken.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextTempTokens(ctx context.Context) (int, error) {
	var count int
	var cursor string
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var tokens []tables.TempToken
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND token != '' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&tokens).Error; err != nil {
				return err
			}
			for i := range tokens {
				if err := tx.Save(&tokens[i]).Error; err != nil {
					return err
				}
				if tokens[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(tokens)
			if batch > 0 {
				cursor = tokens[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextOAuthTokens finds all mcp_oauth_tokens rows with plaintext encryption status
// and re-saves them in batches. The TableMCPOauthToken.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextOAuthTokens(ctx context.Context) (int, error) {
	var count int
	var cursor string
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var tokens []tables.TableMCPOauthToken
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&tokens).Error; err != nil {
				return err
			}
			for i := range tokens {
				if err := tx.Save(&tokens[i]).Error; err != nil {
					return err
				}
				if tokens[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(tokens)
			if batch > 0 {
				cursor = tokens[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextOAuthConfigs finds all oauth_configs rows with plaintext encryption status
// and re-saves them in batches. The TableOauthConfig.BeforeSave hook handles encryption.
// client_secret is the only sensitive column left on this table — state/
// code_verifier/code_challenge/expires_at moved to mcp_oauth_flows (see that
// migration) and code_verifier was the only one of those that was ever
// encrypted, so the WHERE clause below no longer needs an OR branch for it.
func (s *RDBConfigStore) encryptPlaintextOAuthConfigs(ctx context.Context) (int, error) {
	var count int
	var cursor string
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var configs []tables.TableOauthConfig
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND client_secret != '' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&configs).Error; err != nil {
				return err
			}
			for i := range configs {
				if err := tx.Save(&configs[i]).Error; err != nil {
					return err
				}
				if configs[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(configs)
			if batch > 0 {
				cursor = configs[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextMCPClients finds all config_mcp_clients rows with plaintext encryption
// status and re-saves them in batches. The TableMCPClient.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextMCPClients(ctx context.Context) (int, error) {
	var count int
	var cursor uint
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var clients []tables.TableMCPClient
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&clients).Error; err != nil {
				return err
			}
			for i := range clients {
				if err := tx.Save(&clients[i]).Error; err != nil {
					return err
				}
				if clients[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(clients)
			if batch > 0 {
				cursor = clients[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextProviderProxies finds all config_providers rows that have a non-empty
// proxy config with plaintext encryption status and re-saves them in batches. The
// TableProvider.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextProviderProxies(ctx context.Context) (int, error) {
	var count int
	var cursor uint
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var providers []tables.TableProvider
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND proxy_config_json != '' AND proxy_config_json IS NOT NULL AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&providers).Error; err != nil {
				return err
			}
			for i := range providers {
				if err := tx.Save(&providers[i]).Error; err != nil {
					return err
				}
				if providers[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(providers)
			if batch > 0 {
				cursor = providers[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextVectorStoreConfigs finds all config_vector_store rows that have a non-empty
// config with plaintext encryption status and re-saves them in batches. The
// TableVectorStoreConfig.BeforeSave hook handles encryption.
func (s *RDBConfigStore) encryptPlaintextVectorStoreConfigs(ctx context.Context) (int, error) {
	var count int
	var cursor uint
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var configs []tables.TableVectorStoreConfig
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND config IS NOT NULL AND config != '' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&configs).Error; err != nil {
				return err
			}
			for i := range configs {
				if err := tx.Save(&configs[i]).Error; err != nil {
					return err
				}
				if configs[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(configs)
			if batch > 0 {
				cursor = configs[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}

// encryptPlaintextPlugins finds all config_plugins rows that have a non-empty config with
// plaintext encryption status and re-saves them in batches. The TablePlugin.BeforeSave hook
// handles encryption.
func (s *RDBConfigStore) encryptPlaintextPlugins(ctx context.Context) (int, error) {
	var count int
	var cursor uint
	for {
		var batch, encrypted int
		if err := s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			batch, encrypted = 0, 0
			var plugins []tables.TablePlugin
			if err := claimUnlocked(tx).
				Where("(encryption_status = ? OR encryption_status IS NULL OR encryption_status = '') AND config_json != '' AND config_json != '{}' AND id > ?", encryptionStatusPlainText, cursor).
				Order("id").
				Limit(encryptionBatchSize).
				Find(&plugins).Error; err != nil {
				return err
			}
			for i := range plugins {
				if err := tx.Save(&plugins[i]).Error; err != nil {
					return err
				}
				if plugins[i].EncryptionStatus == encryptionStatusEncrypted {
					encrypted++
				}
			}
			batch = len(plugins)
			if batch > 0 {
				cursor = plugins[batch-1].ID
			}
			return nil
		}); err != nil {
			return count, err
		}
		if batch == 0 {
			break
		}
		count += encrypted
	}
	return count, nil
}
