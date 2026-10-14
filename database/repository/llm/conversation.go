package llm

import (
	"context"
	"database/sql"
	"time"

	"github.com/thrasher-corp/gocryptotrader/database"
	modelPSQL "github.com/thrasher-corp/gocryptotrader/database/models/postgres"
	modelSQLite "github.com/thrasher-corp/gocryptotrader/database/models/sqlite3"
	"github.com/thrasher-corp/sqlboiler/boil"
	"github.com/thrasher-corp/sqlboiler/queries/qm"
)

// Conversation mirrors llm_conversations.
type Conversation struct {
	ID           string
	Title        string
	Kind         string
	StrategyID   *string
	MarketScope  *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
	MessageCount int64
}

// InsertConversation inserts a new conversation.
func InsertConversation(ctx context.Context, c *Conversation) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMConversation{
			ID:           c.ID,
			Title:        c.Title,
			Kind:         c.Kind,
			StrategyID:   nullString(c.StrategyID),
			MarketScope:  nullString(c.MarketScope),
			CreatedAt:    tsToText(c.CreatedAt),
			UpdatedAt:    tsToText(c.UpdatedAt),
			ArchivedAt:   nullString(timeToTextPtr(c.ArchivedAt)),
			MessageCount: c.MessageCount,
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMConversation{
		ID:           c.ID,
		Title:        c.Title,
		Kind:         c.Kind,
		StrategyID:   nullString(c.StrategyID),
		MarketScope:  nullString(c.MarketScope),
		CreatedAt:    c.CreatedAt.UTC(),
		UpdatedAt:    c.UpdatedAt.UTC(),
		ArchivedAt:   nullTime(c.ArchivedAt),
		MessageCount: int(c.MessageCount),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func conversationFromSQLite(row *modelSQLite.LLMConversation) (*Conversation, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	updatedAt, err := tsFromText(row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	out := &Conversation{
		ID:           row.ID,
		Title:        row.Title,
		Kind:         row.Kind,
		StrategyID:   stringPtr(row.StrategyID),
		MarketScope:  stringPtr(row.MarketScope),
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		MessageCount: row.MessageCount,
	}
	if row.ArchivedAt.Valid {
		t, err := tsFromText(row.ArchivedAt.String)
		if err != nil {
			return nil, err
		}
		out.ArchivedAt = &t
	}
	return out, nil
}

// GetConversation returns a conversation by id.
func GetConversation(ctx context.Context, id string) (*Conversation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMConversations(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return conversationFromSQLite(row)
	}
	row, err := modelPSQL.LLMConversations(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Conversation{
		ID:           row.ID,
		Title:        row.Title,
		Kind:         row.Kind,
		StrategyID:   stringPtr(row.StrategyID),
		MarketScope:  stringPtr(row.MarketScope),
		CreatedAt:    row.CreatedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
		ArchivedAt:   timePtr(row.ArchivedAt),
		MessageCount: int64(row.MessageCount),
	}, nil
}

// ListConversations returns conversations newest first, optionally including
// archived ones.
func ListConversations(ctx context.Context, includeArchived bool) ([]Conversation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.OrderBy("updated_at DESC"), qm.OrderBy("id")}
	if !includeArchived {
		queries = append(queries, qm.Where("archived_at IS NULL"))
	}
	out := make([]Conversation, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMConversations(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			c, err := conversationFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *c)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMConversations(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, Conversation{
			ID:           row.ID,
			Title:        row.Title,
			Kind:         row.Kind,
			StrategyID:   stringPtr(row.StrategyID),
			MarketScope:  stringPtr(row.MarketScope),
			CreatedAt:    row.CreatedAt.UTC(),
			UpdatedAt:    row.UpdatedAt.UTC(),
			ArchivedAt:   timePtr(row.ArchivedAt),
			MessageCount: int64(row.MessageCount),
		})
	}
	return out, nil
}

// ArchiveConversation marks a conversation archived at the given time.
func ArchiveConversation(ctx context.Context, id string, at time.Time) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		_, err := modelSQLite.LLMConversations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelSQLite.M{colArchivedAt: tsToText(at)})
		return err
	}
	_, err := modelPSQL.LLMConversations(qm.Where("id = ?", id)).UpdateAll(ctx, database.DB.SQL, modelPSQL.M{colArchivedAt: at.UTC()})
	return err
}

// ChatMessage mirrors llm_chat_messages (append-only).
type ChatMessage struct {
	ID                string
	ConversationID    string
	Seq               int64
	Role              string
	ContentCipher     []byte
	ContentNonce      []byte
	ContentSha256     string
	CharCount         int64
	LLMProfileRevID   *string
	LLMModel          *string
	PairEpoch         *int64
	PromptVersion     *string
	UsageInputTokens  *int64
	UsageOutputTokens *int64
	LatencyMS         *int64
	ToolTranscript    *string
	ErrorCode         *string
	CreatedAt         time.Time
}

// AppendChatMessage appends a message to a conversation and maintains the
// conversation's message_count/updated_at. The sequence number is assigned
// here (max+1 within the transaction) so concurrent appends cannot collide on
// an application-computed number.
func AppendChatMessage(ctx context.Context, m *ChatMessage) (int64, error) {
	if err := available(); err != nil {
		return 0, err
	}
	var seq int64
	err := runInTx(ctx, func(tx *sql.Tx) error {
		if dialectIsSQLite() {
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) + 1 FROM llm_chat_messages WHERE conversation_id = ?", m.ConversationID).Scan(&seq); err != nil {
				return err
			}
			pairEpoch := nullInt(m.PairEpoch)
			row := &modelSQLite.LLMChatMessage{
				ID:                m.ID,
				ConversationID:    m.ConversationID,
				Seq:               seq,
				Role:              m.Role,
				ContentCipher:     nullBytes(m.ContentCipher),
				ContentNonce:      nullBytes(m.ContentNonce),
				ContentSha256:     m.ContentSha256,
				CharCount:         m.CharCount,
				LLMProfileRevID:   nullString(m.LLMProfileRevID),
				LLMModel:          nullString(m.LLMModel),
				PairEpoch:         pairEpoch,
				PromptVersion:     nullString(m.PromptVersion),
				UsageInputTokens:  nullInt(m.UsageInputTokens),
				UsageOutputTokens: nullInt(m.UsageOutputTokens),
				LatencyMS:         nullInt(m.LatencyMS),
				ToolTranscript:    nullString(m.ToolTranscript),
				ErrorCode:         nullString(m.ErrorCode),
				CreatedAt:         tsToText(m.CreatedAt),
			}
			if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE llm_conversations SET message_count = message_count + 1, updated_at = ? WHERE id = ?", tsToText(m.CreatedAt), m.ConversationID)
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) + 1 FROM llm_chat_messages WHERE conversation_id = $1", m.ConversationID).Scan(&seq); err != nil {
			return err
		}
		row := &modelPSQL.LLMChatMessage{
			ID:                m.ID,
			ConversationID:    m.ConversationID,
			Seq:               int(seq),
			Role:              m.Role,
			ContentCipher:     nullBytes(m.ContentCipher),
			ContentNonce:      nullBytes(m.ContentNonce),
			ContentSha256:     m.ContentSha256,
			CharCount:         int(m.CharCount),
			LLMProfileRevID:   nullString(m.LLMProfileRevID),
			LLMModel:          nullString(m.LLMModel),
			PairEpoch:         pgInt(m.PairEpoch),
			PromptVersion:     nullString(m.PromptVersion),
			UsageInputTokens:  pgInt(m.UsageInputTokens),
			UsageOutputTokens: pgInt(m.UsageOutputTokens),
			LatencyMS:         pgInt(m.LatencyMS),
			ToolTranscript:    nullString(m.ToolTranscript),
			ErrorCode:         nullString(m.ErrorCode),
			CreatedAt:         m.CreatedAt.UTC(),
		}
		if err := row.Insert(ctx, tx, boil.Infer()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE llm_conversations SET message_count = message_count + 1, updated_at = $1 WHERE id = $2", m.CreatedAt.UTC(), m.ConversationID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// ListChatMessages returns messages of a conversation with seq greater than
// afterSeq, oldest first, capped by limit (0 = no cap).
func ListChatMessages(ctx context.Context, conversationID string, afterSeq int64, limit int) ([]ChatMessage, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{
		qm.Where("conversation_id = ? AND seq > ?", conversationID, afterSeq),
		qm.OrderBy("seq"),
	}
	if limit > 0 {
		queries = append(queries, qm.Limit(limit))
	}
	out := make([]ChatMessage, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMChatMessages(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			createdAt, err := tsFromText(row.CreatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, ChatMessage{
				ID:                row.ID,
				ConversationID:    row.ConversationID,
				Seq:               row.Seq,
				Role:              row.Role,
				ContentCipher:     bytesPtr(row.ContentCipher),
				ContentNonce:      bytesPtr(row.ContentNonce),
				ContentSha256:     row.ContentSha256,
				CharCount:         row.CharCount,
				LLMProfileRevID:   stringPtr(row.LLMProfileRevID),
				LLMModel:          stringPtr(row.LLMModel),
				PairEpoch:         intPtr(row.PairEpoch),
				PromptVersion:     stringPtr(row.PromptVersion),
				UsageInputTokens:  intPtr(row.UsageInputTokens),
				UsageOutputTokens: intPtr(row.UsageOutputTokens),
				LatencyMS:         intPtr(row.LatencyMS),
				ToolTranscript:    stringPtr(row.ToolTranscript),
				ErrorCode:         stringPtr(row.ErrorCode),
				CreatedAt:         createdAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMChatMessages(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, ChatMessage{
			ID:                row.ID,
			ConversationID:    row.ConversationID,
			Seq:               int64(row.Seq),
			Role:              row.Role,
			ContentCipher:     bytesPtr(row.ContentCipher),
			ContentNonce:      bytesPtr(row.ContentNonce),
			ContentSha256:     row.ContentSha256,
			CharCount:         int64(row.CharCount),
			LLMProfileRevID:   stringPtr(row.LLMProfileRevID),
			LLMModel:          stringPtr(row.LLMModel),
			PairEpoch:         pgIntPtr(row.PairEpoch),
			PromptVersion:     stringPtr(row.PromptVersion),
			UsageInputTokens:  pgIntPtr(row.UsageInputTokens),
			UsageOutputTokens: pgIntPtr(row.UsageOutputTokens),
			LatencyMS:         pgIntPtr(row.LatencyMS),
			ToolTranscript:    stringPtr(row.ToolTranscript),
			ErrorCode:         stringPtr(row.ErrorCode),
			CreatedAt:         row.CreatedAt.UTC(),
		})
	}
	return out, nil
}
