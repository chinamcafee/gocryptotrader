package main

import (
	"fmt"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/urfave/cli/v2"
)

// The providers command family (docs/llm-dev 08 §3.4).
//
// It mirrors the RPCs one to one: each action dials, converts its flags and
// prints the raw JSON reply, so scripted use and manual review see exactly the
// bytes the API returned. Secrets are entered into the vault via
// `gctcli llm secrets` (M1-01) and referenced here only by their `secret://`
// ref plus fingerprint -- the key itself never appears in a command line.

const (
	flagProvKind          = "kind"
	flagProvName          = "name"
	flagProvProfile       = "profile"
	flagProvBaseURL       = "base-url"
	flagProvModel         = "model"
	flagProvKeyRef        = "key-ref"
	flagProvFingerprint   = "fingerprint"
	flagProvTimeout       = "timeout"
	flagProvMaxOutTokens  = "max-output-tokens"
	flagProvPromptVersion = "prompt-version"
	flagProvBudget        = "budget"
	flagProvReserveCap    = "reservation-cap"
	flagProvAllowLocal    = "allow-insecure-localhost"
	flagProvProxy         = "proxy"
	flagProvRevision      = "revision"
	flagProvLLMRevision   = "llm-revision"
	flagProvJevRevision   = "jev-revision"
	flagProvExpectedEpoch = "expected-epoch"
	flagProvPassword      = "password"
	flagProvIdempotency   = "idempotency-key"
)

var llmProvidersCommand = &cli.Command{
	Name:  "providers",
	Usage: "manage provider profiles, layered probes and the active model pair",
	Subcommands: []*cli.Command{
		{
			Name:  "list",
			Usage: "list provider profiles with their newest revision",
			Flags: []cli.Flag{&cli.BoolFlag{Name: "archived", Usage: "include archived profiles"}},
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMListProviderProfiles(c.Context, &gctrpc.LLMListProviderProfilesRequest{IncludeArchived: c.Bool("archived")})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "create",
			Usage: "create a disabled profile (kind: LLM_OPENAI_COMPAT or JEV)",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: flagProvKind, Usage: "provider kind"},
				&cli.StringFlag{Name: flagProvName, Usage: "display name"},
				&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
			},
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMCreateProviderProfile(c.Context, &gctrpc.LLMCreateProviderProfileRequest{
					Kind: c.String(flagProvKind), DisplayName: c.String(flagProvName), Actor: c.String(flagNameActor),
				})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "update",
			Usage: "append a new immutable revision to a profile",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: flagProvProfile, Usage: "profile id"},
				&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
				&cli.StringFlag{Name: flagProvBaseURL, Usage: "provider base URL"},
				&cli.StringFlag{Name: flagProvModel, Usage: "model id (Jev needs jev-<major>.<minor>.<patch>)"},
				&cli.StringFlag{Name: flagProvKeyRef, Usage: "vault reference, e.g. secret://llm/deepseek"},
				&cli.StringFlag{Name: flagProvFingerprint, Usage: "api key fingerprint (16 hex)"},
				&cli.Int64Flag{Name: flagProvTimeout, Usage: "request timeout in seconds"},
				&cli.Int64Flag{Name: flagProvMaxOutTokens, Usage: "max output tokens (LLM only)"},
				&cli.StringFlag{Name: flagProvPromptVersion, Usage: "prompt version (LLM only)"},
				&cli.StringFlag{Name: flagProvBudget, Usage: "daily budget in USD as a decimal string"},
				&cli.Int64Flag{Name: flagProvReserveCap, Usage: "24h reservation cap"},
				&cli.BoolFlag{Name: flagProvAllowLocal, Usage: "allow a plain-HTTP loopback endpoint"},
				&cli.StringFlag{Name: flagProvProxy, Usage: "outbound proxy address"},
			},
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMUpdateProviderProfile(c.Context, &gctrpc.LLMUpdateProviderProfileRequest{
					Id: c.String(flagProvProfile), Actor: c.String(flagNameActor),
					BaseUrl: c.String(flagProvBaseURL), ModelId: c.String(flagProvModel),
					ApiKeyRef: c.String(flagProvKeyRef), ApiKeyFingerprint: c.String(flagProvFingerprint),
					TimeoutSeconds: c.Int64(flagProvTimeout), MaxOutputTokens: c.Int64(flagProvMaxOutTokens),
					PromptVersion: c.String(flagProvPromptVersion), DailyBudgetUsd: c.String(flagProvBudget),
					ReservationCap_24H:     c.Int64(flagProvReserveCap),
					AllowInsecureLocalhost: c.Bool(flagProvAllowLocal),
					ProxyAddress:           c.String(flagProvProxy),
				})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "delete",
			Usage: "archive a profile (rows are never removed)",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: flagProvProfile, Usage: "profile id"},
				&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
			},
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMDeleteProviderProfile(c.Context, &gctrpc.LLMDeleteProviderProfileRequest{
					Id: c.String(flagProvProfile), Actor: c.String(flagNameActor),
				})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "test",
			Usage: "run the layered capability probes against a revision",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: flagProvProfile, Usage: "profile id"},
				&cli.StringFlag{Name: flagProvRevision, Usage: "revision id (default: the live revision)"},
			},
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMTestProviderProfile(c.Context, &gctrpc.LLMTestProviderProfileRequest{
					Id: c.String(flagProvProfile), RevisionId: c.String(flagProvRevision),
				})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "pair",
			Usage: "show the active model pair",
			Action: func(c *cli.Context) error {
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMGetActiveModelPair(c.Context, &gctrpc.LLMGetActiveModelPairRequest{})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
		{
			Name:  "switch",
			Usage: "CAS-switch the active model pair (requires re-authentication)",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: flagProvLLMRevision, Usage: "LLM profile revision id (empty clears the slot)"},
				&cli.StringFlag{Name: flagProvJevRevision, Usage: "Jev profile revision id (empty clears the slot)"},
				&cli.Int64Flag{Name: flagProvExpectedEpoch, Usage: "the pair epoch the decision was read from"},
				&cli.StringFlag{Name: flagProvPassword, Usage: "operator password (re-authentication)"},
				&cli.StringFlag{Name: flagProvIdempotency, Usage: "idempotency key"},
				&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
			},
			Action: func(c *cli.Context) error {
				if c.String(flagProvIdempotency) == "" {
					return fmt.Errorf("--%s is required: a retry without a key cannot be told apart from a second switch", flagProvIdempotency)
				}
				conn, cancel, err := setupClient(c)
				if err != nil {
					return err
				}
				defer cancel()
				result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMSwitchActiveModelPair(c.Context, &gctrpc.LLMSwitchActiveModelPairRequest{
					LlmProfileRevisionId: c.String(flagProvLLMRevision),
					JevProfileRevisionId: c.String(flagProvJevRevision),
					ExpectedPairEpoch:    c.Int64(flagProvExpectedEpoch),
					OperatorPassword:     c.String(flagProvPassword),
					IdempotencyKey:       c.String(flagProvIdempotency),
					Actor:                c.String(flagNameActor),
				})
				if err != nil {
					return err
				}
				jsonOutput(result)
				return nil
			},
		},
	},
}
