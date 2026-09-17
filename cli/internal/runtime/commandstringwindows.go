//go:build windows

package runtime

import (
	"strings"
	"syscall"
)

// BuildCommandString renders argv using Windows command-line quoting rules,
// then wraps the result if needed so cmd.exe cannot reinterpret a
// metacharacter in an unquoted segment (see wrapForCmdExeMetacharacters).
func BuildCommandString(arguments ...string) string {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, syscall.EscapeArg(argument))
	}
	return wrapForCmdExeMetacharacters(strings.Join(quoted, " "))
}
