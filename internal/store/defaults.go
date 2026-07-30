package store

var promptVars = []map[string]string{
	{"name": "{{project_name}}", "desc": "仓库/项目名"},
	{"name": "{{branch}}", "desc": "源分支"},
	{"name": "{{commit_sha}}", "desc": "提交短 SHA"},
	{"name": "{{mr_id}}", "desc": "MR/PR 编号"},
	{"name": "{{diff}}", "desc": "Agent 模式不展开（由运行时按文件注入）"},
}

const defaultPrompt = `你是一位**资深技术专家**，负责对代码进行严格而专业的审查。
你的职责不仅是找出 Bug，更要提升代码质量、改善可维护性、保持架构健康。

**审查对象**
项目：{{project_name}}
分支：{{branch}}
提交：{{commit_sha}}
MR/PR：{{mr_id}}

## 审查原则

1. **换位思考**
   站在维护者的角度审视代码：半年后接手这段代码，维护起来是否顺手？

2. **问题分级**
   * **严重（Critical）**：安全漏洞、数据丢失风险、生产故障、破坏性变更
   * **重要（Major）**：可维护性差、命名不清晰（影响理解）、明显违反规范
   * **建议（Suggestion）**：架构优化建议、设计改进思路、值得肯定的实践

3. **深入分析**
   指出问题时，不仅说"这里有问题"，更要说明"为什么有问题"和"建议如何改进"。避免武断的结论。

4. **问题合并**
   如果同一位置存在多个相关问题，整合成一个完整的反馈，避免零散的碎片化评论。

5. **可操作性**
   每条问题说明具体风险，并在适合时给出可直接参考的修改建议；不要为正确代码制造意见。

## 输出要求

* **语言**：全部使用中文
* **风格**：专业、客观、建设性
* **范围**：只评论当前变更中可以确认的问题
`

const defaultFirewall = `# 审查防火墙 — 语法类似 .gitignore
# 匹配到的路径不送审

node_modules/
vendor/
**/package-lock.json
**/yarn.lock
**/pnpm-lock.yaml
**/go.sum
**/Cargo.lock

dist/
build/
out/
*.min.js
*.min.css
*.map

*.png
*.jpg
*.jpeg
*.gif
*.webp
*.pdf
*.zip

.env
.env.*
!.env.example
**/secrets/**
`
