package configstore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// The startup backfill runs on every booting node against one shared database.
// These tests cover what that concurrency requires of it: nodes must claim
// disjoint rows instead of waiting on each other, and must never deadlock.

// newEncryptionPodStore opens an independent pool against the schema that
// setupPostgresDeadlockStore already migrated, standing in for another node.
func newEncryptionPodStore(t *testing.T) *RDBConfigStore {
	t.Helper()
	db, err := gorm.Open(postgres.Open(postgresDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	store := &RDBConfigStore{logger: bifrost.NewDefaultLogger(schemas.LogLevelWarn)}
	store.db.Store(db)
	store.migrateOnFreshFn = func(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
		return fn(ctx, store.DB())
	}
	store.refreshPoolFn = func(ctx context.Context) error { return nil }
	return store
}

// TestEncryptPlaintextOAuthConfigs_SkipsRowsLockedByAnotherPod holds every row
// under an open transaction and asserts a second node walks past them instead
// of queueing behind the lock. Without SKIP LOCKED the write blocks until the
// holder commits, which at boot means one slow node stalls all the others.
func TestEncryptPlaintextOAuthConfigs_SkipsRowsLockedByAnotherPod(t *testing.T) {
	store := setupPostgresDeadlockStore(t)
	seedPlaintextOAuthConfigs(t, store.DB(), 20, "secret-")

	holder := newEncryptionPodStore(t)
	tx := holder.DB().Begin()
	require.NoError(t, tx.Error)
	defer func() { _ = tx.Rollback() }()

	var held []tables.TableOauthConfig
	require.NoError(t, tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&held).Error)
	require.Len(t, held, 20, "the holder must own every row for this to prove anything")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	other := newEncryptionPodStore(t)
	done := make(chan error, 1)
	go func() {
		_, err := other.encryptPlaintextOAuthConfigs(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "a node must not block on rows another node already claimed")
	case <-time.After(20 * time.Second):
		t.Fatal("encryptPlaintextOAuthConfigs blocked on rows locked by another node")
	}
}

// TestEncryptPlaintextOAuthConfigs_ConcurrentPodsDoNotDeadlock reproduces the
// reported startup crash: several nodes booting at once against the same table.
// Unordered batches let two nodes take the same rows in opposite orders, which
// Postgres resolves by killing one with SQLSTATE 40P01 — a fatal error at boot.
func TestEncryptPlaintextOAuthConfigs_ConcurrentPodsDoNotDeadlock(t *testing.T) {
	store := setupPostgresDeadlockStore(t)

	const (
		pods  = 5
		total = encryptionBatchSize * 4
	)
	seedPlaintextOAuthConfigs(t, store.DB(), total, "secret-")

	stores := make([]*RDBConfigStore, pods)
	for i := range stores {
		stores[i] = newEncryptionPodStore(t)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := make(chan struct{})
	counts := make(chan int, pods)
	errs := make(chan error, pods)
	var wg sync.WaitGroup
	for _, s := range stores {
		wg.Add(1)
		go func(s *RDBConfigStore) {
			defer wg.Done()
			<-start
			count, err := s.encryptPlaintextOAuthConfigs(ctx)
			counts <- count
			errs <- err
		}(s)
	}
	close(start)
	wg.Wait()
	close(counts)
	close(errs)

	for err := range errs {
		if isPostgresDeadlock(err) {
			t.Fatalf("startup encryption deadlocked across nodes: %v", err)
		}
		require.NoError(t, err)
	}

	// Every row is claimed by exactly one node, so the per-node counts partition
	// the table rather than overlapping.
	claimed := 0
	for c := range counts {
		claimed += c
	}
	assert.Equal(t, total, claimed, "each row should be encrypted by exactly one node")

	var remaining int64
	require.NoError(t, store.DB().Table("oauth_configs").
		Where("encryption_status = ? OR encryption_status IS NULL OR encryption_status = ''", encryptionStatusPlainText).
		Count(&remaining).Error)
	assert.Zero(t, remaining)

	var found tables.TableOauthConfig
	require.NoError(t, store.DB().Where("id = ?", oauthConfigID(total-1)).First(&found).Error)
	assert.Equal(t, fmt.Sprintf("secret-%d", total-1), found.ClientSecret.GetValue())
}
