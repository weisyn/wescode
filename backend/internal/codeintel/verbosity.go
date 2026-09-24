package codeintel

import "strconv"

// VerbosityLevel controls the detail level of tool results.
// Reduces token consumption when Agent doesn't need full details.
type VerbosityLevel string

const (
	VerbositySummary VerbosityLevel = "summary" // ~100 tokens: names + counts only
	VerbosityDetail  VerbosityLevel = "detail"  // ~400 tokens: names + signatures + paths
	VerbosityFull    VerbosityLevel = "full"    // ~1K tokens: full code snippets
)

// ParseVerbosity normalizes a verbosity string. Defaults to VerbosityDetail.
func ParseVerbosity(v string) VerbosityLevel {
	switch VerbosityLevel(v) {
	case VerbositySummary:
		return VerbositySummary
	case VerbosityFull:
		return VerbosityFull
	default:
		return VerbosityDetail
	}
}

// TruncateWithTotal 按 verbosity 截断，并返回**截断前**的总数。
//
// 返回两个值是为了让调用点无法沉默地丢掉总数：前一版把它丢在函数内部，于是 18 个
// 调用点里 8 个报的是截断后的 len()——"Found 5 caller(s)" 而实际 20 个，而漏掉不报错、
// 不打日志，只是那个数字变小了。缺陷清单与治法见 design/27-presentation-contract.md。
func TruncateWithTotal[T any](items []T, v VerbosityLevel) (shown []T, total int) {
	total = len(items)
	limit := verbosityLimit(v)
	if total <= limit {
		return items, total
	}
	return items[:limit], total
}

// CountPhrase 把"显示了几个 / 一共几个"说成一句话，供工具写进 `Content`。
//
// 与截断器同处一个文件是刻意的：两者是一对，分开放会让下一个人用了前者而想不起后者。
// `total < shown`（调用方算错）时不产出 "5 of 1" 这种矛盾数字——它会让用户以为 UI 坏了。
func CountPhrase(shown, total int) string {
	if total > shown {
		return strconv.Itoa(shown) + " of " + strconv.Itoa(total)
	}
	return strconv.Itoa(shown)
}

func verbosityLimit(v VerbosityLevel) int {
	switch v {
	case VerbositySummary:
		return 5
	case VerbosityDetail:
		return 15
	case VerbosityFull:
		return 50
	default:
		return 15
	}
}
