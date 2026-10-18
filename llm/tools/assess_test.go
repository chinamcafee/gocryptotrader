package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeAssessOpening(t *testing.T) {
	t.Parallel()
	args, err := DecodeAssessOpening(`{"markets":["Binance:usdtmarginedfutures:AGT_USDT","Binance:usdtmarginedfutures:AGT_USDT"],"horizon":" 4-8h ","note":"follow-up"}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"Binance:usdtmarginedfutures:AGT_USDT"}, args.Markets, "duplicates collapse")
	assert.Equal(t, "4-8h", args.Horizon)
	assert.Equal(t, "follow-up", args.Note)
}

func TestDecodeAssessOpeningRejects(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"markets":["m"],"horizon":""}`,
		`{"markets":[],"horizon":"4-8h"}`,
		`{"horizon":"4-8h"}`,
		`{"markets":["m"],"horizon":"4-8h","extra":1}`,
	}
	for _, raw := range cases {
		_, err := DecodeAssessOpening(raw)
		require.ErrorIs(t, err, ErrArgumentsInvalid, raw)
	}
	args, err := decodeAssessMarkets(MaxAssessMarkets)
	require.NoError(t, err)
	assert.Len(t, args.Markets, MaxAssessMarkets)
	_, err = decodeAssessMarkets(MaxAssessMarkets + 1)
	require.ErrorIs(t, err, ErrArgumentsInvalid)
}

func decodeAssessMarkets(count int) (*AssessOpeningArgs, error) {
	var builder strings.Builder
	builder.WriteString(`{"markets":[`)
	for i := range count {
		if i > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, `"m%c"`, rune('A'+i))
	}
	builder.WriteString(`],"horizon":"4-8h"}`)
	return DecodeAssessOpening(builder.String())
}
