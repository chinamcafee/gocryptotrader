#!/usr/bin/env python3
"""Post-regeneration normalization for the generated llm_* model tests.

Why this exists
---------------
`make gen_db_models` (sqlboiler --wipe) recreates the whole
database/models/{postgres,sqlite3} tree from the live database. The generated
per-table test files exercise full CRUD (TestDelete / TestQueryDeleteAll /
TestSliceDeleteAll / ...) for every table, but the llm_* tables are deliberately
protected by the immutability triggers from migration 20261010120800:

  * 11 tables reject DELETE (and some reject UPDATE of protected columns);
  * llm_active_model_pair additionally has `CHECK (id = 1)`, which the generic
    randomize-based suite cannot satisfy.

Those generated CRUD tests therefore assert behaviour the schema forbids, and
`go test ./database/models/...` fails on them. The project's own coverage for
these tables lives in database/repository/llm (llm_repository_test.go and
llm_domains_test.go), which exercises the tables through their real access
patterns.

What this script does (idempotent)
----------------------------------
1. Removes the suite registrations for the protected tables -- the single-line
   ``t.Run("...", testLLM<Table>...)`` calls -- from every remaining test file.
   The generated helper files are kept on purpose: sibling test files reference
   their type maps (e.g. llm_conversations_test.go reads llmChatMessageDBTypes),
   and unregistered package-level helpers compile without running.
2. Verifies that database/models/postgres/boil_main_test.go still carries the
   project patch that gracefully skips postgres ORM tests when the connection
   config is absent (see workLog 2026-10-10 session-23 attachment 4); exits 1
   with an instruction if the regeneration clobbered it.

Run it after every `make gen_db_models` invocation, then run
`go test ./database/models/...` to confirm green.
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIALECTS = ("postgres", "sqlite3")

# Tokens that mark a generated test helper as exercising a protected table:
# either directly (helper named after the table) or through a relationship
# (helper named ``test<KeptTable>To...<ProtectedToken>...``). Matching on the
# helper name -- not on the t.Run label -- also covers the y->ies plural
# (LLMDayStartInventory -> LLMDayStartInventories).
PROTECTED_TOKENS = (
    "LLMActiveModelPair",
    "LLMAuditEvent",
    "LLMBudgetReservation",
    "LLMChatMessage",
    "LLMDayStartInventor",
    "LLMDecisionRun",
    "LLMMarketSnapshot",
    "LLMOrderIntent",
    "LLMProviderProbeResult",
    "LLMRiskEvaluation",
    "LLMStrategyApproval",
)

# llm_strategy_versions itself is not delete-protected, but its generated
# full-row update tests write the frozen columns and are rejected by the
# freeze trigger. Drop only the update-shaped registrations.
PRECISION_DROP = (
    (re.compile(r'^\s*t\.Run\("[^"]*",\s*testLLMStrategyVersions?(Update|SliceUpdateAll)\)'), "llm_strategy_versions frozen columns"),
)

PATCH_MARKER = "Skipping postgres ORM tests due to missing config"

PSQL_SKIP_HELPER = '''func missingPSQLConfigKeys() []string {
\trequiredKeys := [...]string{
\t\t"psql.user",
\t\t"psql.host",
\t\t"psql.dbname",
\t}

\tmissing := make([]string, 0, len(requiredKeys))
\tfor _, key := range requiredKeys {
\t\tif strings.TrimSpace(viper.GetString(key)) == "" {
\t\t\tmissing = append(missing, key)
\t\t}
\t}
\treturn missing
}

'''

PSQL_SKIP_CALL_ANCHOR = '''\t// Load configuration
\terr = initViper()
\tif err != nil {
\t\tfmt.Println("unable to load config file")
\t\tos.Exit(-2)
\t}
'''

PSQL_SKIP_CALL = '''
\tmissingPSQLConfig := missingPSQLConfigKeys()
\tif len(missingPSQLConfig) != 0 {
\t\t// TODO(#1818): Replace this hard exit skip once sqlboiler-generated postgres tests support explicit runtime skip handling.
\t\tfmt.Printf("Skipping postgres ORM tests due to missing config: %s (see https://github.com/thrasher-corp/gocryptotrader/issues/567)\\n", strings.Join(missingPSQLConfig, ", "))
\t\tos.Exit(0)
\t}
'''


def restore_boil_main_patch(path):
    """Re-inserts the project's missing-config skip into the generated file."""
    with open(path, "r", encoding="utf-8") as handle:
        text = handle.read()
    if PATCH_MARKER in text:
        return True
    if "func TestMain(m *testing.M) {" not in text or PSQL_SKIP_CALL_ANCHOR not in text:
        print("cannot restore %s: anchors not found (regenerator output changed?)" % path)
        return False
    text = text.replace("func TestMain(m *testing.M) {", PSQL_SKIP_HELPER + "func TestMain(m *testing.M) {", 1)
    text = text.replace(PSQL_SKIP_CALL_ANCHOR, PSQL_SKIP_CALL_ANCHOR + PSQL_SKIP_CALL, 1)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)
    print("restored missing-config skip patch in %s" % path)
    return True


def main():
    removed_lines = 0
    for dialect in DIALECTS:
        directory = os.path.join(ROOT, "database", "models", dialect)
        if not os.path.isdir(directory):
            print("missing directory: %s" % directory)
            return 1

        patterns = [re.compile(r'^\s*t\.Run\("[^"]*",\s*[A-Za-z0-9_]*%s[A-Za-z0-9_]*\)' % re.escape(token)) for token in PROTECTED_TOKENS]
        patterns.extend(pattern for pattern, _ in PRECISION_DROP)
        for name in sorted(os.listdir(directory)):
            if not name.endswith(".go"):
                continue
            path = os.path.join(directory, name)
            with open(path, "r", encoding="utf-8") as handle:
                lines = handle.readlines()
            kept = [line for line in lines if not any(pattern.match(line) for pattern in patterns)]
            if len(kept) != len(lines):
                removed_lines += len(lines) - len(kept)
                with open(path, "w", encoding="utf-8") as handle:
                    handle.writelines(kept)

    boil_main = os.path.join(ROOT, "database", "models", "postgres", "boil_main_test.go")
    if not restore_boil_main_patch(boil_main):
        return 1

    print("removed %d suite registration lines" % removed_lines)
    return 0


if __name__ == "__main__":
    sys.exit(main())
