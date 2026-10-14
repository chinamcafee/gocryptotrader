package engine

import (
	"context"
	"fmt"
	"time"
)

// M4-11 装配件 · 时钟漂移读数（G 组 `ClockDriftMs` 的读面）。
//
// 07 §5.6 `blockOnClockDrift`（Q8 定案 1000ms）的判据是「与交易所校时的
// 偏差」——本件从**交易所自报时钟**读数：漂移 = |venue 时间 − 本地时间|，
// 取毫秒幅值（G 组映射对负值报「不可读」，幅值是其唯一合法形状——检查侧
// 的 ±limit 双边界只等幅值来填）。
//
// 读不到即拒：nil 源、零 as-of、零回包、venue 错误——都拒绝而非报 0
// （0 会静默通过漂移闸=fail-open）。
func readClockDriftMs(ctx context.Context, src iServerTimeSource, now time.Time) (int64, error) {
	if src == nil {
		return 0, fmt.Errorf("%w: the server-time source is required", ErrRiskFactsInput)
	}
	if now.IsZero() {
		return 0, fmt.Errorf("%w: the drift needs an as-of instant", ErrRiskFactsInput)
	}
	serverTime, err := src.UServerTime(ctx)
	if err != nil {
		return 0, fmt.Errorf("llm clock drift: reading the venue time: %w", err)
	}
	if serverTime.IsZero() {
		return 0, fmt.Errorf("%w: the venue returned no server time", ErrRiskFactsInput)
	}
	drift := serverTime.Sub(now)
	if drift < 0 {
		drift = -drift
	}
	return drift.Milliseconds(), nil
}

// iServerTimeSource is the venue seam (binance's Exchange satisfies it via
// UServerTime).
type iServerTimeSource interface {
	GetName() string
	UServerTime(ctx context.Context) (time.Time, error)
}
