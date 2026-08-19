package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestRedactAgentCommandArgsPreservesOnlySafeFlagNames(t *testing.T) {
	t.Parallel()

	overlongFlag := "--" + strings.Repeat("a", maxLoggedAgentCommandFlagLen)
	args := []string{
		"--api-key", "api-key-secret",
		"--dash-prefixed-secret", "-sTk9xQZ-secretvalue",
		"--token=token-secret",
		"--header", "Authorization: Bearer header-secret",
		"-c", `model_providers.example.api_key="config-secret"`,
		"--future-secret", "future-value-secret",
		"prompt-secret",
		"--verbose",
		"-not-a-short-flag",
		overlongFlag,
	}
	want := []string{
		"--api-key", redactedAgentCommandArg,
		"--dash-prefixed-secret", redactedAgentCommandArg,
		"--token",
		"--header", redactedAgentCommandArg,
		"-c", redactedAgentCommandArg,
		"--future-secret", redactedAgentCommandArg,
		redactedAgentCommandArg,
		"--verbose",
		redactedAgentCommandArg,
		redactedAgentCommandArg,
	}

	got := redactAgentCommandArgs(args, nil)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("redactAgentCommandArgs = %v, want %v", got, want)
	}
}

func TestTrustedAgentCommandPositionalsFollowSourceIndexes(t *testing.T) {
	t.Parallel()

	invocationArgs := []string{"acp", "--api-key", "-sTk9xQZ-secretvalue", "acp"}
	finalArgs := []string{
		"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "wrapper.ps1",
		"start", "q36",
		"acp", "--api-key", "-sTk9xQZ-secretvalue", "acp",
	}
	cfg := Config{LaunchPrefix: []string{"start", "q36"}}

	trusted := cfg.trustedAgentCommandPositionals(finalArgs, newAgentCommandLogArgs(
		invocationArgs,
		trustAgentCommandPositional(0, "acp"),
		trustAgentCommandPositional(3, "acp"),
	))

	wantTrusted := map[int]struct{}{
		7:  {}, // invocationStart=5 (after -NoProfile/-ExecutionPolicy/Bypass/-File/wrapper.ps1? no — see below) + 0+2 = 7
		10: {},
	}
	// finalArgs is [powerShell-prelude(4), "start", "q36", "acp", "--api-key", "-sTk9xQZ-secretvalue", "acp"] = 11 entries
	// LaunchPrefix = ["start", "q36"] (len 2)
	// invocationArgs = ["acp", "--api-key", "-sTk9xQZ-secretvalue", "acp"] (len 4)
	// originalLen = 2 + 4 = 6
	// start = 11 - 6 = 5
	// invocationStart = 5 + 2 = 7
	// trusted[0+7=7], trusted[3+7=10]
	if len(trusted) != len(wantTrusted) {
		t.Fatalf("trusted = %v, want %v", trusted, wantTrusted)
	}
	for k := range wantTrusted {
		if _, ok := trusted[k]; !ok {
			t.Fatalf("trusted = %v, want key %d present", trusted, k)
		}
	}

	// Reject if the launch prefix doesn't match the final argv suffix.
	mismatched := Config{LaunchPrefix: []string{"different", "prefix"}}
	if got := mismatched.trustedAgentCommandPositionals(finalArgs, newAgentCommandLogArgs(invocationArgs)); got != nil {
		t.Fatalf("mismatched prefix trusted = %v, want nil", got)
	}
}

func containsRuntimeArgReference(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name == "Args" {
				found = true
				return false
			}
		case *ast.Ident:
			switch strings.ToLower(node.Name) {
			case "args", "cmdargs", "argv":
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

// TestOnlyLaunchGoLogsAgentCommandArgs keeps raw argv out of adapter-local log
// calls. Every runtime process log must flow through Config.logAgentCommand in
// launch.go, where values are redacted consistently for text and JSON handlers.
// The guard checks both common field labels and the expressions themselves, so
// renaming an "args" field to "argv" cannot bypass it while still passing
// cmd.Args, args, cmdArgs, or argv to a logger.
func TestOnlyLaunchGoLogsAgentCommandArgs(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "launch.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Debug", "Info", "Warn", "Error", "Log", "LogAttrs":
			default:
				return true
			}
			for _, arg := range call.Args {
				literal, ok := arg.(*ast.BasicLit)
				if ok && literal.Kind == token.STRING &&
					(literal.Value == `"args"` || literal.Value == `"argv"` || literal.Value == `"agent command"`) {
					offenders = append(offenders, fset.Position(call.Pos()).String())
					break
				}
				if containsRuntimeArgReference(arg) {
					offenders = append(offenders, fset.Position(call.Pos()).String())
					break
				}
			}
			return true
		})
	}

	if len(offenders) > 0 {
		t.Fatalf("runtime argv logs must use Config.logAgentCommand in launch.go so values are redacted. Offending sites:\n%s",
			strings.Join(offenders, "\n"))
	}
}