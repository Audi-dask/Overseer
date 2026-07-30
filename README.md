# Overseer

**[English](#english) | [中文](#中文)**

Self-hosted code review for merge requests and push events — webhook-driven, pluggable LLMs, agent-based review, GitLab feedback, and notifications.

**Web** · [GitHub](https://github.com/Audi-dask/Overseer) · [License (AGPL-3.0)](LICENSE)

---

<a id="english"></a>

## English

Overseer connects to GitLab (more VCS providers planned), watches MR or Push events, runs an agent review against a shallow checkout, posts comments back to the platform, and optionally pushes summary cards to Feishu or generic webhooks.

### Important

- Set a **fixed** **`MASTER_KEY`** in `.env` before first run. It encrypts VCS tokens and LLM API keys. Changing it invalidates stored credentials — re-enter them in the admin UI.
- Set the **callback Base URL** in the admin UI to the public address GitLab uses (e.g. `http://review.example.com`, or `https://…` if TLS terminates at your load balancer). Webhooks use `{base}/hooks/{instance_id}`.
- If the container registry is private, run `docker login` against your registry before `docker compose pull`.

### Features

| Area | Description |
| --- | --- |
| VCS | GitLab today; GitHub / Gitea planned |
| Triggers | Per repo: **MR** or **Push** (mutually exclusive) |
| Review | Agent with file read / code search; custom prompts; path firewall |
| Feedback | MR notes + inline discussions; Push → commit comments |
| Notify | Feishu interactive cards; generic JSON webhooks |
| Ops | Review history, per-run logs, manual re-run |

### Deploy (Docker Compose)

1. Get `docker-compose.yml` and `.env.example` from this repository (clone or copy the two files into a deploy directory).

2. Configure environment:

```bash
cp .env.example .env
# Generate a fixed key: openssl rand -hex 32
# Edit .env — at minimum set MASTER_KEY; optionally PORT and ADMIN_PASSWORD
```

3. (Optional) Override the image in `.env`:

```bash
# OVERSEER_IMAGE=ccr.ccs.tencentyun.com/audi-dask/overseer:20260730-fix1
```

4. Start:

```bash
docker compose pull
docker compose up -d
docker compose logs -f overseer
```

5. Open **`http://<server-ip>:8080/`** on your LAN/VPN for first-time admin setup (see [Public exposure](#public-exposure-en)).

Data persists in the **`overseer-data`** volume (`/data/overseer.db`, workspaces, review logs).

**Upgrade**

```bash
docker compose pull
docker compose up -d
```

### Configuration

| Variable | Notes |
| --- | --- |
| `MASTER_KEY` | Required; keep stable across restarts |
| `PORT` | Host port mapped to container `8080` (default **8080**) |
| `ADMIN_PASSWORD` | Optional; skips first-time setup when set |
| `OVERSEER_IMAGE` | Container image (see `docker-compose.yml` default) |
| Callback Base URL | Admin → Settings; must be public to GitLab |
| Webhook secret | Validated on incoming hooks (not the admin password) |

Inside the container: `DB_PATH=/data/overseer.db`, `WORKSPACE_DIR=/data/workspaces`, `REVIEW_LOG_DIR=/data/reviewlogs`.

### Public exposure {#public-exposure-en}

Expose **only** GitLab webhook callbacks on the public internet. Do **not** put `/api/auth/login`, the admin UI, or other `/api/*` routes on a public hostname.

| Access | URL | Purpose |
| --- | --- | --- |
| Public | `http://review.example.com/hooks/{instance_id}` | GitLab `POST` webhooks only (nginx **:80** on origin; **443** at LB if used) |
| Internal | `http://10.x.x.x:8080/` (VPN / LAN) | Login, settings, review UI |

Set **Callback Base URL** to the URL GitLab reaches (often `https://review.example.com` when the LB terminates TLS). Open the admin UI at **`http://<server-ip>:8080/`** on your LAN/VPN only.

Nginx on the origin listens on **port 80** and reverse-proxies to `http://127.0.0.1:8080`. Change `.env` `PORT` only if **8080** is already taken on that host.

Example nginx config: [`deploy/nginx/overseer.conf.example`](deploy/nginx/overseer.conf.example)

```nginx
upstream overseer { server 127.0.0.1:8080; }

server {
    listen 80;
    server_name review.example.com;
    location ~ ^/hooks/[A-Za-z0-9_-]+$ {
        limit_except POST { deny all; }
        proxy_pass http://overseer;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location / { return 404; }
}
```

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

- 在 `.env` 中设置**固定**的 **`MASTER_KEY`** 后再启动。用于加密 VCS Token 与 LLM Key；变更后已存凭证失效，需在管理台重新填写。
- 在管理台配置 **回调 Base URL** 为 GitLab 实际访问的公网地址（源站 nginx 为 **80** 时可填 `http://…`；若 LB 对外提供 **HTTPS**，填 `https://…`）。钩子路径为 `{base}/hooks/{instance_id}`。
- 若镜像仓库为私有，请先 `docker login` 对应 registry，再执行 `docker compose pull`。

### 功能概览

| 模块 | 说明 |
| --- | --- |
| VCS | 已支持 GitLab；GitHub / Gitea 规划中 |
| 触发 | 每仓库二选一：**MR** 或 **Push** |
| 审查 | Agent 读文件 / 搜代码；可配置提示词；路径防火墙 |
| 回填 | MR 总评 + 行内 discussion；Push → Commit 评论 |
| 通知 | 飞书 interactive 卡片；通用 Webhook JSON |
| 运维 | 审查记录、单次运行日志、手动重跑 |

### 部署（Docker Compose）

1. 从本仓库获取 `docker-compose.yml` 与 `.env.example`（克隆仓库，或仅复制这两个文件到部署目录）。

2. 配置环境变量：

```bash
cp .env.example .env
# 生成固定密钥：openssl rand -hex 32
# 编辑 .env — 至少设置 MASTER_KEY；可选 PORT、ADMIN_PASSWORD
```

3. （可选）在 `.env` 中覆盖镜像：

```bash
# OVERSEER_IMAGE=ccr.ccs.tencentyun.com/audi-dask/overseer:20260730-fix1
```

4. 启动：

```bash
docker compose pull
docker compose up -d
docker compose logs -f overseer
```

5. 在内网/VPN 打开 **`http://<服务器IP>:8080/`** 完成首次设置（见[公网暴露](#public-exposure-zh)）。

数据保存在 **`overseer-data`** 卷（数据库、工作区、审查日志）。

**升级**

```bash
docker compose pull
docker compose up -d
```

### 配置要点

| 变量 | 说明 |
| --- | --- |
| `MASTER_KEY` | 必填；重启后保持不变 |
| `PORT` | 宿主机映射端口，默认 **8080**（对应容器内 `8080`） |
| `ADMIN_PASSWORD` | 可选；设置后跳过首次初始化 |
| `OVERSEER_IMAGE` | 容器镜像（默认值见 `docker-compose.yml`） |
| 回调 Base URL | 管理台 → 服务设置；须对 GitLab 可达 |
| 钩子 Secret | 校验入站 Webhook（非管理台登录密码） |

容器内路径：`DB_PATH=/data/overseer.db`，`WORKSPACE_DIR=/data/workspaces`，`REVIEW_LOG_DIR=/data/reviewlogs`。

### 公网暴露 {#public-exposure-zh}

公网**只暴露 GitLab 回调** `/hooks/{instance_id}`，不要把 `/api/auth/login`、管理台 UI 或其它 `/api/*` 放到公网域名上。

| 访问面 | 地址 | 用途 |
| --- | --- | --- |
| 公网 | `http://review.example.com/hooks/{instance_id}` | 仅 GitLab Webhook（源站 nginx **:80**；**443** 一般在 LB） |
| 内网 | `http://10.x.x.x:8080/`（VPN / 办公网） | 登录、配置、审查记录 |

**回调 Base URL** 填 GitLab 能访问到的对外 URL（LB 终结 TLS 时通常为 `https://review.example.com`）。管理台仅在 **内网/VPN** 通过 **`http://<服务器IP>:8080/`** 访问。

源站 nginx 监听 **80**，反代到 `http://127.0.0.1:8080`。仅当宿主机 **8080** 已被占用时，再在 `.env` 里改 `PORT`。

完整示例：[`deploy/nginx/overseer.conf.example`](deploy/nginx/overseer.conf.example)

```nginx
upstream overseer { server 127.0.0.1:8080; }

server {
    listen 80;
    server_name review.example.com;
    location ~ ^/hooks/[A-Za-z0-9_-]+$ {
        limit_except POST { deny all; }
        proxy_pass http://overseer;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location / { return 404; }
}
```

### 许可

本项目以 **[GNU Affero General Public License v3.0](LICENSE)**（AGPL-3.0）发布。

### 参考

- [alibaba/open-code-review](https://github.com/alibaba/open-code-review) — Agent 工作流参考
- [GitLab Webhooks](https://docs.gitlab.com/ee/user/project/integrations/webhooks.html)
- [飞书自定义机器人](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot)
