package llm

// This file is the machine-checked half of the tree's design contract.
//
// Package llm.go states the rule — the tree does not import the engine, so it
// can be extracted into a separate worker process later (OPEN-QUESTIONS Q22).
// This file enforces it. A property like that is invisible in review and easy
// to destroy with one convenient import, so it is asserted rather than
// remembered.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/thrasher-corp/gocryptotrader"

// allowedInternalPrefixes is exhaustive: an import of this repository that is
// not under one of these prefixes fails the test.
//
// The list is short on purpose. Each entry is a package small enough and
// general enough that moving llm/ out would mean moving it too, or vendoring
// it. Adding an entry is a decision about the model layer's portability, so it
// belongs in this file with a reason rather than in a call site.
//
// In particular, `exchanges/kline` is absent even though the snapshot contract
// describes candle intervals. kline imports exchanges/order, so taking the
// interval type from there would drag the order manager in behind it; the
// snapshot package defines its own small interval enum instead.
var allowedInternalPrefixes = []string{
	modulePath + "/llm",             // the tree itself, including the shared sentinels in llm/llm.go
	modulePath + "/llm/",            // and its packages
	modulePath + "/types",           // decimal and Number, the money vocabulary
	modulePath + "/currency",        // pair naming
	modulePath + "/exchanges/asset", // the asset-item vocabulary
	modulePath + "/common",          // small shared helpers
	modulePath + "/logging",         // structured logging
	modulePath + "/encoding/json",   // the codec abstraction the repo-wide depguard rule requires instead of encoding/json
}

// blockedPackages are named explicitly, with the reason, so a failure explains
// itself. They are a subset of what the allowlist already rejects; they exist
// because "you imported the engine" is a more useful message than "not in the
// list".
var blockedPackages = map[string]string{
	modulePath + "/engine":          "the engine owns the trading subsystems; importing it makes the model layer unmovable",
	modulePath + "/exchanges/order": "the order vocabulary belongs to execution; the model layer must not know how to express an order",
	modulePath + "/exchanges":       "the exchange manager pulls every wrapper, and every wrapper pulls the order manager",
	modulePath + "/database":        "persistence sits below the model layer, not inside it",
	modulePath + "/communications":  "the comms manager is an engine subsystem",
	modulePath + "/dispatch":        "dispatch is an engine subsystem",
	modulePath + "/config":          "the model layer is configured by its caller, not by the global config file",
	modulePath + "/core":            "core is an engine subsystem",
	modulePath + "/gctcli":          "the CLI depends on the engine, so the dependency would be circular in spirit",
	modulePath + "/backtester":      "the backtester is a separate process",
	modulePath + "/rpc":             "the RPC layer is a caller of this layer, never a dependency of it",
}

// importedPackage is one import edge found in the tree.
type importedPackage struct {
	file string
	path string
}

// collectImports parses every Go file under root and returns its imports.
//
// Parsing rather than running `go list` keeps the check offline and instant, and
// it sees a violation in a file that does not compile yet — which is exactly
// when a boundary mistake is cheapest to fix.
func collectImports(t *testing.T) []importedPackage {
	t.Helper()
	fset := token.NewFileSet()
	var imports []importedPackage
	walked := 0

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch {
			case path == ".":
				return nil
			case strings.HasPrefix(entry.Name(), "."), entry.Name() == "testdata", entry.Name() == "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, relErr := filepath.Rel(".", path)
		if relErr != nil {
			return relErr
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		walked++
		for _, spec := range file.Imports {
			unquoted, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			imports = append(imports, importedPackage{file: relative, path: unquoted})
		}
		return nil
	})
	require.NoError(t, err, "the llm tree must be walkable")
	// A filesystem walk that silently found nothing would make every assertion
	// below vacuously true, which is the failure mode of a test like this.
	require.Positivef(t, walked, "no Go files were parsed under %s, so this check proved nothing", ".")
	return imports
}

func TestLLMTreeIsPresent(t *testing.T) {
	t.Parallel()

	// The packages this contract is about. If one is renamed or moved, the
	// checks below would still pass while proving nothing about it.
	for _, name := range []string{
		"candidate", "coverage", "httpclient", "jev", "openai", "secrets", "snapshot",
	} {
		info, err := os.Stat(name)
		require.NoErrorf(t, err, "the %s package must exist under llm/", name)
		assert.Truef(t, info.IsDir(), "llm/%s should be a directory", name)
	}
}

func TestLLMPackagesImportOnlyTheAllowedPrefixes(t *testing.T) {
	t.Parallel()

	imports := collectImports(t)
	seenInternal := make(map[string]string, len(imports))
	for _, edge := range imports {
		if !strings.HasPrefix(edge.path, modulePath) {
			continue
		}
		seenInternal[edge.path] = edge.file

		allowed := false
		for _, prefix := range allowedInternalPrefixes {
			if strings.HasPrefix(edge.path, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			assert.Failf(t, "disallowed import",
				"%s imports %s, which is not on the model layer's allowlist. Either use a lighter dependency, "+
					"or add the prefix to allowedInternalPrefixes with a reason.", edge.file, edge.path)
		}
	}

	// Sanity: the walk must actually have seen internal imports, or the loop
	// above would have had nothing to check.
	assert.NotEmpty(t, seenInternal, "no internal imports were found, so the allowlist was never exercised")
	assert.Contains(t, seenInternal, modulePath+"/llm/candidate", "the snapshot package should depend on the contract vocabulary")
}

func TestLLMPackagesDoNotImportTheBlockedPackages(t *testing.T) {
	t.Parallel()

	for _, edge := range collectImports(t) {
		reason, blocked := blockedPackages[edge.path]
		if blocked {
			assert.Failf(t, "blocked import", "%s imports %s: %s", edge.file, edge.path, reason)
		}
		// A subpackage of a blocked package is blocked too, except where the
		// allowlist intentionally carves one out.
		for pkg, why := range blockedPackages {
			if !strings.HasPrefix(edge.path, pkg+"/") {
				continue
			}
			allowed := false
			for _, prefix := range allowedInternalPrefixes {
				if strings.HasPrefix(edge.path, prefix) {
					allowed = true
					break
				}
			}
			if !allowed {
				assert.Failf(t, "blocked import", "%s imports %s: %s", edge.file, edge.path, why)
			}
		}
	}
}

func TestLLMTreeDoesNotDependOnAThirdPartySDK(t *testing.T) {
	t.Parallel()

	// Third-party dependencies are allowed to be small and boring. A provider
	// SDK, an HTTP framework or a tracing client would each make the layer
	// harder to move and larger than it needs to be, and each one also tends to
	// come with its own transport — which would bypass the hardened client.
	allowed := map[string]bool{
		"github.com/gofrs/uuid":         true, // identifier generation
		"github.com/stretchr/testify":   true, // test assertions
		"github.com/quagmt/udecimal":    true, // reached through types/decimal under a build tag
		"github.com/shopspring/decimal": true, // the other types/decimal implementation
	}

	for _, edge := range collectImports(t) {
		if strings.HasPrefix(edge.path, modulePath) || !strings.Contains(edge.path, ".") {
			continue
		}
		root := edge.path
		for _, allowedRoot := range []string{
			"github.com/gofrs/uuid", "github.com/stretchr/testify", "github.com/quagmt/udecimal", "github.com/shopspring/decimal",
		} {
			if edge.path == allowedRoot || strings.HasPrefix(edge.path, allowedRoot+"/") {
				root = allowedRoot
				break
			}
		}
		assert.True(t, allowed[root],
			"%s imports %s. A new third-party dependency in the model layer needs a reason: "+
				"it must not bring its own transport, and it must be small enough to move.", edge.file, edge.path)
	}
}

func TestCollectImportsSeesTestFiles(t *testing.T) {
	t.Parallel()

	// A boundary violation written in a test file breaks liftability just as
	// well as one in a source file, so the walk must not skip _test.go.
	imports := collectImports(t)
	var sawTestFile, sawSourceFile bool
	for _, edge := range imports {
		if strings.HasSuffix(edge.file, "_test.go") {
			sawTestFile = true
		} else {
			sawSourceFile = true
		}
	}
	assert.True(t, sawSourceFile, "source files should be parsed")
	assert.True(t, sawTestFile, "test files should be parsed too")
}

func TestImportParsingFindsKnownEdges(t *testing.T) {
	t.Parallel()

	// Prove the parser reads what we think it reads, using an edge that exists
	// for a reason: the snapshot package uses the candidate contract.
	imports := collectImports(t)
	found := false
	for _, edge := range imports {
		if edge.path == modulePath+"/llm/candidate" && strings.HasPrefix(edge.file, "snapshot"+string(filepath.Separator)) {
			found = true
			break
		}
	}
	assert.True(t, found, "the snapshot package's dependency on llm/candidate should be visible to the parser")

	// And an import that must not exist anywhere.
	for _, edge := range imports {
		assert.NotEqualf(t, modulePath+"/engine", edge.path, "%s should not import the engine", edge.file)
	}
}
