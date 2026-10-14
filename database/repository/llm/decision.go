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

// MarketSnapshot mirrors llm_market_snapshots (append-only).
type MarketSnapshot struct {
	ID               string
	Exchange         string
	Pair             string
	Asset            string
	SourceTime       time.Time
	BuiltAt          time.Time
	FreshnessMS      int64
	QualityJSON      string
	CandleWindowJSON string
	IndicatorJSON    string
	TickerJSON       string
	OrderbookJSON    string
	FundingJSON      string
	AccountJSON      string
	TotalBytes       int64
	ContentSha256    string
}

// InsertMarketSnapshot appends a market snapshot.
func InsertMarketSnapshot(ctx context.Context, s *MarketSnapshot) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMMarketSnapshot{
			ID:               s.ID,
			Exchange:         s.Exchange,
			Pair:             s.Pair,
			Asset:            s.Asset,
			SourceTime:       tsToText(s.SourceTime),
			BuiltAt:          tsToText(s.BuiltAt),
			FreshnessMS:      s.FreshnessMS,
			QualityJSON:      s.QualityJSON,
			CandleWindowJSON: s.CandleWindowJSON,
			IndicatorJSON:    s.IndicatorJSON,
			TickerJSON:       s.TickerJSON,
			OrderbookJSON:    s.OrderbookJSON,
			FundingJSON:      s.FundingJSON,
			AccountJSON:      s.AccountJSON,
			TotalBytes:       s.TotalBytes,
			ContentSha256:    s.ContentSha256,
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMMarketSnapshot{
		ID:               s.ID,
		Exchange:         s.Exchange,
		Pair:             s.Pair,
		Asset:            s.Asset,
		SourceTime:       s.SourceTime.UTC(),
		BuiltAt:          s.BuiltAt.UTC(),
		FreshnessMS:      int(s.FreshnessMS),
		QualityJSON:      s.QualityJSON,
		CandleWindowJSON: s.CandleWindowJSON,
		IndicatorJSON:    s.IndicatorJSON,
		TickerJSON:       s.TickerJSON,
		OrderbookJSON:    s.OrderbookJSON,
		FundingJSON:      s.FundingJSON,
		AccountJSON:      s.AccountJSON,
		TotalBytes:       int(s.TotalBytes),
		ContentSha256:    s.ContentSha256,
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func marketSnapshotFromSQLite(row *modelSQLite.LLMMarketSnapshot) (*MarketSnapshot, error) {
	sourceTime, err := tsFromText(row.SourceTime)
	if err != nil {
		return nil, err
	}
	builtAt, err := tsFromText(row.BuiltAt)
	if err != nil {
		return nil, err
	}
	return &MarketSnapshot{
		ID:               row.ID,
		Exchange:         row.Exchange,
		Pair:             row.Pair,
		Asset:            row.Asset,
		SourceTime:       sourceTime,
		BuiltAt:          builtAt,
		FreshnessMS:      row.FreshnessMS,
		QualityJSON:      row.QualityJSON,
		CandleWindowJSON: row.CandleWindowJSON,
		IndicatorJSON:    row.IndicatorJSON,
		TickerJSON:       row.TickerJSON,
		OrderbookJSON:    row.OrderbookJSON,
		FundingJSON:      row.FundingJSON,
		AccountJSON:      row.AccountJSON,
		TotalBytes:       row.TotalBytes,
		ContentSha256:    row.ContentSha256,
	}, nil
}

// GetMarketSnapshot returns a snapshot by id.
func GetMarketSnapshot(ctx context.Context, id string) (*MarketSnapshot, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMMarketSnapshots(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return marketSnapshotFromSQLite(row)
	}
	row, err := modelPSQL.LLMMarketSnapshots(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &MarketSnapshot{
		ID:               row.ID,
		Exchange:         row.Exchange,
		Pair:             row.Pair,
		Asset:            row.Asset,
		SourceTime:       row.SourceTime.UTC(),
		BuiltAt:          row.BuiltAt.UTC(),
		FreshnessMS:      int64(row.FreshnessMS),
		QualityJSON:      row.QualityJSON,
		CandleWindowJSON: row.CandleWindowJSON,
		IndicatorJSON:    row.IndicatorJSON,
		TickerJSON:       row.TickerJSON,
		OrderbookJSON:    row.OrderbookJSON,
		FundingJSON:      row.FundingJSON,
		AccountJSON:      row.AccountJSON,
		TotalBytes:       int64(row.TotalBytes),
		ContentSha256:    row.ContentSha256,
	}, nil
}

// DecisionRun mirrors llm_decision_runs (append-only).
type DecisionRun struct {
	ID                      string
	StrategyVersionID       *string
	SnapshotID              *string
	Trigger                 string
	RunMode                 string
	LLMProfileRevisionID    string
	JevProfileRevisionID    *string
	PairEpoch               int64
	LLMModel                string
	JevModel                *string
	PromptVersion           string
	QuestionTemplateVersion *string
	CandidateJSON           *string
	JevJSON                 *string
	JevManifestState        *string
	ToolTranscriptJSON      *string
	ToolCallCount           int64
	LLMRoundCount           int64
	UsageInputTokens        int64
	UsageOutputTokens       int64
	JevInputTokens          *int64
	JevOutputTokens         *int64
	LatencyMS               int64
	DeadlineAt              *time.Time
	Decision                string
	Reason                  string
	AnalysisSafety          *string
	AnalysisSafetyReason    *string
	InputHash               string
	CreatedAt               time.Time
}

// InsertDecisionRun appends a decision run.
func InsertDecisionRun(ctx context.Context, r *DecisionRun) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMDecisionRun{
			ID:                      r.ID,
			StrategyVersionID:       nullString(r.StrategyVersionID),
			SnapshotID:              nullString(r.SnapshotID),
			Trigger:                 r.Trigger,
			RunMode:                 r.RunMode,
			LLMProfileRevisionID:    r.LLMProfileRevisionID,
			JevProfileRevisionID:    nullString(r.JevProfileRevisionID),
			PairEpoch:               r.PairEpoch,
			LLMModel:                r.LLMModel,
			JevModel:                nullString(r.JevModel),
			PromptVersion:           r.PromptVersion,
			QuestionTemplateVersion: nullString(r.QuestionTemplateVersion),
			CandidateJSON:           nullString(r.CandidateJSON),
			JevJSON:                 nullString(r.JevJSON),
			JevManifestState:        nullString(r.JevManifestState),
			ToolTranscriptJSON:      nullString(r.ToolTranscriptJSON),
			ToolCallCount:           r.ToolCallCount,
			LLMRoundCount:           r.LLMRoundCount,
			UsageInputTokens:        r.UsageInputTokens,
			UsageOutputTokens:       r.UsageOutputTokens,
			JevInputTokens:          nullInt(r.JevInputTokens),
			JevOutputTokens:         nullInt(r.JevOutputTokens),
			LatencyMS:               r.LatencyMS,
			DeadlineAt:              nullString(timeToTextPtr(r.DeadlineAt)),
			Decision:                r.Decision,
			Reason:                  r.Reason,
			AnalysisSafety:          nullString(r.AnalysisSafety),
			AnalysisSafetyReason:    nullString(r.AnalysisSafetyReason),
			InputHash:               r.InputHash,
			CreatedAt:               tsToText(r.CreatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMDecisionRun{
		ID:                      r.ID,
		StrategyVersionID:       nullString(r.StrategyVersionID),
		SnapshotID:              nullString(r.SnapshotID),
		Trigger:                 r.Trigger,
		RunMode:                 r.RunMode,
		LLMProfileRevisionID:    r.LLMProfileRevisionID,
		JevProfileRevisionID:    nullString(r.JevProfileRevisionID),
		PairEpoch:               int(r.PairEpoch),
		LLMModel:                r.LLMModel,
		JevModel:                nullString(r.JevModel),
		PromptVersion:           r.PromptVersion,
		QuestionTemplateVersion: nullString(r.QuestionTemplateVersion),
		CandidateJSON:           nullString(r.CandidateJSON),
		JevJSON:                 nullString(r.JevJSON),
		JevManifestState:        nullString(r.JevManifestState),
		ToolTranscriptJSON:      nullString(r.ToolTranscriptJSON),
		ToolCallCount:           int(r.ToolCallCount),
		LLMRoundCount:           int(r.LLMRoundCount),
		UsageInputTokens:        int(r.UsageInputTokens),
		UsageOutputTokens:       int(r.UsageOutputTokens),
		JevInputTokens:          pgInt(r.JevInputTokens),
		JevOutputTokens:         pgInt(r.JevOutputTokens),
		LatencyMS:               int(r.LatencyMS),
		DeadlineAt:              nullTime(r.DeadlineAt),
		Decision:                r.Decision,
		Reason:                  r.Reason,
		AnalysisSafety:          nullString(r.AnalysisSafety),
		AnalysisSafetyReason:    nullString(r.AnalysisSafetyReason),
		InputHash:               r.InputHash,
		CreatedAt:               r.CreatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

func decisionRunFromSQLite(row *modelSQLite.LLMDecisionRun) (*DecisionRun, error) {
	createdAt, err := tsFromText(row.CreatedAt)
	if err != nil {
		return nil, err
	}
	out := &DecisionRun{
		ID:                      row.ID,
		StrategyVersionID:       stringPtr(row.StrategyVersionID),
		SnapshotID:              stringPtr(row.SnapshotID),
		Trigger:                 row.Trigger,
		RunMode:                 row.RunMode,
		LLMProfileRevisionID:    row.LLMProfileRevisionID,
		JevProfileRevisionID:    stringPtr(row.JevProfileRevisionID),
		PairEpoch:               row.PairEpoch,
		LLMModel:                row.LLMModel,
		JevModel:                stringPtr(row.JevModel),
		PromptVersion:           row.PromptVersion,
		QuestionTemplateVersion: stringPtr(row.QuestionTemplateVersion),
		CandidateJSON:           stringPtr(row.CandidateJSON),
		JevJSON:                 stringPtr(row.JevJSON),
		JevManifestState:        stringPtr(row.JevManifestState),
		ToolTranscriptJSON:      stringPtr(row.ToolTranscriptJSON),
		ToolCallCount:           row.ToolCallCount,
		LLMRoundCount:           row.LLMRoundCount,
		UsageInputTokens:        row.UsageInputTokens,
		UsageOutputTokens:       row.UsageOutputTokens,
		JevInputTokens:          intPtr(row.JevInputTokens),
		JevOutputTokens:         intPtr(row.JevOutputTokens),
		LatencyMS:               row.LatencyMS,
		Decision:                row.Decision,
		Reason:                  row.Reason,
		AnalysisSafety:          stringPtr(row.AnalysisSafety),
		AnalysisSafetyReason:    stringPtr(row.AnalysisSafetyReason),
		InputHash:               row.InputHash,
		CreatedAt:               createdAt,
	}
	if row.DeadlineAt.Valid {
		t, err := tsFromText(row.DeadlineAt.String)
		if err != nil {
			return nil, err
		}
		out.DeadlineAt = &t
	}
	return out, nil
}

func decisionRunFromPSQL(row *modelPSQL.LLMDecisionRun) *DecisionRun {
	return &DecisionRun{
		ID:                      row.ID,
		StrategyVersionID:       stringPtr(row.StrategyVersionID),
		SnapshotID:              stringPtr(row.SnapshotID),
		Trigger:                 row.Trigger,
		RunMode:                 row.RunMode,
		LLMProfileRevisionID:    row.LLMProfileRevisionID,
		JevProfileRevisionID:    stringPtr(row.JevProfileRevisionID),
		PairEpoch:               int64(row.PairEpoch),
		LLMModel:                row.LLMModel,
		JevModel:                stringPtr(row.JevModel),
		PromptVersion:           row.PromptVersion,
		QuestionTemplateVersion: stringPtr(row.QuestionTemplateVersion),
		CandidateJSON:           stringPtr(row.CandidateJSON),
		JevJSON:                 stringPtr(row.JevJSON),
		JevManifestState:        stringPtr(row.JevManifestState),
		ToolTranscriptJSON:      stringPtr(row.ToolTranscriptJSON),
		ToolCallCount:           int64(row.ToolCallCount),
		LLMRoundCount:           int64(row.LLMRoundCount),
		UsageInputTokens:        int64(row.UsageInputTokens),
		UsageOutputTokens:       int64(row.UsageOutputTokens),
		JevInputTokens:          pgIntPtr(row.JevInputTokens),
		JevOutputTokens:         pgIntPtr(row.JevOutputTokens),
		LatencyMS:               int64(row.LatencyMS),
		DeadlineAt:              timePtr(row.DeadlineAt),
		Decision:                row.Decision,
		Reason:                  row.Reason,
		AnalysisSafety:          stringPtr(row.AnalysisSafety),
		AnalysisSafetyReason:    stringPtr(row.AnalysisSafetyReason),
		InputHash:               row.InputHash,
		CreatedAt:               row.CreatedAt.UTC(),
	}
}

// GetDecisionRun returns a decision run by id.
func GetDecisionRun(ctx context.Context, id string) (*DecisionRun, error) {
	if err := available(); err != nil {
		return nil, err
	}
	if dialectIsSQLite() {
		row, err := modelSQLite.LLMDecisionRuns(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return decisionRunFromSQLite(row)
	}
	row, err := modelPSQL.LLMDecisionRuns(qm.Where("id = ?", id)).One(ctx, database.DB.SQL)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decisionRunFromPSQL(row), nil
}

// ListDecisionRunsByStrategy returns runs of a strategy version, newest first.
func ListDecisionRunsByStrategy(ctx context.Context, strategyVersionID string, limit int) ([]DecisionRun, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("strategy_version_id = ?", strategyVersionID), qm.OrderBy("created_at DESC")}
	if limit > 0 {
		queries = append(queries, qm.Limit(limit))
	}
	out := make([]DecisionRun, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMDecisionRuns(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			r, err := decisionRunFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMDecisionRuns(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, *decisionRunFromPSQL(row))
	}
	return out, nil
}

// ListReviewRunsBefore returns REVIEW decision runs created before cutoff (and,
// when since is non-zero, after it). The grace window keeps repeated sweeps
// idempotent: once a run has been swept, later sweeps no longer see it.
//
// ListDecisionRunsByStrategy returns runs of a strategy version, newest first.
func ListReviewRunsBefore(ctx context.Context, cutoff, since time.Time, limit int) ([]DecisionRun, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("decision = ? AND created_at < ?", "REVIEW", cutoff), qm.OrderBy("created_at ASC")}
	if !since.IsZero() {
		queries = append(queries, qm.Where("created_at > ?", since))
	}
	if limit > 0 {
		queries = append(queries, qm.Limit(limit))
	}
	out := make([]DecisionRun, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMDecisionRuns(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			r, err := decisionRunFromSQLite(row)
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMDecisionRuns(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, *decisionRunFromPSQL(row))
	}
	return out, nil
}

// RiskEvaluation mirrors llm_risk_evaluations (append-only).
type RiskEvaluation struct {
	ID               string
	DecisionRunID    string
	CheckVersion     string
	Verdict          string
	ChecksJSON       string
	RejectionsJSON   *string
	EnvelopeHash     string
	ComputedQty      *string
	ComputedNotional *string
	ReferencePrice   *string
	EvaluatedAt      time.Time
}

// InsertRiskEvaluation appends a risk evaluation.
func InsertRiskEvaluation(ctx context.Context, e *RiskEvaluation) error {
	if err := available(); err != nil {
		return err
	}
	if dialectIsSQLite() {
		row := &modelSQLite.LLMRiskEvaluation{
			ID:               e.ID,
			DecisionRunID:    e.DecisionRunID,
			CheckVersion:     e.CheckVersion,
			Verdict:          e.Verdict,
			ChecksJSON:       e.ChecksJSON,
			RejectionsJSON:   nullString(e.RejectionsJSON),
			EnvelopeHash:     e.EnvelopeHash,
			ComputedQty:      nullString(e.ComputedQty),
			ComputedNotional: nullString(e.ComputedNotional),
			ReferencePrice:   nullString(e.ReferencePrice),
			EvaluatedAt:      tsToText(e.EvaluatedAt),
		}
		return row.Insert(ctx, database.DB.SQL, boil.Infer())
	}
	row := &modelPSQL.LLMRiskEvaluation{
		ID:               e.ID,
		DecisionRunID:    e.DecisionRunID,
		CheckVersion:     e.CheckVersion,
		Verdict:          e.Verdict,
		ChecksJSON:       e.ChecksJSON,
		RejectionsJSON:   nullString(e.RejectionsJSON),
		EnvelopeHash:     e.EnvelopeHash,
		ComputedQty:      nullString(e.ComputedQty),
		ComputedNotional: nullString(e.ComputedNotional),
		ReferencePrice:   nullString(e.ReferencePrice),
		EvaluatedAt:      e.EvaluatedAt.UTC(),
	}
	return row.Insert(ctx, database.DB.SQL, boil.Infer())
}

// ListRiskEvaluationsByRun returns evaluations of a decision run oldest first.
func ListRiskEvaluationsByRun(ctx context.Context, decisionRunID string) ([]RiskEvaluation, error) {
	if err := available(); err != nil {
		return nil, err
	}
	queries := []qm.QueryMod{qm.Where("decision_run_id = ?", decisionRunID), qm.OrderBy("evaluated_at")}
	out := make([]RiskEvaluation, 0)
	if dialectIsSQLite() {
		rows, err := modelSQLite.LLMRiskEvaluations(queries...).All(ctx, database.DB.SQL)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			evaluatedAt, err := tsFromText(row.EvaluatedAt)
			if err != nil {
				return nil, err
			}
			out = append(out, RiskEvaluation{
				ID:               row.ID,
				DecisionRunID:    row.DecisionRunID,
				CheckVersion:     row.CheckVersion,
				Verdict:          row.Verdict,
				ChecksJSON:       row.ChecksJSON,
				RejectionsJSON:   stringPtr(row.RejectionsJSON),
				EnvelopeHash:     row.EnvelopeHash,
				ComputedQty:      stringPtr(row.ComputedQty),
				ComputedNotional: stringPtr(row.ComputedNotional),
				ReferencePrice:   stringPtr(row.ReferencePrice),
				EvaluatedAt:      evaluatedAt,
			})
		}
		return out, nil
	}
	rows, err := modelPSQL.LLMRiskEvaluations(queries...).All(ctx, database.DB.SQL)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, RiskEvaluation{
			ID:               row.ID,
			DecisionRunID:    row.DecisionRunID,
			CheckVersion:     row.CheckVersion,
			Verdict:          row.Verdict,
			ChecksJSON:       row.ChecksJSON,
			RejectionsJSON:   stringPtr(row.RejectionsJSON),
			EnvelopeHash:     row.EnvelopeHash,
			ComputedQty:      stringPtr(row.ComputedQty),
			ComputedNotional: stringPtr(row.ComputedNotional),
			ReferencePrice:   stringPtr(row.ReferencePrice),
			EvaluatedAt:      row.EvaluatedAt.UTC(),
		})
	}
	return out, nil
}
