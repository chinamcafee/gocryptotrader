package engine

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// DeepSeek 等 OpenAI 兼容端点偶尔把工具调用写成 DSML 文本标记而不是结构化
// tool_calls（2026-10-17 实测 deepseek-flash：第二轮返回
// `<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="screenMarkets">...`）。若不
// 转换，这段标记会作为"回答"展示并落库——用户看到的是一串不可读的数据。
//
// 解析只负责把标记还原成调用；工具名白名单、参数 schema、作用域与预算仍由
// 循环和 runner 逐项执行——DSML 不是绕过任何闸门的通道。

const dsmlMarker = `[｜|]{1,2}\s*DSML\s*[｜|]{1,2}`

var (
	dsmlBlockPattern  = regexp.MustCompile(`(?s)<\s*` + dsmlMarker + `\s*calls\s*>(.*?)<\s*/\s*` + dsmlMarker + `\s*calls\s*>`)
	dsmlInvokePattern = regexp.MustCompile(`(?s)<\s*` + dsmlMarker + `\s*invoke\s+name="([^"]+)"\s*>(.*?)<\s*/\s*` + dsmlMarker + `\s*invoke\s*>`)
	dsmlParamPattern  = regexp.MustCompile(`(?s)<\s*` + dsmlMarker + `\s*parameter\s+name="([^"]+)"(?:\s+string="(true|false)")?\s*>(.*?)<\s*/\s*` + dsmlMarker + `\s*parameter\s*>`)
	dsmlTagPattern    = regexp.MustCompile(`(?s)<\s*/?\s*` + dsmlMarker + `[^>]*>`)
	dsmlMarkerPattern = regexp.MustCompile(`<\s*` + dsmlMarker)
)

// parseDSMLToolCalls extracts DSML invoke blocks from a model reply. The
// cleaned content never carries markup: complete blocks are removed, and an
// unterminated marker (a truncated stream) cuts the remainder so a raw tag can
// neither be shown nor persisted. hadMarkup reports whether any marker was
// seen, even when no call could be recovered.
func parseDSMLToolCalls(content string) (calls []openai.ToolCall, cleaned string, hadMarkup bool) {
	hadMarkup = dsmlMarkerPattern.MatchString(content)
	if !hadMarkup {
		return nil, content, false
	}
	idPrefix := contentSha256(content)
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	for b, block := range dsmlBlockPattern.FindAllStringSubmatch(content, -1) {
		for i, invoke := range dsmlInvokePattern.FindAllStringSubmatch(block[1], -1) {
			args := make(map[string]any)
			for _, param := range dsmlParamPattern.FindAllStringSubmatch(invoke[2], -1) {
				name := strings.TrimSpace(param[1])
				if name == "" {
					continue
				}
				raw := strings.TrimSpace(param[3])
				var value any
				if param[2] == "true" {
					value = raw
				} else if err := json.Unmarshal([]byte(raw), &value); err != nil {
					value = raw
				}
				args[name] = value
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				continue
			}
			calls = append(calls, openai.ToolCall{
				ID:   fmt.Sprintf("dsml-%s-%d-%d", idPrefix, b+1, i+1),
				Type: openai.ToolTypeFunction,
				Function: openai.ToolCallFunction{
					Name:      strings.TrimSpace(invoke[1]),
					Arguments: string(encoded),
				},
			})
		}
	}
	cleaned = dsmlBlockPattern.ReplaceAllString(content, "")
	cleaned = dsmlTagPattern.ReplaceAllString(cleaned, "")
	if loc := dsmlMarkerPattern.FindStringIndex(cleaned); loc != nil {
		cleaned = cleaned[:loc[0]]
	}
	return calls, strings.TrimSpace(cleaned), true
}
