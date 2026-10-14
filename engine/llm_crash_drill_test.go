package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
)

// M6-06 崩溃恢复演练（docs 11 §8）：真实的第二个进程在认领（SUBMITTING）之后、
// 结果落地之前被 kill -9；重启扫描必须把该意图落成 UNKNOWN + 需对账，
// 并且 UNKNOWN 不可被再次认领（不盲重试）。所有读写都走真实仓储与真实 SQLite。

const (
	drillDirEnv    = "GCT_M6_DRILL_DIR"
	drillDBEnv     = "GCT_M6_DRILL_DB"
	drillIntentEnv = "GCT_M6_DRILL_INTENT"
)

// TestM6CrashDrillChild is the victim process: it connects to the drill
// database, performs the same atomic claim the dispatcher uses, announces it,
// and then blocks until the parent kills it mid-flight.
func TestM6CrashDrillChild(t *testing.T) {
	if os.Getenv(drillDirEnv) == "" {
		t.Skip("child-only test: this process is not a crash drill victim")
	}
	testhelpers.TempDir = os.Getenv(drillDirEnv)
	testhelpers.MigrationDir = filepath.Join("..", "database", "migrations")
	conn, err := testhelpers.ConnectToDatabase(&database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: os.Getenv(drillDBEnv),
	})
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, testhelpers.CloseDatabase(conn))
	}()

	claimed, err := llmstore.ClaimOrderIntent(context.Background(), os.Getenv(drillIntentEnv), time.Now().UTC())
	require.NoError(t, err)
	require.True(t, claimed, "the drill intent must still be PREPARED")
	_, err = os.Stdout.WriteString("CLAIMED\n")
	require.NoError(t, err)
	for {
		time.Sleep(time.Hour)
	}
}

func drillIntent(id, status string, expiresAt, now time.Time) *llmstore.OrderIntent {
	return &llmstore.OrderIntent{
		ID:                id,
		DecisionRunID:     "run-drill",
		RiskEvaluationID:  "risk-drill",
		StrategyVersionID: "ver-drill",
		ApprovalID:        "appr-drill",
		Exchange:          "Binance",
		Pair:              "BTC_USDT",
		Asset:             "usdtmarginedfutures",
		Action:            "OPEN_LONG",
		Side:              "BUY",
		OrderType:         "LIMIT",
		Quantity:          "0.001",
		ClientOrderID:     "drill-" + id,
		NotionalUSD:       "100",
		EnvelopeHash:      "env-drill",
		PairEpoch:         3,
		Status:            status,
		ExpiresAt:         expiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// TestM6CrashDrillKill9MidSubmitting is the drill proper.
func TestM6CrashDrillKill9MidSubmitting(t *testing.T) {
	if os.Getenv(drillDirEnv) != "" {
		t.Skip("this process is the drill child")
	}
	setupProviderAdminDB(t)

	ctx := t.Context()
	now := time.Now().UTC()
	dbName := fmt.Sprintf("m6-crash-drill-%d.db", time.Now().UnixNano())
	conn, err := testhelpers.ConnectToDatabase(&database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: dbName,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, testhelpers.CloseDatabase(conn))
	})

	const (
		claimedID = "drill-claimed"
		staleID   = "drill-stale-prepared"
	)
	require.NoError(t, llmstore.InsertOrderIntent(ctx, drillIntent(claimedID, IntentStatusPrepared, now.Add(time.Minute), now)))
	require.NoError(t, llmstore.InsertOrderIntent(ctx, drillIntent(staleID, IntentStatusPrepared, now.Add(-time.Minute), now)))

	// The victim: a second real process sharing the same SQLite file.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestM6CrashDrillChild", "-test.v") //nolint:gosec // re-exec of this test binary is the drill itself
	cmd.Env = append(os.Environ(),
		drillDirEnv+"="+testhelpers.TempDir,
		drillDBEnv+"="+dbName,
		drillIntentEnv+"="+claimedID,
	)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	claimedSeen := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "CLAIMED") {
			claimedSeen = true
			break
		}
	}
	require.True(t, claimedSeen, "the victim must claim before it dies")

	// Confirm the claim is on disk from an independent reader, then kill -9.
	submitting, err := llmstore.ListOrderIntentsByStatus(ctx, IntentStatusSubmitting)
	require.NoError(t, err)
	require.Len(t, submitting, 1, "exactly the drilled intent is in flight")
	require.Equal(t, claimedID, submitting[0].ID)

	require.NoError(t, cmd.Process.Kill(), "SIGKILL mid-submission")
	_ = cmd.Wait()

	// A hard death leaves the row in SUBMITTING: the claim had no outcome.
	require.NoError(t, testhelpers.CloseDatabase(conn))
	conn, err = testhelpers.ConnectToDatabase(&database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: dbName,
	})
	require.NoError(t, err)
	stillSubmitting, err := llmstore.ListOrderIntentsByStatus(ctx, IntentStatusSubmitting)
	require.NoError(t, err)
	require.Len(t, stillSubmitting, 1)
	require.Equal(t, claimedID, stillSubmitting[0].ID)

	// Restart scan: real repository seams, one pass.
	recovery := &CrashRecovery{
		Intents:   LLMIntentStatusLister{},
		Lifecycle: LLMIntentLifecycle{},
		Audit:     LLMDecisionStore{},
		Metrics:   NewLLMMetrics(),
		Now:       func() time.Time { return now.Add(time.Minute) },
	}
	result, err := recovery.Recover(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{claimedID}, result.UnknownIntents, "the claim without an outcome becomes UNKNOWN")
	assert.Equal(t, []string{staleID}, result.ExpiredIntents, "the stale PREPARED expires alongside")
	assert.True(t, result.ReconciliationRequired)

	unknown, err := llmstore.ListOrderIntentsByStatus(ctx, IntentStatusUnknown)
	require.NoError(t, err)
	require.Len(t, unknown, 1)
	assert.Equal(t, claimedID, unknown[0].ID)

	// 不盲重试：只有 PREPARED 可被认领，UNKNOWN 永远拒绝。
	reclaimed, err := llmstore.ClaimOrderIntent(ctx, claimedID, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.False(t, reclaimed, "UNKNOWN should never be claimed again")

	// 对账开关被拉起：UNKNOWN 计数上表。
	assert.Equal(t, 1.0, recovery.Metrics.Snapshot().Gauges[MetricUnknownOrders])

	// 审计留痕：恰好一条 CRASH_RECOVERY，详情点名两个意图。
	events, err := llmstore.ListAuditEvents(ctx, AuditKindCrashRecovery, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, events[0].DetailJSON, claimedID)
	assert.Contains(t, events[0].DetailJSON, staleID)
}
