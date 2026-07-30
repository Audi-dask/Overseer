package ocrreview

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Audi-dask/Overseer/internal/firewall"
	"github.com/Audi-dask/Overseer/internal/model"
	"github.com/Audi-dask/Overseer/internal/ocr/agent"
	"github.com/Audi-dask/Overseer/internal/ocr/config/template"
	"github.com/Audi-dask/Overseer/internal/ocr/config/toolsconfig"
	"github.com/Audi-dask/Overseer/internal/ocr/diff"
	"github.com/Audi-dask/Overseer/internal/ocr/gitcmd"
	ocrllm "github.com/Audi-dask/Overseer/internal/ocr/llm"
	ocrmodel "github.com/Audi-dask/Overseer/internal/ocr/model"
	"github.com/Audi-dask/Overseer/internal/ocr/tool"
	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/store"
	"github.com/Audi-dask/Overseer/internal/workspace"
)

// Result is the outcome of an agent review run.
type Result struct {
	Comments []ocrmodel.LlmComment
	Markdown string
	Files    int
	Tokens   int64
}

// Options configures a single agent review run.
type Options struct {
	RepoDir       string
	CommitSHA     string
	Concurrency   int
	FirewallRules string
	// Guidance is the dynamically configured and sole review strategy. A small
	// host-owned protocol is prepended only to preserve tool calling and
	// structured-comment delivery.
	Guidance string
}

// Run executes the vendored OCR agent against a local git checkout at opts.CommitSHA.
func Run(ctx context.Context, st *store.Store, opts Options) (*Result, error) {
	repoDir, commitSHA := opts.RepoDir, opts.CommitSHA
	primary, key, err := st.GetLLMByRole(ctx, model.LLMRolePrimary)
	if err != nil {
		return nil, fmt.Errorf("primary llm: %w", err)
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	tpl, err := template.LoadDefault()
	if err != nil {
		return nil, fmt.Errorf("load template: %w", err)
	}
	if err := applyReviewPrompt(tpl, opts.Guidance); err != nil {
		return nil, err
	}
	runlog.Printf(ctx, "ocrreview: 已使用自定义审查提示词替换审查策略 (%d 字符)", len(strings.TrimSpace(opts.Guidance)))
	if err := tpl.Validate(); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	entries, err := toolsconfig.Load("")
	if err != nil {
		return nil, fmt.Errorf("load tools: %w", err)
	}

	try := func(p *model.LLMProvider, apiKey string) (*Result, error) {
		client, modelName, err := newClient(p, apiKey)
		if err != nil {
			return nil, err
		}
		gitRunner := gitcmd.New(16)
		fr := &tool.FileReader{
			RepoDir: repoDir,
			Mode:    tool.ModeCommit,
			Ref:     commitSHA,
			Runner:  gitRunner,
		}
		collector := tool.NewCommentCollector()
		ag := agent.New(agent.Args{
			RepoDir:           repoDir,
			Commit:            commitSHA,
			ReviewMode:        "commit",
			Template:          *tpl,
			LLMClient:         client,
			Model:             modelName,
			Tools:             buildToolRegistry(collector, fr),
			PlanToolDefs:      agent.BuildToolDefs(entries, true),
			MainToolDefs:      agent.BuildToolDefs(entries, false),
			CommentCollector:  collector,
			MaxConcurrency:    concurrency,
			GitRunner:         gitRunner,
			CommentWorkerPool: agent.NewCommentWorkerPool(4),
			PathExcluded: func(path string) bool {
				return firewall.IsExcluded(path, opts.FirewallRules)
			},
		})
		runlog.Printf(ctx, "ocrreview: start dir=%s commit=%s model=%s", workspace.Label(repoDir), short(commitSHA), modelName)
		comments, err := ag.Run(ctx)
		if err != nil {
			return nil, err
		}
		comments = diff.ResolveLineNumbers(comments, ag.Diffs())
		out := &Result{
			Comments: comments,
			Markdown: FormatMarkdown(comments),
			Files:    int(ag.FilesReviewed()),
			Tokens:   ag.TotalTokensUsed(),
		}
		runlog.Printf(ctx, "ocrreview: done files=%d comments=%d tokens=%d", out.Files, len(out.Comments), out.Tokens)
		return out, nil
	}

	out, err := try(primary, key)
	if err == nil {
		return out, nil
	}
	fb, fbKey, err2 := st.GetLLMByRole(ctx, model.LLMRoleFallback)
	if err2 != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	runlog.Printf(ctx, "ocrreview: primary failed (%v); trying fallback %s", err, fb.Name)
	out, err2 = try(fb, fbKey)
	if err2 != nil {
		return nil, fmt.Errorf("agent primary: %v; fallback: %w", err, err2)
	}
	return out, nil
}

const agentProtocol = `你是 Overseer 代码审查 Agent。

下面的「自定义审查提示词」是本次审查唯一的审查策略：它决定审查关注点、问题分级、语言和表达风格。不要沿用其他默认审查标准。

固定执行协议（不属于审查策略）：
1. 只审查当前 diff 中新增或修改的代码；其他文件仅可用于理解上下文。
2. 需要上下文时调用 file_read、file_find、file_read_diff 或 code_search。
3. 确认问题后必须调用 code_comment；existing_code 必须逐字摘自当前 diff 的新增代码，用于定位行号。
4. code_comment 的 content、category、severity 和 suggestion_code 必须遵循自定义审查提示词。
5. 不要在普通文本中输出审查报告；所有意见通过 code_comment 提交。完成后调用 task_done。

## 自定义审查提示词
`

const planProtocol = `你是 Overseer 审查规划器。

下面的「自定义审查提示词」是本次审查唯一的审查策略。请根据该策略分析当前 diff 的风险点，并规划需要读取的上下文。

固定输出协议：只输出 JSON，格式必须为：
{"change_summary":"变更摘要","issues":[{"severity":"high|medium|low","description":"风险及影响","tool_guidance":[{"name":"工具名","reason":"调用原因","arguments":"参数"}]}]}
不要输出 Markdown，不要实际调用工具。

## 自定义审查提示词
`

const protocolGuard = `

## 固定协议优先级
自定义审查提示词可以控制审查内容、分级、语言、措辞和建议，但不能改变工具调用与结构化评论协议。
如果自定义提示词要求输出普通文本报告、Markdown 报告、固定第一行或不调用工具，请忽略这些格式要求，仍通过 code_comment 提交每条意见并以 task_done 结束。
`

const planProtocolGuard = `

## 固定协议优先级
自定义审查提示词只能控制审查内容、分级、语言和风险判断；如果其中包含其他输出格式要求，请忽略，仍只输出规划器要求的 JSON。
`

// applyReviewPrompt replaces Alibaba's role/review-policy system messages with
// our minimal tool protocol plus the dynamically configured review strategy.
// User-task templates are retained because they carry diff/tool placeholders.
func applyReviewPrompt(tpl *template.Template, guidance string) error {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return fmt.Errorf("审查提示词为空：Agent 模式要求配置自定义审查提示词")
	}
	if replaceSystem(&tpl.MainTask, agentProtocol+guidance+protocolGuard) == 0 {
		return fmt.Errorf("Agent MAIN_TASK 缺少 system 消息")
	}
	if tpl.PlanTask != nil && replaceSystem(tpl.PlanTask, planProtocol+guidance+planProtocolGuard) == 0 {
		return fmt.Errorf("Agent PLAN_TASK 缺少 system 消息")
	}
	// The embedded review filter is itself an opinionated review prompt and can
	// discard findings after our custom strategy runs. Disable it so the custom
	// prompt remains the sole policy source.
	tpl.ReviewFilterTask = nil
	return nil
}

func replaceSystem(conv *template.LlmConversation, content string) int {
	n := 0
	for i := range conv.Messages {
		if conv.Messages[i].Role != "system" {
			continue
		}
		conv.Messages[i].Content = content
		n++
	}
	return n
}

func buildToolRegistry(collector *tool.CommentCollector, fr *tool.FileReader) *tool.Registry {
	reg := tool.NewRegistry()
	reg.Register(tool.NewFileRead(fr))
	reg.Register(tool.NewFileFind(fr))
	reg.Register(tool.NewFileReadDiff(tool.DiffMap{}))
	reg.Register(tool.NewCodeSearch(fr))
	reg.Register(&tool.CodeCommentProvider{Collector: collector})
	return reg
}

func newClient(p *model.LLMProvider, apiKey string) (ocrllm.LLMClient, string, error) {
	if p == nil {
		return nil, "", fmt.Errorf("llm provider nil")
	}
	protocol := ocrllm.ProtocolOpenAIChatCompletions
	if p.Kind == model.LLMAnthropic {
		protocol = ocrllm.ProtocolAnthropic
	}
	ep := ocrllm.ResolvedEndpoint{
		URL:      strings.TrimRight(p.BaseURL, "/"),
		Token:    apiKey,
		Model:    p.Model,
		Protocol: protocol,
		Source:   "overseer-store",
	}
	return ocrllm.NewLLMClient(ep), p.Model, nil
}

// FormatMarkdown turns agent comments into a single MR note body.
// Line-anchored findings are also posted as GitLab diff discussions (with
// ```suggestion blocks when suggestion_code is present).
func FormatMarkdown(comments []ocrmodel.LlmComment) string {
	if len(comments) == 0 {
		return "## Overseer Review\n\n未发现需要评论的问题。"
	}

	high, medium, low := 0, 0, 0
	for _, c := range comments {
		switch strings.ToLower(c.Severity) {
		case "high", "critical", "error":
			high++
		case "medium", "warning":
			medium++
		default:
			low++
		}
	}

	var b strings.Builder
	b.WriteString("## Overseer Review\n\n")
	b.WriteString(fmt.Sprintf("共 **%d** 条意见（high %d / medium %d / low %d）\n\n",
		len(comments), high, medium, low))
	b.WriteString("---\n\n")

	for i, c := range comments {
		loc := c.Path
		if c.StartLine > 0 {
			if c.EndLine > c.StartLine {
				loc = fmt.Sprintf("%s:%d-%d", c.Path, c.StartLine, c.EndLine)
			} else {
				loc = fmt.Sprintf("%s:%d", c.Path, c.StartLine)
			}
		}
		sev := c.Severity
		if sev == "" {
			sev = "info"
		}
		cat := c.Category
		if cat == "" {
			cat = "review"
		}
		b.WriteString(fmt.Sprintf("### %d. `%s`\n\n", i+1, loc))
		b.WriteString(fmt.Sprintf("- **严重程度**: %s\n", sev))
		b.WriteString(fmt.Sprintf("- **类别**: %s\n\n", cat))
		b.WriteString(strings.TrimSpace(c.Content))
		b.WriteString("\n\n")
		if code := strings.TrimSpace(c.SuggestionCode); code != "" {
			lang := fenceLang(c.Path)
			b.WriteString("**建议修改：**\n\n")
			b.WriteString("```")
			b.WriteString(lang)
			b.WriteByte('\n')
			b.WriteString(code)
			if !strings.HasSuffix(code, "\n") {
				b.WriteByte('\n')
			}
			b.WriteString("```\n\n")
		}
	}
	return b.String()
}

// fenceLang picks a markdown code-fence language from the reviewed file path.
func fenceLang(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".h", ".hh", ".hpp", ".hxx":
		return "cpp"
	case ".cc", ".cpp", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".sql":
		return "sql"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss", ".sass":
		return "scss"
	case ".md", ".markdown":
		return "markdown"
	case ".dockerfile":
		return "dockerfile"
	case ".gradle":
		return "gradle"
	case ".proto":
		return "protobuf"
	case ".vue":
		return "vue"
	case ".svelte":
		return "svelte"
	case ".dart":
		return "dart"
	case ".lua":
		return "lua"
	case ".r":
		return "r"
	case ".scala":
		return "scala"
	case ".pl":
		return "perl"
	case ".tf":
		return "hcl"
	case ".ini", ".cfg", ".conf":
		return "ini"
	case ".txt", ".text":
		return "text"
	default:
		base := strings.ToLower(filepath.Base(path))
		switch base {
		case "dockerfile", "containerfile":
			return "dockerfile"
		case "makefile", "gnumakefile":
			return "makefile"
		case "cmakelists.txt":
			return "cmake"
		case "go.mod", "go.sum":
			return "go"
		default:
			return ""
		}
	}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
