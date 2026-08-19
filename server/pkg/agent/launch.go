package agent

import (
	"log/slog"
	"os/exec"
	"strings"
)

const redactedAgentCommandArg = "<redacted>"

const maxLoggedAgentCommandFlagLen = 64

// agentCommandLogArgs describes the adapter-assembled argv handed to the
// launch boundary. trustedPositionals contains indexes into invocationArgs for
// literal subcommands owned by the adapter; every other positional remains
// redacted. Keeping index-and-literal assertions instead of a global string
// allowlist makes trust follow the argument's source, so an identical token
// supplied through custom_args is still treated as sensitive.
type agentCommandLogArgs struct {
	invocationArgs     []string
	trustedPositionals []trustedAgentCommandPositional
}

type trustedAgentCommandPositional struct {
	index int
	value string
}

func trustAgentCommandPositional(index int, value string) trustedAgentCommandPositional {
	return trustedAgentCommandPositional{index: index, value: value}
}

func newAgentCommandLogArgs(invocationArgs []string, trustedPositionals ...trustedAgentCommandPositional) agentCommandLogArgs {
	return agentCommandLogArgs{
		invocationArgs:     invocationArgs,
		trustedPositionals: append([]trustedAgentCommandPositional(nil), trustedPositionals...),
	}
}

// logAgentCommand is the only boundary allowed to record runtime process
// arguments. It works from the final exec.Cmd so launch prefixes and
// platform-specific rewrites are represented, but never records argument
// values: flag names and adapter-owned literal subcommands remain useful for
// diagnostics, inline values are removed, and every other positional/value
// token is replaced with a marker.
func (c Config) logAgentCommand(cmd *exec.Cmd, source agentCommandLogArgs) {
	c.logAgentCommandFields(cmd, source, 0, false)
}

// logAgentCommandWithPrompt adds only a typed prompt byte count to the safe
// command log. Keeping it separate from logAgentCommand avoids an open-ended
// optional field channel that could accidentally carry prompt content.
func (c Config) logAgentCommandWithPrompt(cmd *exec.Cmd, source agentCommandLogArgs, promptBytes int) {
	c.logAgentCommandFields(cmd, source, promptBytes, true)
}

func (c Config) logAgentCommandFields(cmd *exec.Cmd, source agentCommandLogArgs, promptBytes int, includePromptBytes bool) {
	if cmd == nil {
		return
	}
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}

	args := []string{}
	if len(cmd.Args) > 1 {
		args = cmd.Args[1:]
	}
	trustedPositionals := c.trustedAgentCommandPositionals(args, source)
	fields := []any{
		"provider", c.provider,
		"exec", cmd.Path,
		"args", redactAgentCommandArgs(args, trustedPositionals),
		"arg_count", len(args),
	}
	if includePromptBytes {
		fields = append(fields, "prompt_bytes", promptBytes)
	}
	logger.Info("agent command", fields...)
}

// trustedAgentCommandPositionals maps source indexes onto the final exec.Cmd
// argv. Platform launch rewrites may prepend a PowerShell host and wrapper
// arguments, so the original launch prefix plus adapter argv must match the
// final suffix before any positional is trusted. A mismatch fails closed.
func (c Config) trustedAgentCommandPositionals(finalArgs []string, source agentCommandLogArgs) map[int]struct{} {
	originalLen := len(c.LaunchPrefix) + len(source.invocationArgs)
	start := len(finalArgs) - originalLen
	if start < 0 {
		return nil
	}
	for i, arg := range c.LaunchPrefix {
		if finalArgs[start+i] != arg {
			return nil
		}
	}
	invocationStart := start + len(c.LaunchPrefix)
	for i, arg := range source.invocationArgs {
		if finalArgs[invocationStart+i] != arg {
			return nil
		}
	}

	trusted := make(map[int]struct{}, len(source.trustedPositionals))
	for _, positional := range source.trustedPositionals {
		if positional.index < 0 || positional.index >= len(source.invocationArgs) ||
			source.invocationArgs[positional.index] != positional.value {
			continue
		}
		trusted[invocationStart+positional.index] = struct{}{}
	}
	return trusted
}

// redactAgentCommandArgs preserves only syntactically plausible flag names and
// source-proven adapter subcommands while removing every value. Single-dash
// flags must be one ASCII letter; this keeps a token such as
// "-sTk9xQZ-secretvalue" from being mistaken for a flag. Long flags are
// length-bounded and lose inline values. All other tokens become <redacted>.
func redactAgentCommandArgs(args []string, trustedPositionals map[int]struct{}) []string {
	redacted := make([]string, len(args))
	for i, arg := range args {
		if _, ok := trustedPositionals[i]; ok {
			redacted[i] = arg
			continue
		}
		if flag, ok := safeAgentCommandFlagName(arg); ok {
			redacted[i] = flag
			continue
		}
		redacted[i] = redactedAgentCommandArg
	}
	return redacted
}

func safeAgentCommandFlagName(arg string) (string, bool) {
	flag := arg
	if equals := strings.IndexByte(flag, '='); equals > 0 {
		flag = flag[:equals]
	}
	if len(flag) == 2 && flag[0] == '-' && isASCIIAlpha(flag[1]) {
		return flag, true
	}
	if len(flag) < 3 || len(flag) > maxLoggedAgentCommandFlagLen || !strings.HasPrefix(flag, "--") || !isASCIIAlpha(flag[2]) {
		return "", false
	}
	for i := 3; i < len(flag); i++ {
		ch := flag[i]
		if !isASCIIAlpha(ch) && (ch < '0' || ch > '9') && ch != '.' && ch != '_' && ch != '-' {
			return "", false
		}
	}
	return flag, true
}

func isASCIIAlpha(ch byte) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}