-- +goose Up
-- Immutability for the LLM fact tables (docs/llm-dev/09-数据存储与配置.md §1.2).
--
-- Same rule as the Postgres sibling, expressed in SQLite's own terms. There is
-- no stored-procedure facility, so the column guard is written out per table.
-- That verbosity is deliberate: a safety rule that can be read in one place
-- without following a call is worth the lines.
--
-- `IS NOT` rather than `<>` throughout. SQLite's `<>` is null-unsafe — when
-- either side is NULL the comparison yields NULL, the WHEN clause is not
-- satisfied, and the write goes through. `IS NOT` treats NULL as a value, which
-- is what stops a column being nulled out to escape its own protection.

-- Fully immutable: insert-only. Every one of these records something that
-- happened; none of them has a column describing a later state.
-- +goose StatementBegin
CREATE TRIGGER llm_decision_runs_immutable
BEFORE UPDATE ON llm_decision_runs
BEGIN
    SELECT RAISE(ABORT, 'llm_decision_runs is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_decision_runs_no_delete
BEFORE DELETE ON llm_decision_runs
BEGIN
    SELECT RAISE(ABORT, 'llm_decision_runs is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_chat_messages_immutable
BEFORE UPDATE ON llm_chat_messages
BEGIN
    SELECT RAISE(ABORT, 'llm_chat_messages is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_chat_messages_no_delete
BEFORE DELETE ON llm_chat_messages
BEGIN
    SELECT RAISE(ABORT, 'llm_chat_messages is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_audit_events_immutable
BEFORE UPDATE ON llm_audit_events
BEGIN
    SELECT RAISE(ABORT, 'llm_audit_events is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_audit_events_no_delete
BEFORE DELETE ON llm_audit_events
BEGIN
    SELECT RAISE(ABORT, 'llm_audit_events is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_risk_evaluations_immutable
BEFORE UPDATE ON llm_risk_evaluations
BEGIN
    SELECT RAISE(ABORT, 'llm_risk_evaluations is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_risk_evaluations_no_delete
BEFORE DELETE ON llm_risk_evaluations
BEGIN
    SELECT RAISE(ABORT, 'llm_risk_evaluations is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_day_start_inventory_immutable
BEFORE UPDATE ON llm_day_start_inventory
BEGIN
    SELECT RAISE(ABORT, 'llm_day_start_inventory is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_day_start_inventory_no_delete
BEFORE DELETE ON llm_day_start_inventory
BEGIN
    SELECT RAISE(ABORT, 'llm_day_start_inventory is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_provider_probe_results_immutable
BEFORE UPDATE ON llm_provider_probe_results
BEGIN
    SELECT RAISE(ABORT, 'llm_provider_probe_results is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_provider_probe_results_no_delete
BEFORE DELETE ON llm_provider_probe_results
BEGIN
    SELECT RAISE(ABORT, 'llm_provider_probe_results is immutable');
END;
-- +goose StatementEnd

-- A snapshot is immutable by construction (docs/llm-dev/04 §3): it is a record
-- of what was true when a decision ran, and editing it would rewrite the
-- decision's own inputs.
-- +goose StatementBegin
CREATE TRIGGER llm_market_snapshots_immutable
BEFORE UPDATE ON llm_market_snapshots
BEGIN
    SELECT RAISE(ABORT, 'llm_market_snapshots is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_market_snapshots_no_delete
BEFORE DELETE ON llm_market_snapshots
BEGIN
    SELECT RAISE(ABORT, 'llm_market_snapshots is immutable');
END;
-- +goose StatementEnd

-- Partially mutable: the lifecycle columns may change, the decision they record
-- may not. The document lists all three as fully immutable, which contradicts
-- its own schema — an approval that can never be revoked, an order intent whose
-- status can never leave PREPARED, and a budget reservation that can never be
-- marked spent. The columns allowed to change are exactly the ones the schema
-- provides for a later state.
-- +goose StatementBegin
CREATE TRIGGER llm_strategy_approvals_immutable_columns
BEFORE UPDATE ON llm_strategy_approvals
WHEN NEW.id IS NOT OLD.id
  OR NEW.strategy_version_id IS NOT OLD.strategy_version_id
  OR NEW.account_id IS NOT OLD.account_id
  OR NEW.network IS NOT OLD.network
  OR NEW.execution_mode IS NOT OLD.execution_mode
  OR NEW.approved_market_ids IS NOT OLD.approved_market_ids
  OR NEW.risk_envelope_json IS NOT OLD.risk_envelope_json
  OR NEW.risk_envelope_hash IS NOT OLD.risk_envelope_hash
  OR NEW.pair_epoch IS NOT OLD.pair_epoch
  OR NEW.llm_profile_revision_id IS NOT OLD.llm_profile_revision_id
  OR NEW.jev_profile_revision_id IS NOT OLD.jev_profile_revision_id
  OR NEW.valid_from IS NOT OLD.valid_from
  OR NEW.valid_until IS NOT OLD.valid_until
  OR NEW.approved_by IS NOT OLD.approved_by
  OR NEW.approved_at IS NOT OLD.approved_at
BEGIN
    SELECT RAISE(ABORT, 'llm_strategy_approvals may only be revoked or expired');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_strategy_approvals_no_delete
BEFORE DELETE ON llm_strategy_approvals
BEGIN
    SELECT RAISE(ABORT, 'llm_strategy_approvals is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_order_intents_immutable_columns
BEFORE UPDATE ON llm_order_intents
WHEN NEW.id IS NOT OLD.id
  OR NEW.decision_run_id IS NOT OLD.decision_run_id
  OR NEW.risk_evaluation_id IS NOT OLD.risk_evaluation_id
  OR NEW.strategy_version_id IS NOT OLD.strategy_version_id
  OR NEW.approval_id IS NOT OLD.approval_id
  OR NEW.exchange IS NOT OLD.exchange
  OR NEW.pair IS NOT OLD.pair
  OR NEW.asset IS NOT OLD.asset
  OR NEW.action IS NOT OLD.action
  OR NEW.side IS NOT OLD.side
  OR NEW.order_type IS NOT OLD.order_type
  OR NEW.quantity IS NOT OLD.quantity
  OR NEW.price IS NOT OLD.price
  OR NEW.reduce_only IS NOT OLD.reduce_only
  OR NEW.client_order_id IS NOT OLD.client_order_id
  OR NEW.notional_usd IS NOT OLD.notional_usd
  OR NEW.envelope_hash IS NOT OLD.envelope_hash
  OR NEW.pair_epoch IS NOT OLD.pair_epoch
  OR NEW.expires_at IS NOT OLD.expires_at
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'llm_order_intents may only advance its lifecycle');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_order_intents_no_delete
BEFORE DELETE ON llm_order_intents
BEGIN
    SELECT RAISE(ABORT, 'llm_order_intents is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_budget_reservations_immutable_columns
BEFORE UPDATE ON llm_budget_reservations
WHEN NEW.id IS NOT OLD.id
  OR NEW.profile_revision_id IS NOT OLD.profile_revision_id
  OR NEW.kind IS NOT OLD.kind
  OR NEW.purpose IS NOT OLD.purpose
  OR NEW.decision_run_id IS NOT OLD.decision_run_id
  OR NEW.reserved_at IS NOT OLD.reserved_at
  OR NEW.expires_at IS NOT OLD.expires_at
BEGIN
    SELECT RAISE(ABORT, 'llm_budget_reservations may only be settled');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER llm_budget_reservations_no_delete
BEFORE DELETE ON llm_budget_reservations
BEGIN
    SELECT RAISE(ABORT, 'llm_budget_reservations is immutable');
END;
-- +goose StatementEnd

-- A strategy version moves through a state machine, so the table is writable.
-- Its frozen clause set is not: once a version has been reviewed and approved,
-- the text that was approved must stay byte-identical, or the approval refers to
-- something that no longer exists. The gate on frozen_json keeps the freeze
-- itself working, because at that moment the old value is still NULL.
-- +goose StatementBegin
CREATE TRIGGER llm_strategy_versions_frozen_immutable
BEFORE UPDATE ON llm_strategy_versions
WHEN OLD.frozen_json IS NOT NULL
 AND (NEW.frozen_json IS NOT OLD.frozen_json
   OR NEW.strategy_hash IS NOT OLD.strategy_hash
   OR NEW.risk_envelope_hash IS NOT OLD.risk_envelope_hash
   OR NEW.source_prompt_cipher IS NOT OLD.source_prompt_cipher)
BEGIN
    SELECT RAISE(ABORT, 'frozen strategy version fields are immutable');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER llm_strategy_versions_frozen_immutable;
DROP TRIGGER llm_budget_reservations_no_delete;
DROP TRIGGER llm_budget_reservations_immutable_columns;
DROP TRIGGER llm_order_intents_no_delete;
DROP TRIGGER llm_order_intents_immutable_columns;
DROP TRIGGER llm_strategy_approvals_no_delete;
DROP TRIGGER llm_strategy_approvals_immutable_columns;
DROP TRIGGER llm_market_snapshots_no_delete;
DROP TRIGGER llm_market_snapshots_immutable;
DROP TRIGGER llm_provider_probe_results_no_delete;
DROP TRIGGER llm_provider_probe_results_immutable;
DROP TRIGGER llm_day_start_inventory_no_delete;
DROP TRIGGER llm_day_start_inventory_immutable;
DROP TRIGGER llm_risk_evaluations_no_delete;
DROP TRIGGER llm_risk_evaluations_immutable;
DROP TRIGGER llm_audit_events_no_delete;
DROP TRIGGER llm_audit_events_immutable;
DROP TRIGGER llm_chat_messages_no_delete;
DROP TRIGGER llm_chat_messages_immutable;
DROP TRIGGER llm_decision_runs_no_delete;
DROP TRIGGER llm_decision_runs_immutable;
