package vprinter

import "strings"

// quoteArgs 는 인자 슬라이스를 Windows 명령행 한 줄로 합친다(공백 포함 인자는 따옴표).
func quoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
