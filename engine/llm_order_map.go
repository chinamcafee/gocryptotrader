package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-03 OrderIntent → order.Submit 映射（docs 07 §6.1，Q13 选项 A）。
//
// 纯函数：不查库、不下单，只把不可变意图翻译成 gocryptotrader 的下单结构。
// 三条容易忘记又必须记住的规则被钉在这里：
//   - **Side 只用 Buy/Sell**——币安 wrapper 不认 order.Long/Short；
//   - **Leverage = 0**——非 0 会触发 `ErrSubmitLeverageNotSupported`；
//   - **数量转 float64 后必须回读重验**（Q13："转换后强制重新校验"）——
//     M4-12 已减去一个容差 step，这里再用字符串回读证明转换没有变大。

// OrderSubmitFeeDelay mirrors the backtester's post-fill fee retrieval delay.
const OrderSubmitFeeDelay = 500 * time.Millisecond

// BuildOrderSubmit maps one stored intent onto the venue submit.
func BuildOrderSubmit(intent *llmstore.OrderIntent) (*order.Submit, error) {
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.ClientOrderID) == "" {
		return nil, fmt.Errorf("%w: intent id and client order id are required", ErrIntentInputInvalid)
	}
	pair, err := parseIntentPair(intent.Pair)
	if err != nil {
		return nil, err
	}
	item, err := asset.New(strings.ToLower(strings.TrimSpace(intent.Asset)))
	if err != nil {
		return nil, fmt.Errorf("%w: asset %q: %v", ErrIntentInputInvalid, intent.Asset, err)
	}
	side, err := parseIntentSide(intent.Side)
	if err != nil {
		return nil, err
	}
	orderType, err := parseIntentOrderType(intent.OrderType)
	if err != nil {
		return nil, err
	}
	amount, err := floatWithin(intent.Quantity)
	if err != nil {
		return nil, fmt.Errorf("llm order mapping: quantity %q: %w", intent.Quantity, err)
	}
	submit := &order.Submit{
		Exchange:   intent.Exchange,
		Type:       orderType,
		Side:       side,
		Pair:       pair,
		AssetType:  item,
		ReduceOnly: intent.ReduceOnly,
		// Leverage stays zero: the venue wrapper refuses a leveraged submit
		// (ErrSubmitLeverageNotSupported) and the account's leverage is set via
		// the risk envelope, not per order.
		Leverage:      0,
		Amount:        amount,
		ClientOrderID: intent.ClientOrderID,
		// The wrapper's fill response does not always carry fees; ask for the
		// follow-up lookup the same way the backtester does.
		RetrieveFees:     true,
		RetrieveFeeDelay: OrderSubmitFeeDelay,
	}
	if intent.Price != nil && strings.TrimSpace(*intent.Price) != "" {
		price, err := floatWithin(*intent.Price)
		if err != nil {
			return nil, fmt.Errorf("llm order mapping: price %q: %w", *intent.Price, err)
		}
		if orderType == order.StopMarket || orderType == order.StopLimit {
			// A stop order's level is a trigger, not a limit price.
			submit.TriggerPrice = price
		} else {
			submit.Price = price
		}
	}
	if orderType == order.Limit && submit.Price == 0 {
		return nil, fmt.Errorf("%w: a limit order needs a price", ErrIntentInputInvalid)
	}
	return submit, nil
}

// parseIntentPair splits "BTC_USDT" into a currency pair.
func parseIntentPair(raw string) (currency.Pair, error) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return currency.Pair{}, fmt.Errorf("%w: pair %q must be base_quote", ErrIntentInputInvalid, raw)
	}
	return currency.NewPairWithDelimiter(strings.ToUpper(parts[0]), strings.ToUpper(parts[1]), "_"), nil
}

// parseIntentSide maps the stored side ("BUY"/"SELL") onto the venue enum.
func parseIntentSide(raw string) (order.Side, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "BUY":
		return order.Buy, nil
	case orderSideSell:
		return order.Sell, nil
	}
	return order.UnknownSide, fmt.Errorf("%w: side %q must be BUY or SELL", ErrIntentInputInvalid, raw)
}

// parseIntentOrderType maps the stored type onto the venue enum.
func parseIntentOrderType(raw string) (order.Type, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case OrderTypeLimit:
		return order.Limit, nil
	case OrderTypeMarket:
		return order.Market, nil
	case OrderTypeStopMarket:
		return order.StopMarket, nil
	}
	return order.UnknownType, fmt.Errorf("%w: order type %q", ErrIntentInputInvalid, raw)
}

// floatWithin converts a decimal string to float64 and proves the conversion
// did not grow the value (Q13: the conversion is followed by a re-check). The
// round trip uses the shortest representation of the float, so a value that
// cannot be represented exactly still reads back as itself.
func floatWithin(raw string) (float64, error) {
	value, err := decParse(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrIntentInputInvalid, err)
	}
	converted, _ := value.Float64()
	roundTrip, err := decParse(strconv.FormatFloat(converted, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	if roundTrip.GreaterThan(value) {
		return 0, fmt.Errorf("%w: float64 conversion grew %s to %s", ErrIntentInputInvalid, value, roundTrip)
	}
	return converted, nil
}
