package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/urfave/cli/v2"
)

// llmModeCommand is the operator's run-mode switch: the CLI caller of the
// LLMSetRunMode RPC (docs 08 §3.6). Re-authentication is mandatory; the LIVE
// target additionally needs the typed acknowledgement phrase, and the binding
// approval is read server-side.
var llmModeCommand = &cli.Command{
	Name:  "mode",
	Usage: "switch the LLM run mode (re-authentication required; LIVE needs --acknowledged)",
	Flags: []cli.Flag{
		&cli.StringFlag{Name: "run-mode", Usage: "READ_ONLY / SHADOW / PAPER / TESTNET / LIVE"},
		&cli.StringFlag{Name: flagNameActor, Usage: flagUsageActor},
		&cli.StringFlag{Name: "acknowledged", Usage: "the LIVE acknowledgement phrase (I_UNDERSTAND)"},
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
		result, err := gctrpc.NewGoCryptoTraderServiceClient(conn).LLMSetRunMode(c.Context, &gctrpc.LLMSetRunModeRequest{
			RunMode:      c.String("run-mode"),
			Password:     password,
			Actor:        c.String(flagNameActor),
			Acknowledged: c.String("acknowledged"),
		})
		if err != nil {
			return err
		}
		jsonOutput(result)
		return nil
	},
}
