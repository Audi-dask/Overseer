# Overseer

**[English](#english) | [中文](#中文)**

Self-hosted code review for merge requests and push events — webhook-driven, pluggable LLMs, agent-based review, GitLab feedback, and notifications.

**Web** · [GitHub](https://github.com/Audi-dask/Overseer) · [License (AGPL-3.0)](LICENSE)

---

<a id="english"></a>

## English

Overseer connects to GitLab (more VCS providers planned), watches MR or Push events, runs an agent review against a shallow checkout, posts comments back to the platform, and optionally pushes summary cards to Feishu or generic webhooks.

### Important

- Set **`MASTER_KEY`** before first run. It encrypts VCS tokens and LLM API keys in SQLite. Changing it invalidates stored credentials — re-enter them in the admin UI.
- Set the **callback Base URL** to an address GitLab can reach (e.g. `https://review.example.com`). Webhooks use `{base}/hooks/{instance_id}`.

### Features

| Area | Description |
| --- | --- |
| VCS | GitLab today; GitHub / Gitea planned |
| Triggers | Per repo: **MR** or **Push** (mutually exclusive) |
| Review | Agent with file read / code search; custom prompts; path firewall |
| Feedback | MR notes + inline discussions; Push → commit comments |
| Notify | Feishu interactive cards; generic JSON webhooks |
| Ops | Review history, per-run logs, manual re-run |

### Quick start

```bash
git clone https://github.com/Audi-dask/Overseer.git
cd Overseer

export MASTER_KEY="$(openssl rand -hex 32)"
# optional: export ADMIN_PASSWORD=... PORT=8080

go run ./cmd/server
```

Open `http://localhost:8080` and complete the first-time admin setup.

### Docker

```bash
cp .env.example .env
# edit MASTER_KEY in .env

docker compose up -d --build
```

Data persists in the `overseer-data` volume (`/data/overseer.db`, workspaces, review logs).

### Build

Requires **Go 1.25+** and **git** (for agent checkout).

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o overseer ./cmd/server
```

The [Dockerfile](Dockerfile) uses the same flags. UI assets are embedded; restart the binary after editing `internal/ui/dist/`.

### Configuration

| Setting | Notes |
| --- | --- |
| `MASTER_KEY` | Required in production |
| `DB_PATH` | Default `data/overseer.db` |
| `WORKSPACE_DIR` | Shallow clone cache (default `data/workspaces`) |
| `REVIEW_LOG_DIR` | Per-review logs (default `data/reviewlogs`) |
| Callback Base URL | Admin → Settings; must be public to GitLab |
| Webhook secret | Validated on incoming hooks (not the admin password) |

### License

This project is licensed under the **[GNU Affero General Public License v3.0](LICENSE)** (AGPL-3.0).

### References

- [alibaba/open-code-review](https://github.com/alibaba/open-code-review) — agent workflow reference
- [GitLab Webhooks](https://docs.gitlab.com/ee/user/project/integrations/webhooks.html)
- [Feishu bot webhook](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)

---

<a id="中文"></a>

## 中文

Overseer 是自托管的代码审查服务：对接 GitLab（更多 VCS 规划中），监听 MR 或 Push 事件，在浅克隆工作区运行 Agent 审查，将结果回填到平台，并可向飞书或通用 Webhook 推送摘要卡片。

**Web** · [GitHub](https://github.com/Audi-dask/Overseer) · [License (AGPL-3.0)](LICENSE)

### 重要说明

- 首次运行前必须设置 **`MASTER_KEY`**，用于加密 SQLite 中的 VCS Token 与 LLM Key。变更后已存凭证失效，需在管理台重新填写。
- **回调 Base URL** 必须是 GitLab 能访问的公网地址（如 `https://review.example.com`）。钩子地址为 `{base}/hooks/{instance_id}`。

### 功能概览

| 模块 | 说明 |
| --- | --- |
| VCS | 已支持 GitLab；GitHub / Gitea 规划中 |
| 触发 | 每仓库二选一：**MR** 或 **Push** |
| 审查 | Agent 读文件 / 搜代码；可配置提示词；路径防火墙 |
| 回填 | MR 总评 + 行内 discussion；Push → Commit 评论 |
| 通知 | 飞书 interactive 卡片；通用 Webhook JSON |
| 运维 | 审查记录、单次运行日志、手动重跑 |

### 快速启动

```bash
git clone https://github.com/Audi-dask/Overseer.git
cd Overseer

export MASTER_KEY="$(openssl rand -hex 32)"
# 可选：export ADMIN_PASSWORD=... PORT=8080

go run ./cmd/server
```

浏览器打开 `http://localhost:8080`，完成首次管理员设置。

### Docker 部署

```bash
cp .env.example .env
# 编辑 .env 中的 MASTER_KEY

docker compose up -d --build
```

数据保存在 `overseer-data` 卷（数据库、工作区、审查日志）。

### 构建

需要 **Go 1.25+** 与 **git**（Agent checkout 依赖）。

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o overseer ./cmd/server
```

[Dockerfile](Dockerfile) 使用相同参数。UI 为 embed 静态资源，修改 `internal/ui/dist/` 后需重启进程。

### 配置要点

| 项 | 说明 |
| --- | --- |
| `MASTER_KEY` | 生产环境必填 |
| `DB_PATH` | 默认 `data/overseer.db` |
| `WORKSPACE_DIR` | 浅克隆缓存（默认 `data/workspaces`） |
| `REVIEW_LOG_DIR` | 单次审查日志（默认 `data/reviewlogs`） |
| 回调 Base URL | 管理台 → 服务设置；须对 GitLab 可达 |
| 钩子 Secret | 校验入站 Webhook（非管理台登录密码） |

### 许可

本项目以 **[GNU Affero General Public License v3.0](LICENSE)**（AGPL-3.0）发布。

### 参考

- [alibaba/open-code-review](https://github.com/alibaba/open-code-review) — Agent 工作流参考
- [GitLab Webhooks](https://docs.gitlab.com/ee/user/project/integrations/webhooks.html)
- [飞书自定义机器人](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)
