package engine

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M0-13 边界的下半句（docs 10 §1.2）：硬风控引擎不允许 import 模型客户端——
// 它拿不到任何能"问模型意见"的东西，物理上保证裁决纯函数。
//
// llm/boundary_test.go 保证了 llm/ 树不反向依赖 engine；本测试保证 engine 里
// 的风控裁决文件不依赖任何模型/网络侧包。两组合起来才是文档承诺的完整边界。
func TestRiskEngineImportsNoModelClients(t *testing.T) {
	t.Parallel()

	// 风控裁决的三个文件：检查目录 + 数量重算 + 前置门禁。
	files := []string{"llm_risk.go", "llm_sizing.go", "llm_pregate.go"}
	forbidden := map[string]string{
		"github.com/thrasher-corp/gocryptotrader/llm/openai":     "the risk engine must not be able to consult a model",
		"github.com/thrasher-corp/gocryptotrader/llm/jev":        "the risk engine must not be able to consult Jev",
		"github.com/thrasher-corp/gocryptotrader/llm/httpclient": "the risk engine must not own an HTTP client",
		"github.com/thrasher-corp/gocryptotrader/llm/tools":      "the risk engine rules on facts handed to it, it fetches none",
		"github.com/thrasher-corp/gocryptotrader/llm/chat":       "the risk engine is not a chat component",
		"database/sql": "the risk engine is a pure function; it must not query",
		"net/http":     "the risk engine must not make network calls",
	}

	fset := token.NewFileSet()
	for _, name := range files {
		parsed, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		require.NoErrorf(t, err, "parsing %s", name)
		for _, imp := range parsed.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			reason, ok := forbidden[path]
			assert.Falsef(t, ok, "%s imports %q: %s", name, path, reason)
		}
	}
}
