package runtime

import "strings"

// cmdExeMetacharacters are characters cmd.exe treats specially even inside an
// argv-quoted argument (redirection, piping, chaining, escaping). Claude
// invokes the Windows apiKeyHelper command through cmd.exe, so per-argument
// quoting from syscall.EscapeArg (which only quotes on space/tab/quote/
// backslash) is not enough: an unquoted segment containing one of these —
// an install path with "&" in it, for example — can still be reinterpreted
// by cmd.exe instead of passed through as a literal argument.
const cmdExeMetacharacters = "&|<>^"

// wrapForCmdExeMetacharacters wraps command in one more pair of double quotes
// when it contains a cmd.exe metacharacter, so cmd.exe's own quote-stripping
// rule (a command line that starts and ends with a matching quote has that
// outer pair stripped and the interior treated as literal) neutralizes the
// metacharacter instead of letting cmd.exe reinterpret it.
func wrapForCmdExeMetacharacters(command string) string {
	if strings.ContainsAny(command, cmdExeMetacharacters) {
		return `"` + command + `"`
	}
	return command
}
