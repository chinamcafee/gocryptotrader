# GoCryptoTrader Backtester: Macdcross package

<img src="../../../../backtester/common/backtester.png" alt="GoCryptoTrader backtester logo" width="350px" height="350px" hspace="70">

[![Build Status](https://github.com/thrasher-corp/gocryptotrader/actions/workflows/tests.yml/badge.svg?branch=master)](https://github.com/thrasher-corp/gocryptotrader/actions/workflows/tests.yml)
[![Software License](https://img.shields.io/badge/License-MIT-orange.svg?style=flat-square)](https://github.com/thrasher-corp/gocryptotrader/blob/master/LICENSE)
[![GoDoc](https://godoc.org/github.com/thrasher-corp/gocryptotrader?status.svg)](https://godoc.org/github.com/thrasher-corp/gocryptotrader/backtester/eventhandlers/strategies/macdcross)
[![Coverage Status](https://codecov.io/gh/thrasher-corp/gocryptotrader/graph/badge.svg?token=41784B23TS)](https://codecov.io/gh/thrasher-corp/gocryptotrader)

This Macdcross package is part of the GoCryptoTrader codebase.

## This is still in active development

You can track ideas, planned features and what's in progress on our [GoCryptoTrader Kanban board](https://github.com/orgs/thrasher-corp/projects/3).

Join our slack to discuss all things related to GoCryptoTrader! [GoCryptoTrader Slack](https://join.slack.com/t/gocryptotrader/shared_invite/zt-38z8abs3l-gH8AAOk8XND6DP5NfCiG_g)

## Macdcross package overview

The RSI strategy utilises [the gct-ta RSI package](https://github.com/thrasher-corp/gct-ta) to analyse market signals and output buy or sell signals based on the RSI output.
This strategy does support `SimultaneousSignalProcessing` aka [use-simultaneous-signal-processing](../../../../backtester/config/README.md).
This strategy does support strategy customisation in the following ways:
The MACD cross strategy uses [the gct-ta MACD package](https://github.com/thrasher-corp/gct-ta) and outputs a Buy signal on a golden cross (the MACD line crosses above the signal line) and a Sell signal on a death cross. It is one of the two fixed-rule baselines (Q24=A) the LLM decisions are compared against.

## Custom settings

| Field | Description |  Example |
| --- | ------- | --- |
| macd-fast | The fast EMA period | 12 |
| macd-slow | The slow EMA period; must be greater than macd-fast | 26 |
| macd-signal | The signal-line EMA period; also the warmup length together with macd-slow | 9 |

## Donations

<img src="../../../../docs/assets/donate.png" alt="Donate to GoCryptoTrader" hspace="70">

If this framework helped you in any way, or you would like to support the developers working on it, please donate Bitcoin to:

`bc1qk0jareu4jytc0cfrhr5wgshsq8282awpavfahc`
