package codeintel

import "strings"

var taskKeywords = map[TaskType][]string{
	TaskExplain:   {"explain", "what is", "what does", "why", "how does", "解释", "什么是", "为什么"},
	TaskImplement: {"implement", "add", "create", "build", "write", "实现", "添加", "创建", "编写"},
	TaskFixBug:    {"fix", "bug", "error", "broken", "wrong", "修复", "修", "错误", "问题", "crash", "panic"},
	TaskRefactor:  {"refactor", "rename", "extract", "move", "重构", "重命名", "提取", "移动"},
	TaskReview:    {"review", "audit", "inspect", "检查", "审查", "审核", "code review"},
}

// ClassifyTask infers the task type from the user's message text.
// Falls back to TaskGeneral if no keywords match.
func ClassifyTask(text string) TaskType {
	lower := strings.ToLower(text)
	bestType := TaskGeneral
	bestScore := 0

	for taskType, keywords := range taskKeywords {
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestType = taskType
		}
	}
	return bestType
}
