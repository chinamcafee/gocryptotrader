-- +goose Up
-- Immutability for the LLM fact tables (docs/llm-dev/09-数据存储与配置.md §1.2).
--
-- The rule exists because a decision record that can be edited after the fact
-- cannot be audited. A trigger rather than a convention, because the code that
-- might edit a row is exactly the code nobody reviews for that purpose.
--
-- Two stored functions carry the whole rule:
--   * llm_reject_mutation() raises on any UPDATE or DELETE.
--   * llm_reject_immutable_columns() raises only when a column named in the
--     trigger's arguments changed, so a table that legitimately moves through a
--     lifecycle can still be written to.
--
-- to_jsonb/IS DISTINCT FROM is used instead of a chain of `<>` comparisons
-- because `<>` is null-unsafe: with `NEW.col <> OLD.col`, setting a column to
-- NULL yields NULL, the guard is not satisfied, and the write is allowed
-- through. IS DISTINCT FROM treats NULL as a value.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION llm_reject_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is immutable (attempted %)', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION llm_reject_immutable_columns() RETURNS trigger AS $$
DECLARE
    offending text;
BEGIN
    SELECT string_agg(key, ', ' ORDER BY key) INTO offending
    FROM jsonb_object_keys(to_jsonb(NEW)) AS key
    WHERE key = ANY (TG_ARGV)
      AND to_jsonb(NEW) -> key IS DISTINCT FROM to_jsonb(OLD) -> key;
    IF offending IS NOT NULL THEN
        RAISE EXCEPTION '% has immutable columns (attempted to change %)', TG_TABLE_NAME, offending
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Fully immutable: insert-only. Every one of these records something that
-- happened; none of them has a column describing a later state.
CREATE TRIGGER llm_decision_runs_immutable BEFORE UPDATE ON llm_decision_runs
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_decision_runs_no_delete BEFORE DELETE ON llm_decision_runs
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_chat_messages_immutable BEFORE UPDATE ON llm_chat_messages
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_chat_messages_no_delete BEFORE DELETE ON llm_chat_messages
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_audit_events_immutable BEFORE UPDATE ON llm_audit_events
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_audit_events_no_delete BEFORE DELETE ON llm_audit_events
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_risk_evaluations_immutable BEFORE UPDATE ON llm_risk_evaluations
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_risk_evaluations_no_delete BEFORE DELETE ON llm_risk_evaluations
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_day_start_inventory_immutable BEFORE UPDATE ON llm_day_start_inventory
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_day_start_inventory_no_delete BEFORE DELETE ON llm_day_start_inventory
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_provider_probe_results_immutable BEFORE UPDATE ON llm_provider_probe_results
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_provider_probe_results_no_delete BEFORE DELETE ON llm_provider_probe_results
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

-- A snapshot is immutable by construction (docs/llm-dev/04 §3): it is a record
-- of what was true when a decision ran, and editing it would rewrite the
-- decision's own inputs. The document's list omits it, which leaves a fact table
-- writable; that omission is corrected here rather than left as a gap.
CREATE TRIGGER llm_market_snapshots_immutable BEFORE UPDATE ON llm_market_snapshots
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();
CREATE TRIGGER llm_market_snapshots_no_delete BEFORE DELETE ON llm_market_snapshots
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

-- Partially mutable: the lifecycle columns may change, the decision they record
-- may not. The document lists all three as fully immutable, which contradicts
-- its own schema — an approval with revoke columns that can never be revoked, an
-- order intent whose status can never leave PREPARED, and a budget reservation
-- that can never be marked spent. The columns that may change are exactly the
-- ones the schema provides for a later state.
CREATE TRIGGER llm_strategy_approvals_immutable_columns BEFORE UPDATE ON llm_strategy_approvals
    FOR EACH ROW EXECUTE FUNCTION llm_reject_immutable_columns(
        'id', 'strategy_version_id', 'account_id', 'network', 'execution_mode',
        'approved_market_ids', 'risk_envelope_json', 'risk_envelope_hash', 'pair_epoch',
        'llm_profile_revision_id', 'jev_profile_revision_id', 'valid_from', 'valid_until',
        'approved_by', 'approved_at');
CREATE TRIGGER llm_strategy_approvals_no_delete BEFORE DELETE ON llm_strategy_approvals
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_order_intents_immutable_columns BEFORE UPDATE ON llm_order_intents
    FOR EACH ROW EXECUTE FUNCTION llm_reject_immutable_columns(
        'id', 'decision_run_id', 'risk_evaluation_id', 'strategy_version_id', 'approval_id',
        'exchange', 'pair', 'asset', 'action', 'side', 'order_type', 'quantity', 'price',
        'reduce_only', 'client_order_id', 'notional_usd', 'envelope_hash', 'pair_epoch',
        'expires_at', 'created_at');
CREATE TRIGGER llm_order_intents_no_delete BEFORE DELETE ON llm_order_intents
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

CREATE TRIGGER llm_budget_reservations_immutable_columns BEFORE UPDATE ON llm_budget_reservations
    FOR EACH ROW EXECUTE FUNCTION llm_reject_immutable_columns(
        'id', 'profile_revision_id', 'kind', 'purpose', 'decision_run_id',
        'reserved_at', 'expires_at');
CREATE TRIGGER llm_budget_reservations_no_delete BEFORE DELETE ON llm_budget_reservations
    FOR EACH ROW EXECUTE FUNCTION llm_reject_mutation();

-- A strategy version moves through a state machine, so the table is writable.
-- Its frozen clause set is not: once a version has been reviewed and approved,
-- the text that was approved must stay byte-identical, or the approval refers to
-- something that no longer exists. The gate is OLD.frozen_json IS NOT NULL so
-- the freeze itself (which writes both frozen_json and frozen_at) still works.
CREATE TRIGGER llm_strategy_versions_frozen_immutable BEFORE UPDATE ON llm_strategy_versions
    FOR EACH ROW WHEN (OLD.frozen_json IS NOT NULL)
    EXECUTE FUNCTION llm_reject_immutable_columns(
        'frozen_json', 'strategy_hash', 'risk_envelope_hash', 'source_prompt_cipher');

-- +goose Down
DROP TRIGGER llm_strategy_versions_frozen_immutable ON llm_strategy_versions;
DROP TRIGGER llm_budget_reservations_no_delete ON llm_budget_reservations;
DROP TRIGGER llm_budget_reservations_immutable_columns ON llm_budget_reservations;
DROP TRIGGER llm_order_intents_no_delete ON llm_order_intents;
DROP TRIGGER llm_order_intents_immutable_columns ON llm_order_intents;
DROP TRIGGER llm_strategy_approvals_no_delete ON llm_strategy_approvals;
DROP TRIGGER llm_strategy_approvals_immutable_columns ON llm_strategy_approvals;
DROP TRIGGER llm_market_snapshots_no_delete ON llm_market_snapshots;
DROP TRIGGER llm_market_snapshots_immutable ON llm_market_snapshots;
DROP TRIGGER llm_provider_probe_results_no_delete ON llm_provider_probe_results;
DROP TRIGGER llm_provider_probe_results_immutable ON llm_provider_probe_results;
DROP TRIGGER llm_day_start_inventory_no_delete ON llm_day_start_inventory;
DROP TRIGGER llm_day_start_inventory_immutable ON llm_day_start_inventory;
DROP TRIGGER llm_risk_evaluations_no_delete ON llm_risk_evaluations;
DROP TRIGGER llm_risk_evaluations_immutable ON llm_risk_evaluations;
DROP TRIGGER llm_audit_events_no_delete ON llm_audit_events;
DROP TRIGGER llm_audit_events_immutable ON llm_audit_events;
DROP TRIGGER llm_chat_messages_no_delete ON llm_chat_messages;
DROP TRIGGER llm_chat_messages_immutable ON llm_chat_messages;
DROP TRIGGER llm_decision_runs_no_delete ON llm_decision_runs;
DROP TRIGGER llm_decision_runs_immutable ON llm_decision_runs;
DROP FUNCTION llm_reject_immutable_columns();
DROP FUNCTION llm_reject_mutation();
