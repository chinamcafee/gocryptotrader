package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/urfave/cli/v2"
)

// The strategies/decisions command family (docs/llm-dev 08 §4.1, §3.2/§3.5).
//
// These commands mirror the RPCs one to one: each action dials, converts its
// flags and prints the raw JSON reply, so scripted use and manual review see
// exactly the same bytes the API returned.

const (
	flagNameVersion  = "version"
	flagNameActor    = "actor"
	flagUsageVersion = "strategy version id"
	flagUsageActor   = "operator identity"
)

var llmCommand = &cli.Command{
	Name:  "llm",
	Usage: "LLM strategy, approval and decision commands",
	Subcommands: []*cli.Command{
		llmProvidersCommand,
		llmModeCommand,
		{
			Name:  "strategies",
			Usage: "manage strategy versions, approvals and the version tree",
			Subcommands: []*cli.Command{
				{
					Name:  "list",
					Usage: "list strategies",
					Flags: []cli.Flag{&cli.BoolFlag{Name: "archived", Usage: "include archived strategies"}},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMListStrategies(c.Context, &gctrpc.LLMListStrategiesRequest{IncludeArchived: c.Bool("archived")})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "get",
					Usage: "get one strategy version with its tree",
					Flags: []cli.Flag{&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion}},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetStrategyVersion(c.Context, &gctrpc.LLMGetStrategyVersionRequest{Id: c.String(flagNameVersion)})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "extract",
					Usage: "extract a policy proposal from a conversation",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "conversation", Usage: "conversation id"},
						&cli.StringFlag{Name: "revision", Usage: "LLM profile revision id"},
						&cli.StringFlag{Name: "markets", Usage: "comma-separated market ids the proposal may cover"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMExtractPolicy(c.Context, &gctrpc.LLMExtractPolicyRequest{
							ConversationId:    c.String("conversation"),
							ProfileRevisionId: c.String("revision"),
							Markets:           splitCSV(c.String("markets")),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "freeze",
					Usage: "freeze a reviewed reviewable-v1 document",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
						&cli.StringFlag{Name: "policy-file", Usage: "path to the reviewable-v1 JSON to freeze"},
					},
					Action: func(c *cli.Context) error {
						policyJSON, err := os.ReadFile(c.String("policy-file"))
						if err != nil {
							return fmt.Errorf("reading policy file: %w", err)
						}
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMFreezeStrategyVersion(c.Context, &gctrpc.LLMFreezeStrategyVersionRequest{
							Id: c.String(flagNameVersion), Actor: c.String(flagNameActor), PolicyJson: string(policyJSON),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "diff",
					Usage: "diff two frozen versions",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "from", Usage: "from version id"},
						&cli.StringFlag{Name: "to", Usage: "to version id"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMDiffStrategyVersions(c.Context, &gctrpc.LLMDiffStrategyVersionsRequest{FromId: c.String("from"), ToId: c.String("to")})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "approve",
					Usage: "approve a frozen version (re-authentication required)",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: "account", Usage: "account id"},
						&cli.StringFlag{Name: "network", Usage: "binance-mainnet / binance-testnet"},
						&cli.StringFlag{Name: "mode", Usage: "SHADOW / PAPER / TESTNET / LIVE"},
						&cli.StringFlag{Name: "markets", Usage: "comma-separated approved market ids"},
						&cli.Int64Flag{Name: "valid-minutes", Usage: "approval validity in minutes (<= 10080)"},
						&cli.Int64Flag{Name: "pair-epoch", Usage: "the pair epoch the approval binds to"},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
						&cli.StringFlag{Name: "idempotency", Usage: "idempotency key for a safe retry"},
						&cli.BoolFlag{Name: "password-stdin", Usage: "read the operator password from stdin"},
						&cli.StringFlag{Name: "password", Usage: "operator password (prefer --password-stdin)"},
					},
					Action: func(c *cli.Context) error {
						password := c.String("password")
						if c.Bool("password-stdin") {
							line, err := bufio.NewReader(os.Stdin).ReadString('\n')
							if err != nil && line == "" {
								return fmt.Errorf("reading the operator password: %w", err)
							}
							password = strings.TrimSpace(line)
						}
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMApproveStrategyVersion(c.Context, &gctrpc.LLMApproveStrategyVersionRequest{
							StrategyVersionId: c.String(flagNameVersion),
							AccountId:         c.String("account"),
							Network:           c.String("network"),
							ExecutionMode:     c.String("mode"),
							ApprovedMarketIds: splitCSV(c.String("markets")),
							ValidMinutes:      c.Int64("valid-minutes"),
							OperatorPassword:  password,
							IdempotencyKey:    c.String("idempotency"),
							ExpectedPairEpoch: c.Int64("pair-epoch"),
							Actor:             c.String(flagNameActor),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "revoke",
					Usage: "revoke an approval",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "approval", Usage: "approval id"},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
						&cli.StringFlag{Name: "reason", Usage: "revocation reason"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMRevokeApproval(c.Context, &gctrpc.LLMRevokeApprovalRequest{
							Id: c.String("approval"), Actor: c.String(flagNameActor), Reason: c.String("reason"),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "pause",
					Usage: "pause a strategy version",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
						&cli.StringFlag{Name: "reason", Usage: "pause reason"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMPauseStrategy(c.Context, &gctrpc.LLMPauseStrategyRequest{
							StrategyVersionId: c.String(flagNameVersion), Actor: c.String(flagNameActor), Reason: c.String("reason"),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "resume",
					Usage: "resume a paused strategy version (requires a live approval)",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
						&cli.StringFlag{Name: "reason", Usage: "resume reason"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMResumeStrategy(c.Context, &gctrpc.LLMResumeStrategyRequest{
							StrategyVersionId: c.String(flagNameVersion), Actor: c.String(flagNameActor), Reason: c.String("reason"),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
			},
		},
		{
			Name:  "decisions",
			Usage: "review decision runs and their audit trail",
			Subcommands: []*cli.Command{
				{
					Name:  "list",
					Usage: "list decision runs for a strategy version",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "strategy", Usage: flagUsageVersion},
						&cli.IntFlag{Name: "limit", Usage: "maximum rows"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMListDecisionRuns(c.Context, &gctrpc.LLMListDecisionRunsRequest{
							StrategyVersionId: c.String("strategy"), Limit: int32(c.Int("limit")),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "get",
					Usage: "get one decision run with its source documents",
					Flags: []cli.Flag{&cli.StringFlag{Name: "id", Usage: "decision run id"}},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetDecisionRun(c.Context, &gctrpc.LLMGetDecisionRunRequest{Id: c.String("id")})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "lineage",
					Usage: "get the full audit chain for one decision run (A13)",
					Flags: []cli.Flag{&cli.StringFlag{Name: "id", Usage: "decision run id"}},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetDecisionLineage(c.Context, &gctrpc.LLMGetDecisionLineageRequest{Id: c.String("id")})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "risk",
					Usage: "get the risk evidence: actual vs allowed for every check (A12)",
					Flags: []cli.Flag{&cli.StringFlag{Name: "id", Usage: "decision run id"}},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetRiskEvidence(c.Context, &gctrpc.LLMGetRiskEvidenceRequest{DecisionRunId: c.String("id")})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "trigger",
					Usage: "manually trigger one analysis",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: "market", Usage: "market id"},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMTriggerAnalysis(c.Context, &gctrpc.LLMTriggerAnalysisRequest{
							StrategyVersionId: c.String(flagNameVersion), MarketId: c.String("market"), Actor: c.String(flagNameActor),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
				{
					Name:  "preview",
					Usage: "read-only preview: produces HOLD/REVIEW only",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: flagNameVersion, Usage: flagUsageVersion},
						&cli.StringFlag{Name: "market", Usage: "market id"},
						&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMPreviewDecision(c.Context, &gctrpc.LLMPreviewDecisionRequest{
							StrategyVersionId: c.String(flagNameVersion), MarketId: c.String("market"), Actor: c.String(flagNameActor),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
			},
		},
		{
			Name:  "audit",
			Usage: "read the audit event stream",
			Subcommands: []*cli.Command{
				{
					Name:  "list",
					Usage: "list audit events, optionally by kind",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "kind", Usage: "event kind filter"},
						&cli.IntFlag{Name: "limit", Usage: "maximum rows"},
					},
					Action: func(c *cli.Context) error {
						conn, cancel, err := setupClient(c)
						if err != nil {
							return err
						}
						defer cancel()
						result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMListAuditEvents(c.Context, &gctrpc.LLMListAuditEventsRequest{
							Kind: c.String("kind"), Limit: int32(c.Int("limit")),
						})
						if err != nil {
							return err
						}
						jsonOutput(result)
						return nil
					},
				},
			},
		},
		{
			Name:  "readiness",
			Usage: "admission check for a target network (exit 2 = NOT_READY, docs 11 §8)",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "network", Usage: "target network, e.g. binance-testnet"},
				&cli.StringFlag{Name: "mode", Usage: "run mode to admit (default TESTNET)"},
				&cli.BoolFlag{Name: "json", Usage: "print the raw JSON report instead of the gate table"},
			},
			Action: func(c *cli.Context) error {
				network := c.String("network")
				if network == "" {
					return errors.New("--network is required, e.g. binance-testnet")
				}
				mode := c.String("mode")
				if mode == "" {
					mode = "TESTNET"
				}
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetReadiness(c.Context, &gctrpc.LLMReadinessRequest{
					Network: network, Mode: mode,
				})
				if err != nil {
					return err
				}
				if c.Bool("json") {
					jsonOutput(result)
				} else {
					for _, gate := range result.GetGates() {
						fmt.Printf("%-24s %-4s %s\n", gate.GetCode(), gate.GetStatus(), gate.GetDetail())
					}
					verdict := "READY"
					if !result.GetReady() {
						verdict = "NOT_READY"
					}
					fmt.Printf("%s  %s / %s  (%s)\n", verdict, result.GetNetwork(), result.GetMode(), result.GetCheckedAt())
				}
				if !result.GetReady() {
					os.Exit(2)
				}
				return nil
			},
		},
	},
}

// splitCSV turns a comma-separated flag into a slice, dropping blanks so
// "a,,b" and "a,b" behave the same.
func splitCSV(value string) []string {
	var out []string
	for part := range strings.SplitSeq(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
