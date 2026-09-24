package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	appengine "github.com/weisyn/wescode/internal/engine"
	"github.com/weisyn/wescode/internal/failure"
	"github.com/weisyn/wescode/presets"
	"github.com/weisyn/wesgine"
	"github.com/weisyn/wesgine/message"
)

func (h *Handler) handleListSkills(_ context.Context, _ Request) (any, *RPCError) {
	skills, err := h.engine.ListSkills()
	if err != nil {
		return nil, internalError(err)
	}
	items := make([]SkillItem, 0, len(skills))
	for _, s := range skills {
		tags := s.Tags
		if tags == nil {
			tags = []string{}
		}
		items = append(items, SkillItem{
			Slug:                   s.Slug,
			Name:                   s.Name,
			Description:            s.Description,
			Enabled:                s.Enabled,
			Tags:                   tags,
			Path:                   s.Path,
			Operators:              s.Operators,
			Category:               s.Category,
			Channel:                s.Channel,
			ExecutionMode:          s.ExecutionMode,
			DisableModelInvocation: s.DisableModelInvocation,
			UsageCount:             s.UsageCount,
		})
	}
	return items, nil
}

func (h *Handler) handleCreateSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params CreateSkillParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	h.engine.InvalidateSkillCache()
	skill, err := h.engine.CreateSkill(ctx, params.Name)
	if err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[skill] skill.create", "slug", skill.Slug, "name", params.Name)
	tags := skill.Tags
	if tags == nil {
		tags = []string{}
	}
	return SkillItem{
		Slug:        skill.Slug,
		Name:        skill.Name,
		Description: skill.Description,
		Enabled:     skill.Enabled,
		Tags:        tags,
		Path:        skill.Path,
	}, nil
}

func (h *Handler) handleGetSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	info, err := cell.Skills().Info(ctx, params.Name)
	if err != nil {
		return nil, internalError(err)
	}
	resp := map[string]any{
		"name": info.Name, "slug": info.Name, "description": info.Description,
		"enabled": info.Enabled, "tags": info.Tags, "channel": info.Channel,
		"version": info.Version,
	}
	// Read the locale-resolved manifest (e.g. SKILL.zh-CN.md when Cell
	// locale is zh-CN), not the hardcoded "SKILL.md". The Loader already
	// did the resolution at scan time; Info.ManifestFile carries the
	// result. Falling back to "SKILL.md" keeps existing Cells (whose
	// engine predates ManifestFile) working.
	manifest := info.ManifestFile
	if manifest == "" {
		manifest = "SKILL.md"
	}
	if content, readErr := cell.Skills().GetContent(ctx, params.Name, manifest); readErr == nil {
		resp["content"] = string(content)
	}
	return resp, nil
}

func (h *Handler) handleUpdateSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	tmpDir, tmpErr := os.MkdirTemp("", "skill-update-*")
	if tmpErr != nil {
		return nil, internalError(tmpErr)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(params.Content), 0o644); err != nil {
		return nil, internalError(err)
	}
	if err := cell.Skills().InstallFromDir(ctx, params.Name, tmpDir); err != nil {
		return nil, internalError(err)
	}
	h.engine.InvalidateSkillCache()
	L(ctx).Info("[skill] skill.update", "name", params.Name)
	return map[string]string{"name": params.Name}, nil
}

func (h *Handler) handleCreateSkillFull(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" || params.Content == "" {
		return nil, &RPCError{Code: -32602, Message: "name and content are required"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	slug := strings.ToLower(strings.ReplaceAll(params.Name, " ", "-"))
	tmpDir, tmpErr := os.MkdirTemp("", "skill-create-*")
	if tmpErr != nil {
		return nil, internalError(tmpErr)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(params.Content), 0o644); err != nil {
		return nil, internalError(err)
	}
	if err := cell.Skills().InstallFromDir(ctx, slug, tmpDir); err != nil {
		return nil, internalError(err)
	}
	h.engine.InvalidateSkillCache()
	L(ctx).Info("[skill] skill.create_full", "slug", slug, "name", params.Name)
	return map[string]string{"name": slug}, nil
}

func (h *Handler) handleToggleSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params ToggleSkillParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Slug == "" {
		return nil, &RPCError{Code: -32602, Message: "slug is required"}
	}
	h.engine.InvalidateSkillCache()
	if err := h.engine.ToggleSkill(ctx, params.Slug, params.Enabled); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[skill] skill.toggle", "slug", params.Slug, "enabled", params.Enabled)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleDeleteSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params DeleteSkillParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Slug == "" {
		return nil, &RPCError{Code: -32602, Message: "slug is required"}
	}
	h.engine.InvalidateSkillCache()
	if err := h.engine.DeleteSkill(ctx, params.Slug); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[skill] skill.delete", "slug", params.Slug)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleReloadSkills(ctx context.Context, _ Request) (any, *RPCError) {
	h.engine.InvalidateSkillCache()
	if err := h.engine.ReloadSkills(ctx); err != nil {
		return nil, internalError(err)
	}
	L(ctx).Info("[skill] skill.reload")
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleUploadSkillFile(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name     string `json:"name"`
		SubDir   string `json:"subDir"`
		FileName string `json:"fileName"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" || params.SubDir == "" || params.FileName == "" || params.Content == "" {
		return nil, &RPCError{Code: -32602, Message: "name, subDir, fileName, and content are required"}
	}
	allowedDirs := map[string]bool{"scripts": true, "references": true, "assets": true}
	if !allowedDirs[params.SubDir] {
		return nil, &RPCError{Code: -32602, Message: "subDir must be scripts, references, or assets"}
	}
	if strings.Contains(params.FileName, "/") || strings.Contains(params.FileName, "\\") || strings.HasPrefix(params.FileName, ".") {
		return nil, &RPCError{Code: -32602, Message: "invalid fileName"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	info, err := cell.Skills().Info(ctx, params.Name)
	if err != nil {
		return nil, internalError(fmt.Errorf("skill %q not found: %w", params.Name, err))
	}
	targetDir := filepath.Join(info.Dir, params.SubDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, internalError(err)
	}
	data, err := base64.StdEncoding.DecodeString(params.Content)
	if err != nil {
		return nil, &RPCError{Code: -32602, Message: "content must be valid base64"}
	}
	if err := os.WriteFile(filepath.Join(targetDir, params.FileName), data, 0o644); err != nil {
		return nil, internalError(err)
	}
	if err := cell.Skills().Reload(ctx); err != nil {
		L(ctx).Warn("[skill] skill.reload failed after upload", "error", err)
	}
	h.engine.InvalidateSkillCache()
	L(ctx).Info("[skill] skill.upload_file", "name", params.Name, "sub_dir", params.SubDir, "file", params.FileName)
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleListSkillResources(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return nil, &RPCError{Code: -32602, Message: "name is required"}
	}
	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	resources, err := cell.Skills().ListResources(ctx, params.Name)
	if err != nil {
		return []any{}, nil
	}
	return resources, nil
}

func (h *Handler) handleSkillHealth(_ context.Context, _ Request) (any, *RPCError) {
	report, err := h.engine.SkillHealth()
	if err != nil {
		return nil, internalError(err)
	}
	return report, nil
}

func (h *Handler) handleSkillStatus(_ context.Context, _ Request) (any, *RPCError) {
	report, err := h.engine.SkillStatus()
	if err != nil {
		return nil, internalError(err)
	}
	return report, nil
}

func (h *Handler) handleCheckSkillBindings(_ context.Context, req Request) (any, *RPCError) {
	var params CheckSkillBindingsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if len(params.Slugs) == 0 {
		return nil, &RPCError{Code: -32602, Message: "slugs is required"}
	}
	result, err := h.engine.CheckSkillBindings(params.Slugs)
	if err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (h *Handler) handleValidateSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Name == "" {
		return map[string]any{"status": "compatible", "issues": []any{}}, nil
	}

	type compatIssue struct {
		Category    string `json:"category"`
		Severity    string `json:"severity"`
		Title       string `json:"title"`
		Description string `json:"description"`
		AutoFixable bool   `json:"autoFixable"`
	}

	var issues []compatIssue

	cell := h.engine.Cell()
	skills, _ := h.engine.ListSkills()
	var found *appengine.SkillInfo
	for i := range skills {
		if skills[i].Slug == params.Name {
			found = &skills[i]
			break
		}
	}

	if found != nil {
		if found.Name == "" {
			issues = append(issues, compatIssue{
				Category: "frontmatter", Severity: "error",
				Title: "缺少名称", Description: "SKILL.md frontmatter 中未设置 name 字段",
				AutoFixable: true,
			})
		}
		if found.Description == "" {
			issues = append(issues, compatIssue{
				Category: "frontmatter", Severity: "warning",
				Title: "缺少描述", Description: "建议在 frontmatter 中添加 description 字段",
				AutoFixable: true,
			})
		}
		if cell != nil {
			toolNames := cell.Tools().Names()
			registered := make(map[string]struct{}, len(toolNames))
			for _, n := range toolNames {
				registered[n] = struct{}{}
			}
			for _, op := range found.Operators {
				if _, ok := registered[op]; !ok {
					issues = append(issues, compatIssue{
						Category: "operator", Severity: "error",
						Title:       fmt.Sprintf("算子 %q 未注册", op),
						Description: fmt.Sprintf("SKILL.md 声明了 operator %q，但当前引擎中无此工具", op),
						AutoFixable: true,
					})
				}
			}
		}
	}

	if params.Content != "" {
		body := params.Content
		if idx := strings.Index(body[3:], "---"); idx >= 0 {
			body = body[3+idx+3:]
		}
		if strings.TrimSpace(body) == "" {
			issues = append(issues, compatIssue{
				Category: "content", Severity: "warning",
				Title: "正文为空", Description: "SKILL.md 正文部分没有内容，建议添加技能指导内容",
				AutoFixable: true,
			})
		}
	}

	status := "compatible"
	hasError := false
	hasWarning := false
	for _, issue := range issues {
		if issue.Severity == "error" {
			hasError = true
		}
		if issue.Severity == "warning" {
			hasWarning = true
		}
	}
	if hasError {
		status = "incompatible"
	} else if hasWarning {
		status = "adaptable"
	}
	if issues == nil {
		issues = []compatIssue{}
	}
	return map[string]any{"status": status, "issues": issues}, nil
}

// handleVisibleSkills serves `sidebar/visibleSkills` — the sole data source
// for the wesui SkillActivationBar in WescodeChatInput (ADR-326).
func (h *Handler) handleVisibleSkills(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		AgentID string `json:"agentId,omitempty"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, invalidParams(err)
		}
	}
	list, err := h.engine.ListVisibleSkills(ctx, strings.TrimSpace(params.AgentID))
	if err != nil {
		return nil, internalError(err)
	}
	if list == nil {
		list = []appengine.VisibleSkillInfo{}
	}
	return list, nil
}

func (h *Handler) handleSkillCatalog(_ context.Context, _ Request) (any, *RPCError) {
	installed, _ := h.engine.ListSkills()
	installedSet := make(map[string]struct{}, len(installed))
	for _, s := range installed {
		installedSet[s.Slug] = struct{}{}
	}

	entries, err := fs.ReadDir(presets.SkillsFS, "skills")
	if err != nil {
		return []any{}, nil
	}

	result := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		data, readErr := fs.ReadFile(presets.SkillsFS, "skills/"+slug+"/SKILL.md")
		if readErr != nil {
			continue
		}
		name := slug
		description := ""
		var tags []string
		category := "编程"
		content := string(data)
		if strings.HasPrefix(content, "---") {
			if end := strings.Index(content[3:], "---"); end >= 0 {
				yamlBlock := content[3 : 3+end]
				for _, line := range strings.Split(yamlBlock, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "name:") {
						name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
						name = strings.Trim(name, "\"'")
					} else if strings.HasPrefix(line, "description:") {
						description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
						description = strings.Trim(description, "\"'")
					} else if strings.HasPrefix(line, "- ") && len(tags) < 10 {
						tags = append(tags, strings.TrimPrefix(line, "- "))
					}
				}
			}
		}
		if tags == nil {
			tags = []string{}
		}
		result = append(result, map[string]any{
			"id":            slug,
			"name":          name,
			"description":   description,
			"category":      category,
			"tags":          tags,
			"downloadCount": 0,
			"source":        "builtin",
			"hue":           200,
			"permissions":   []string{},
		})
	}
	return result, nil
}

func (h *Handler) handleSkillHubInstall(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Slug == "" {
		return nil, &RPCError{Code: -32602, Message: "slug is required"}
	}

	data, readErr := fs.ReadFile(presets.SkillsFS, "skills/"+params.Slug+"/SKILL.md")
	if readErr != nil {
		// Invalid params, not internal: every slug the renderer can offer came
		// from skill/hubList, which enumerates this same embedded FS. A miss
		// means the caller invented a slug, so this is diagnostic text for
		// whoever wrote that call — not a sentence a user is meant to read.
		return nil, &RPCError{Code: -32602, Message: fmt.Sprintf("no builtin skill %q in the embedded catalog", params.Slug)}
	}

	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}
	tmpDir, tmpErr := os.MkdirTemp("", "skill-catalog-*")
	if tmpErr != nil {
		return nil, internalError(tmpErr)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), data, 0o644); err != nil {
		return nil, internalError(err)
	}
	if err := cell.Skills().InstallFromDir(ctx, params.Slug, tmpDir); err != nil {
		return nil, internalError(err)
	}
	h.engine.InvalidateSkillCache()
	L(ctx).Info("[skill] skill.hub_install", "slug", params.Slug)
	return map[string]string{"slug": params.Slug, "status": "installed"}, nil
}

func (h *Handler) handleRepairSkill(ctx context.Context, req Request) (any, *RPCError) {
	var params struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Issues  []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Severity    string `json:"severity"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, invalidParams(err)
	}
	if params.Content == "" {
		return nil, &RPCError{Code: -32602, Message: "content is required"}
	}

	cell := h.engine.Cell()
	if cell == nil {
		return nil, &RPCError{Code: -32603, Message: "engine not initialized"}
	}

	var issueList strings.Builder
	for i, issue := range params.Issues {
		fmt.Fprintf(&issueList, "%d. [%s] %s: %s\n", i+1, issue.Severity, issue.Title, issue.Description)
	}

	var registeredTools []string
	if names := cell.Tools().Names(); len(names) > 0 {
		registeredTools = names
	}
	toolsHint := ""
	if len(registeredTools) > 0 {
		toolsHint = "\n\n当前引擎注册的工具列表：" + strings.Join(registeredTools, ", ")
	}

	systemPrompt := `你是一个 SKILL.md 修复专家。用户会给你一个 SKILL.md 文件和发现的问题列表。
请修复所有问题，返回修复后的完整 SKILL.md 内容。

修复规则：
1. 保留原始的 YAML frontmatter 格式（--- 包围）
2. 如果缺少 name，根据文件内容推断一个合适的名称
3. 如果缺少 description，根据内容生成一句描述
4. 如果 operators 中引用了不存在的工具，替换为最接近的已注册工具名
5. 如果正文为空，根据 name 和 description 生成合理的技能指导内容
6. 保持原有内容风格，只修复已知问题
7. 只返回修复后的 SKILL.md 全文，不要任何解释` + toolsHint

	userMsg := fmt.Sprintf("请修复以下 SKILL.md：\n\n```markdown\n%s\n```\n\n发现的问题：\n%s", params.Content, issueList.String())

	chatReq := wesgine.AppChatRequest{
		Actor:        "local",
		SystemPrompt: systemPrompt,
		Messages: []message.Message{
			{Role: message.RoleUser, Content: []message.ContentBlock{message.NewTextBlock(userMsg)}},
		},
		Tags: map[string]string{"actor": "local", "purpose": "skill_repair"},
	}

	resp, err := cell.Runtime().Chat(ctx, chatReq)
	if err != nil {
		L(ctx).Warn("[skill] repairSkill LLM call failed", "error", err)
		return nil, errFailureDetail(failure.ReasonSkillRepairFailed, err.Error())
	}

	repaired := ""
	if resp != nil {
		for _, block := range resp.Message.Content {
			if block.Type == "text" {
				repaired += block.Text
			}
		}
	}
	if strings.Contains(repaired, "```markdown") {
		if start := strings.Index(repaired, "```markdown"); start >= 0 {
			rest := repaired[start+len("```markdown"):]
			if end := strings.Index(rest, "```"); end >= 0 {
				repaired = strings.TrimSpace(rest[:end])
			}
		}
	} else if strings.Contains(repaired, "```") {
		if start := strings.Index(repaired, "```"); start >= 0 {
			rest := repaired[start+3:]
			if nl := strings.Index(rest, "\n"); nl >= 0 {
				rest = rest[nl+1:]
			}
			if end := strings.Index(rest, "```"); end >= 0 {
				repaired = strings.TrimSpace(rest[:end])
			}
		}
	}

	if repaired == "" {
		// Same Reason as the call failure above: the user's next move is
		// identical either way, and "the provider errored" vs "the provider
		// answered with nothing" is a distinction only the log needs.
		return nil, errFailureDetail(failure.ReasonSkillRepairFailed, "empty response")
	}

	return map[string]string{"repairedContent": repaired}, nil
}
