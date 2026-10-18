package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// M6-08 备份恢复演练（docs 11 §8）：一台“机器”的全部持久化状态就是三样
// 文件——chat 键目录（会话加密钥匙）、SQLite 数据库文件、配置文件。演练按
// 文件级备份走两跳（A → 备份介质 → B），然后在一个**全新的进程**里以机器 B
// 的目录重开：库行可读、provider 明文 key 可回读、会话密文可开、用量读面
// 可汇总、配置可加载迁移。两个负例同时钉死：缺 conversation-key.bin =
// 旧会话永久不可读（备份完整性不是可选项）；库按设计含**明文 provider key**（2026-10-15
// vault 移除——如实钉死新安全态势），但**会话正文必须保持密文**。

const (
	drillRestoreDirEnv = "GCT_M6_RESTORE_DIR"
	drillRestoreDBEnv  = "GCT_M6_RESTORE_DB"
	drillRestoreOK     = "RESTORE_OK"
)

// 演练夹具：父子两个测试共享同一进程镜像里的这些常量，不靠环境传值。
const (
	drillLLMPlaintext  = "sk-llm-backup-111"
	drillJevPlaintext  = "jev-token-backup-222"
	drillConversation  = "conv-m6-backup"
	drillUserMessage   = "备份恢复演练：这条消息必须能在另一台机器上打开。"
	drillReplyMessage  = "restored reply byte-for-byte"
	drillIntentID      = "drill-backup-intent"
	drillAuditKind     = "DRILL_BACKUP"
	drillReservationID = "res-drill-backup"
)

// TestM6BackupRestoreChild is the "machine B" half of the drill: it runs in a
// fresh process with its data root pointed at the restored directory, and it
// re-derives every handle from the restored files only (new chat key dir, new
// database connection, cipher rebuilt from the restored key file).
func TestM6BackupRestoreChild(t *testing.T) {
	restoreDir := os.Getenv(drillRestoreDirEnv)
	if restoreDir == "" {
		t.Skip("child-only test: this process is not a restore drill machine B")
	}
	testhelpers.TempDir = restoreDir
	testhelpers.MigrationDir = filepath.Join("..", "database", "migrations")
	ctx := t.Context()

	// Database: connecting into the restored root runs the migrations, which
	// must be a clean no-op on a database that was current when it was backed up.
	conn, err := testhelpers.ConnectToDatabase(&database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: os.Getenv(drillRestoreDBEnv),
	})
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, testhelpers.CloseDatabase(conn))
	}()

	// Provider keys: stored plaintext since the vault removal (2026-10-15);
	// the restored rows carry them back verbatim and fingerprints derive.
	pair, err := llmstore.GetActiveModelPair(ctx)
	require.NoError(t, err)
	assert.Equal(t, LLMPairReady, pair.Readiness)
	require.NotNil(t, pair.LLMProfileRevisionID)
	require.NotNil(t, pair.JevProfileRevisionID)

	llmRevision, err := llmstore.GetProfileRevision(ctx, *pair.LLMProfileRevisionID)
	require.NoError(t, err)
	assert.Equal(t, drillLLMPlaintext, llmRevision.APIKeyRef, "the restored row carries the key back")
	assert.Equal(t, deriveKeyFingerprint(drillLLMPlaintext), llmRevision.APIKeyFingerprint)

	jevRevision, err := llmstore.GetProfileRevision(ctx, *pair.JevProfileRevisionID)
	require.NoError(t, err)
	assert.Equal(t, drillJevPlaintext, jevRevision.APIKeyRef, "the restored Jev token comes back the same way")

	// Encrypted conversation: the cipher is rebuilt from the restored
	// conversation-key.bin; every body must open byte for byte.
	messages, err := llmstore.ListChatMessages(ctx, drillConversation, 0, 0)
	require.NoError(t, err)
	require.Len(t, messages, 2)
	key, err := chat.LoadOrCreateConversationKey(filepath.Join(restoreDir, "llm-chat-keys"))
	require.NoError(t, err)
	cipher, err := chat.NewConversationCipher(key)
	require.NoError(t, err)
	for i, want := range []string{drillUserMessage, drillReplyMessage} {
		opened, err := cipher.Open(&chat.Message{
			ID: messages[i].ID, Ciphertext: messages[i].ContentCipher,
			Nonce: messages[i].ContentNonce, Sha256: messages[i].ContentSha256,
		})
		require.NoErrorf(t, err, "restored message %d must open", i)
		assert.Equal(t, want, string(opened))
	}

	// Execution and audit rows survive the trip.
	intents, err := llmstore.ListOrderIntentsByStatus(ctx, IntentStatusPrepared)
	require.NoError(t, err)
	require.Len(t, intents, 1)
	assert.Equal(t, drillIntentID, intents[0].ID)
	events, err := llmstore.ListAuditEvents(ctx, drillAuditKind, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, events[0].DetailJSON, "backup drill row")

	// The usage read face (W7-03) aggregates the settled reservation from the
	// restored database: token accounting survives the machine move.
	server := &RPCServer{Engine: &Engine{llmManager: &LLMManager{}}}
	usage, err := server.LLMGetUsage(ctx, &gctrpc.LLMGetUsageRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(1234), usage.GetTotalInputTokens(), "settled tokens should be visible after restore")
	assert.Equal(t, int64(567), usage.GetTotalOutputTokens())
	require.Len(t, usage.GetLines(), 1)
	assert.Equal(t, int64(890), usage.GetLines()[0].GetAvgLatencyMs())

	// Configuration: the current-version file loads as-is; the older file
	// migrates to the current version on the restored machine.
	var fresh config.Config
	require.NoError(t, fresh.ReadConfigFromFile(filepath.Join(restoreDir, "config.json"), true))
	assert.Equal(t, 19, fresh.Version)
	assert.True(t, fresh.LLM.Enabled)
	assert.Equal(t, "SHADOW", fresh.LLM.RunMode)
	assert.Equal(t, "acct-m6-08", fresh.LLM.AccountID)
	assert.Equal(t, 9, fresh.LLM.Halt.ReviewExpireHours)
	fresh.CheckLLMConfig()
	assert.Equal(t, 9, fresh.LLM.Halt.ReviewExpireHours, "the check should not overwrite an explicit value")

	var old config.Config
	require.NoError(t, old.ReadConfigFromFile(filepath.Join(restoreDir, "config-v17.json"), true))
	assert.Equal(t, 19, old.Version, "the restored older config should migrate to the current version")
	assert.False(t, old.LLM.Enabled, "an absent llm section lands disabled")
	old.CheckLLMConfig()
	assert.Equal(t, "READ_ONLY", old.LLM.RunMode)

	// Negative: a chat-key copy that dropped conversation-key.bin re-mints a
	// fresh key, and the old ciphertexts can never be opened again.
	incompleteDir := filepath.Join(t.TempDir(), "llm-chat-keys")
	copyTree(t, filepath.Join(restoreDir, "llm-chat-keys"), incompleteDir)
	require.NoError(t, os.Remove(filepath.Join(incompleteDir, chat.ConversationKeyFile)))
	newKey, err := chat.LoadOrCreateConversationKey(incompleteDir)
	require.NoError(t, err)
	assert.NotEqual(t, key, newKey, "a missing conversation key is re-minted, not recovered")
	orphanCipher, err := chat.NewConversationCipher(newKey)
	require.NoError(t, err)
	_, err = orphanCipher.Open(&chat.Message{
		ID: messages[0].ID, Ciphertext: messages[0].ContentCipher,
		Nonce: messages[0].ContentNonce, Sha256: messages[0].ContentSha256,
	})
	assert.ErrorIs(t, err, chat.ErrDecrypt, "without the backed-up key file the old messages are lost")

	// The restored database carries provider keys as plaintext by design
	// (operator simplification, 2026-10-15); chat bodies, however, must stay
	// encrypted: the conversation text must NOT appear in the clear.
	raw, err := os.ReadFile(filepath.Join(restoreDir, os.Getenv(drillRestoreDBEnv)))
	require.NoError(t, err)
	assert.Contains(t, string(raw), drillLLMPlaintext, "provider keys are stored plaintext by design")
	assert.NotContainsf(t, string(raw), drillUserMessage, "chat bodies should stay encrypted at rest")

	_, err = os.Stdout.WriteString(drillRestoreOK + "\n")
	require.NoError(t, err)
}

// TestM6BackupRestoreDrill is the drill proper: it builds machine A, backs the
// three persistent artifacts up through a backup medium to a fresh directory
// tree, and hands machine B to the child process above.
func TestM6BackupRestoreDrill(t *testing.T) {
	if os.Getenv(drillRestoreDirEnv) != "" {
		t.Skip("this process is the restore drill child")
	}
	setupProviderAdminDB(t)
	ctx := t.Context()

	// Machine A: the chat key directory and the two provider revisions (keys
	// are plaintext by design since the vault removal).
	machineA := t.TempDir()
	chatDirA := filepath.Join(machineA, "llm-chat-keys")
	conversationKey, err := chat.LoadOrCreateConversationKey(chatDirA)
	require.NoError(t, err)
	cipher, err := chat.NewConversationCipher(conversationKey)
	require.NoError(t, err)

	// Machine A: database populated through production paths.
	dbName := fmt.Sprintf("m6-backup-drill-%d.db", time.Now().UnixNano())
	conn, err := testhelpers.ConnectToDatabase(&database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: dbName,
	})
	require.NoError(t, err)
	llmProfile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Backup LLM", "op-drill", providerAdminNow)
	require.NoError(t, err)
	jevProfile, err := CreateProviderProfile(ctx, ProviderKindJev, "Backup Jev", "op-drill", providerAdminNow)
	require.NoError(t, err)
	maxTokens := int64(4096)
	promptVersion := openai.PromptVersionAnalysisReadOnly
	budget := "5.00"
	llmRevision, err := AddProviderRevision(ctx, &ProviderRevisionParams{
		ProfileID: llmProfile.ID, Actor: "op-drill", BaseURL: "https://api.example.com/v1",
		ModelID: "model-backup", APIKey: drillLLMPlaintext,
		TimeoutSeconds: 30, MaxOutputTokens: &maxTokens, PromptVersion: &promptVersion,
		DailyBudgetUSD: &budget, ReservationCap24H: 3,
	}, providerAdminNow)
	require.NoError(t, err)
	questionTemplate := "systemone-v1"
	jevRevision, err := AddProviderRevision(ctx, &ProviderRevisionParams{
		ProfileID: jevProfile.ID, Actor: "op-drill", BaseURL: "https://jev.example.com/v1",
		ModelID: "jev-1.0.0", APIKey: drillJevPlaintext,
		TimeoutSeconds: 60, QuestionTemplateVersion: &questionTemplate, ReservationCap24H: 5,
	}, providerAdminNow)
	require.NoError(t, err)
	require.NoError(t, llmstore.SetActiveModelPair(ctx, &llmstore.ActiveModelPair{
		LLMProfileRevisionID: &llmRevision.ID, JevProfileRevisionID: &jevRevision.ID,
		PairEpoch: 1, Readiness: LLMPairReady, UpdatedAt: providerAdminNow, UpdatedBy: "op-drill",
	}))

	now := time.Now().UTC()
	require.NoError(t, llmstore.InsertOrderIntent(ctx, drillIntent(drillIntentID, IntentStatusPrepared, now.Add(time.Minute), now)))
	require.NoError(t, llmstore.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: "audit-drill-backup", Kind: drillAuditKind, Actor: "op-drill", Subject: new("m6-08"),
		DetailJSON: `{"note":"backup drill row"}`, CreatedAt: now,
	}))
	require.NoError(t, llmstore.InsertBudgetReservation(ctx, &llmstore.BudgetReservation{
		ID: drillReservationID, ProfileRevisionID: llmRevision.ID, Kind: ProviderKindLLM, Purpose: "CHAT",
		Status: reservationStatusReserved, ReservedAt: now, ExpiresAt: now.Add(time.Minute),
	}))
	in, out, latency := int64(1234), int64(567), int64(890)
	require.NoError(t, llmstore.SettleBudgetReservation(ctx, drillReservationID, reservationStatusUsed, now.Add(time.Second), &in, &out, &latency))

	require.NoError(t, llmstore.InsertConversation(ctx, &llmstore.Conversation{
		ID: drillConversation, Title: "M6-08 backup restore", Kind: ConversationKindChat,
		CreatedAt: providerAdminNow, UpdatedAt: providerAdminNow, MessageCount: 0,
	}))
	userSealed, err := cipher.Seal("msg-drill-1", []byte(drillUserMessage))
	require.NoError(t, err)
	_, err = llmstore.AppendChatMessage(ctx, &llmstore.ChatMessage{
		ID: userSealed.ID, ConversationID: drillConversation, Role: "user",
		ContentCipher: userSealed.Ciphertext, ContentNonce: userSealed.Nonce, ContentSha256: userSealed.Sha256,
		CharCount: int64(len([]rune(drillUserMessage))), CreatedAt: providerAdminNow.Add(time.Minute),
	})
	require.NoError(t, err)
	replySealed, err := cipher.Seal("msg-drill-2", []byte(drillReplyMessage))
	require.NoError(t, err)
	_, err = llmstore.AppendChatMessage(ctx, &llmstore.ChatMessage{
		ID: replySealed.ID, ConversationID: drillConversation, Role: "assistant",
		ContentCipher: replySealed.Ciphertext, ContentNonce: replySealed.Nonce, ContentSha256: replySealed.Sha256,
		CharCount: int64(len([]rune(drillReplyMessage))), CreatedAt: providerAdminNow.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	// Machine A: configuration files (current version with an explicit LLM
	// section, and an older one that must migrate on the other side).
	configDirA := filepath.Join(machineA, "config")
	require.NoError(t, os.MkdirAll(configDirA, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDirA, "config.json"), []byte(
		`{"version":18,"name":"drill-machine-a","llm":{"enabled":true,"runMode":"SHADOW","accountId":"acct-m6-08","halt":{"reviewExpireHours":9}}}`,
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(configDirA, "config-v17.json"), []byte(
		`{"version":17,"name":"drill-machine-a-old"}`,
	), 0o600))

	// Hop 1: machine A -> backup medium (file level; the database is closed
	// first so its journal is checkpointed into the one file that is copied).
	medium := t.TempDir()
	require.NoError(t, testhelpers.CloseDatabase(conn))
	copyTree(t, chatDirA, filepath.Join(medium, "llm-chat-keys"))
	copyTree(t, configDirA, medium)
	copyTree(t, filepath.Join(testhelpers.TempDir, dbName), filepath.Join(medium, dbName))
	assert.FileExists(t, filepath.Join(medium, "llm-chat-keys", chat.ConversationKeyFile),
		"the conversation key file is part of the backup set")

	// Hop 2: backup medium -> machine B (a fresh root, nothing shared with A).
	machineB := t.TempDir()
	copyTree(t, medium, machineB)

	// Machine B is verified in a fresh process with only the restored files.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestM6BackupRestoreChild") //nolint:gosec // re-exec of this test binary is the drill itself
	cmd.Env = append(os.Environ(),
		drillRestoreDirEnv+"="+machineB,
		drillRestoreDBEnv+"="+dbName,
	)
	childOut, err := cmd.Output()
	require.NoErrorf(t, err, "the machine B verification process must pass:\n%s", childOut)
	assert.Contains(t, string(childOut), drillRestoreOK, "the child should report a completed restore")
}

// copyTree copies a file tree preserving the vault-style modes (0700 dirs,
// 0600 files). It backs both the backup hop and the restore hop. A file src
// copies as a single file. The recursion is hand-rolled rather than a walk
// callback so every read happens on a path this function built itself.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	require.NoError(t, err, "stat of the copy source")
	if !info.IsDir() {
		body, err := os.ReadFile(src)
		require.NoError(t, err, "reading the copy source")
		require.NoError(t, os.WriteFile(dst, body, 0o600), "writing the copy target")
		return
	}
	require.NoErrorf(t, copyDirTree(src, dst), "copying %s to %s", src, dst)
}

// copyDirTree recursively copies one directory.
func copyDirTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		source := filepath.Join(src, entry.Name())
		target := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirTree(source, target); err != nil {
				return err
			}
			continue
		}
		body, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, body, 0o600); err != nil {
			return err
		}
	}
	return nil
}
