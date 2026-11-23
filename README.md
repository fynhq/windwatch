<a id="english"></a>

# 听风者 (WindWatch)

**Lightweight, self-hosted monitoring in a single Go binary.**

WindWatch monitors HTTP(S) endpoints, TCP ports, Docker containers, and external heartbeats, and alerts through Telegram or webhooks. It runs as one binary with SQLite and server-rendered pages; no external services or frontend build chain.

English | [中文](#chinese)

## Features

- **HTTP(S)** — method, expected status, keyword, redirects, timeout, optional certificate expiry check, and per-monitor outbound proxy.
- **TCP** — `host:port` reachability.
- **Docker** — container running and, optionally, healthy.
- **Push / heartbeat** — secret URL for scripts or devices; missing heartbeats after the interval plus grace period mark the monitor down. Tokens can be regenerated.
- **Notifications** — multiple Telegram bots and Generic, DingTalk, WeCom, or Feishu webhooks, each with its own language, proxy, and test action; per-monitor selection, repeat reminders, retries, deduplication, and a delivery log.
- **Dashboard and history** — search, status filters, 24h/7d/30d/90d uptime, latency, recent checks, and timezone-aware charts.
- **Multi-user and public page** — administrator and read-only viewer roles; optional login-free `/status` page for selected monitors; Simplified Chinese and English; responsive UI.
- **Small footprint** — about 30 MB idle memory, embedded assets, SQLite storage; up to 500 monitors, with performance targets assuming about 200.

## Quick start

Requires Go 1.27.4 or newer.

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

Open [http://localhost:8080/setup](http://localhost:8080/setup) to create the administrator. There are no default credentials; setup closes after the first account, and later accounts are created under **Users**. `serve` is the default command.

Tagged [releases](https://github.com/fynhq/windwatch/releases) provide Linux binaries and the GHCR image; use `make dist` for a local release build.

## Deployment

All state lives in one data directory; monitors, proxies, notifications, and users are managed in the web UI.

### Docker Compose

Copy the tracked [compose.example.yaml](compose.example.yaml) to `compose.yaml`, create the data directory, and start:

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
sudo chown 65532:65532 data && sudo chmod 700 data   # Linux only
docker compose up -d
```

Open [http://localhost:8080/setup](http://localhost:8080/setup). Data and daily logs stay in `./data`; view logs with `docker compose logs -f windwatch`.

The example uses host networking on Linux so monitors can reach host services such as a local proxy. On Docker Desktop, skip `chown`, comment out `network_mode: host`, uncomment the `ports` mapping, and use `host.docker.internal` instead of `127.0.0.1`. For multi-second HTTPS latency there, enable the commented `GODEBUG=tlsmlkem=0`; certificate verification is unchanged.

To upgrade, back up `./data`, then run `docker compose pull && docker compose up -d`. Migrations run at startup and are not reversible; to roll back, restore the backup and pin `WINDWATCH_IMAGE` to the previous tag or digest. Published images support linux/amd64 and linux/arm64; release images are signed with cosign keyless (cosign v3+ is required for verification) and include SBOM/provenance attestations. Docker socket, hardening, and other options are documented in [compose.example.yaml](compose.example.yaml); mounting the socket is required for Docker monitors and grants significant host control.

### Single binary (Linux amd64 and arm64)

Standalone binaries are provided only for Linux amd64 and arm64; other platforms should use the container image. Releases publish `windwatch-linux-amd64-vX.Y.Z.tar.gz`, `windwatch-linux-arm64-vX.Y.Z.tar.gz`, and `SHA256SUMS`. Verify with `sha256sum -c --strict SHA256SUMS`, extract the archive for your architecture, and run `windwatch version`.

`make dist` runs formatting, vet, and tests, then writes both archives and `dist/SHA256SUMS`; each archive contains the static `windwatch` binary and `THIRD_PARTY_NOTICES.md`. HTTPS checks use the system CA bundle, so minimal hosts need `ca-certificates`.

Install with systemd (replace `X.Y.Z` with the release version):

```bash
tar -xzf windwatch-linux-amd64-vX.Y.Z.tar.gz -C /tmp
sudo install -m 0755 /tmp/windwatch /usr/local/bin/windwatch
sudo useradd --system --user-group --home-dir /var/lib/windwatch --shell /usr/sbin/nologin windwatch
sudo install -d -o windwatch -g windwatch -m 0750 /var/lib/windwatch
sudo install -m 0644 deploy/windwatch.service /etc/systemd/system/windwatch.service
sudo systemctl daemon-reload
sudo systemctl enable --now windwatch
```

The unit stores data in `/var/lib/windwatch` and listens on `:8080`; its comments cover trusted proxies and Docker monitoring. Use `journalctl -u windwatch -f` for logs. To upgrade, back up the data directory, stop the service, replace the binary, and start it again; confirm with `windwatch version`. Never point two instances at one data directory.

## Configuration

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | Persistent data directory |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | Daily logs, kept for 30 days |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | unset | Proxy IPs or CIDRs whose `X-Forwarded-*` headers are trusted |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | Endpoint used by `windwatch healthcheck` |
| — | `DOCKER_HOST` | Linux Unix socket | Docker Engine endpoint; `unix://` and `tcp://` are supported |
| — | `GOMEMLIMIT` | unset | Go soft memory limit; useful in a constrained container |

Business settings (users, public page, notification methods, outbound proxies, and monitors) are managed in the web UI and stored in SQLite. There is no configuration file.

## Monitoring

Each monitor has an interval, timeout, failure threshold, and recovery threshold. Defaults: 60 seconds, 10 seconds, 2 failures before `DOWN`, and 1 success before recovery. Intervals range from 20 seconds to 24 hours; timeouts from 1 second to 5 minutes and must be shorter than the interval. Before an active check marks a monitor `DOWN`, WindWatch rechecks once after a short delay; a successful recheck prevents the transition. Push monitors cannot be rechecked.

**HTTP(S)** supports `GET` and `HEAD`, an expected status code or range (default `200-299`), an optional keyword, redirect control, and TLS verification (enabled by default). An optional check warns when the TLS certificate (default 14 days) expires. Skip TLS verification only for internal services with self-signed certificates.

**TCP** checks that `host:port` accepts a connection; it does not verify a proxy's egress path. Inside a container, `127.0.0.1` is the container itself.

**Docker** checks a container by name or ID and can require healthy status; `starting` is not a failure. `DOCKER_HOST` supports Unix sockets and `tcp://`; Windows deployments should use the container image or a `tcp://` endpoint. Checks use read-only inspect calls, always connect directly, and ignore proxies. For Docker monitoring in the example container, uncomment the socket and `group_add` entries and set `DOCKER_GID`.

**Push / heartbeat** provides a secret URL, shown on the monitor page:

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

`GET` and `POST` are accepted; the token is the only credential, valid heartbeats are limited to one per second per token, and unauthenticated floods are capped per client IP. The monitor stays `PENDING` until the first heartbeat; there is no initial timeout alert. Keep the URL out of application and reverse-proxy logs.

**Outbound proxies** — HTTP and SOCKS5 proxies with unique names and optional credentials are configured under **Settings → Outbound proxies** and selected per HTTP/TCP monitor and per notification method. A failed proxy fails the check; there is no direct fallback. Docker and Push checks ignore proxies.

## Notifications

WindWatch notifies on `DOWN`, recovery, and certificate expiry warnings. Add methods under **Settings → Notifications**; names must be unique. Telegram uses a bot token and admin chat IDs. Webhooks support Generic, DingTalk, WeCom, and Feishu; Generic sends versioned JSON with event, monitor, message, text, and timestamp, and can be signed; DingTalk and Feishu support their platform signatures. Each method has its own optional proxy, language, and **Test** action. Each monitor selects methods and repeat reminders; turning notifications off keeps the selection but pauses delivery.

Delivery is attempted once immediately; retryable failures are queued and retried with backoff for up to about one hour. State alerts keep their order per monitor, method, and target; duplicate events are deduplicated; non-retryable errors fail immediately. **Settings → Delivery log** shows the last 100 attempts for 365 days. Notification language is independent of the UI language.

## Operations

**Data and backup** — the data directory contains `windwatch.db` (with WAL and SHM files), `secret.key`, and daily logs under `logs/`. `secret.key` encrypts proxy passwords, Push tokens, and notification credentials; back it up with the database, because lost keys cannot be recovered. Stop WindWatch and copy the whole directory. Migrations run at startup; back up before upgrading, and automatic downgrades are not supported. Raw checks are kept for 7 days; 30-minute rollups, events, and delivery logs for 365 days.

**Timezone** — choose an IANA timezone under **Settings → General** (default `UTC`). It controls displayed timestamps, notifications, and daily chart boundaries; storage remains UTC.

**Accounts** — the first account created at `/setup` is an administrator. Administrators manage monitors, proxies, notifications, users, and the public page. Viewers can inspect dashboards, details, and events but cannot change configuration or see Push tokens, notification channel names, or proxy names. Keep at least one active administrator; disabling an account or resetting its password invalidates its sessions. Web password reset keeps a disabled account disabled; the offline recovery command re-enables it. There is no self-registration, email invitation, or OIDC.

**Public status page** — enable it under **Settings → Public page**, set a title and description, and select monitors. `/status` requires no login, refreshes every 30 seconds, shows only names, types, statuses, and 24h/7d/30d/90d uptime, and is marked `noindex, nofollow`. Disabling it returns 404.

**Password recovery** — if no administrator can sign in, stop the service and run:

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

The command prompts without echoing; `--password-stdin` supports scripts, and `--username NAME` selects an account when several exist. It re-enables the account, replaces the password hash, and invalidates all sessions.

**Reverse proxies and security** — the built-in server speaks HTTP only; terminate TLS with Caddy, Nginx, or Traefik. WindWatch trusts `X-Forwarded-*` only when the direct peer is listed in `WINDWATCH_TRUSTED_PROXIES`; list only proxies you control, never client networks. The effective client address is used for login rate limiting, and the forwarded scheme and host for Secure cookies and the Push URL. Run one instance per data directory, under an unprivileged user with write access only to that directory, and keep the directory and `secret.key` unreadable by others. `/healthz` reports process and database health plus the build version; it does not probe the scheduler. Do not expose the admin panel anonymously.

## Development

```bash
make check
make race
make build
```

`make check` runs formatting, vet, and tests; `make vuln` runs the pinned `govulncheck`; `make dist` and `make docker-build` wrap deployment builds; `make version` prints the current version. The code lives in `cmd/windwatch` and `internal/`; tests use local fakes for HTTP, TCP, Docker, Telegram, and proxies. SQLite uses the pure-Go `modernc.org/sqlite` driver, so `CGO_ENABLED=0` builds are supported.

## License

Released under the MIT License. See [LICENSE](LICENSE).

---

<a id="chinese"></a>

# 听风者 (WindWatch)

**单二进制、轻量、自托管的监控面板。**

WindWatch（听风者）监控 HTTP(S) 地址、TCP 端口、Docker 容器和外部心跳，并通过 Telegram 或 Webhook 告警。一个二进制、一个 SQLite 数据库、服务端渲染页面，不依赖外部服务和前端构建链。

[English](#english) | 中文

## 功能

- **HTTP(S)**：支持请求方法、期望状态码、响应关键字、跳转、超时、可选的证书到期检查和按监控选择的出站代理。
- **TCP**：检查 `host:port` 是否可连接。
- **Docker**：检查容器是否运行，并可要求 healthy。
- **Push / 心跳**：为脚本或设备提供秘密 URL；超过“间隔 + 宽限期”没有心跳时判定异常，Token 可重新生成。
- **通知**：多个 Telegram Bot，以及通用、钉钉、企微、飞书 Webhook；每种方式独立配置语言、代理和测试按钮。支持按监控选择方式、重复提醒、重试、去重和发送记录。
- **仪表盘与历史**：搜索、状态筛选、24h/7d/30d/90d 可用率、延迟、最近检查和按时区归日的图表。
- **多用户与公开页**：管理员和只读观察者角色；可选的免登录 `/status` 页面，只展示选定监控；支持简体中文、英文和响应式布局。
- **资源占用小**：空载内存约 30 MB，模板和静态资源内嵌，使用 SQLite；单实例最多 500 个监控，性能目标以约 200 个为基准。

## 快速开始

需要 Go 1.27.4 或更高版本。

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup) 创建管理员。系统没有默认密码，创建第一个账号后初始化页面关闭，后续账号在**用户**页创建；`serve` 是默认子命令。

正式发布在 [GitHub Releases](https://github.com/fynhq/windwatch/releases) 提供 Linux 二进制和 GHCR 镜像；本地发布构建使用 `make dist`。

## 部署

所有状态都保存在同一个数据目录；监控、代理、通知和用户都在 Web 界面中管理。

### Docker Compose

把仓库中的 [compose.example.yaml](compose.example.yaml) 复制成本地 `compose.yaml`，创建数据目录后启动：

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
sudo chown 65532:65532 data && sudo chmod 700 data   # 仅 Linux
docker compose up -d
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup)。数据和日志保存在 `./data`；用 `docker compose logs -f windwatch` 查看日志。

示例在 Linux 上默认使用 host 网络，便于访问本地代理等宿主机服务。Docker Desktop 上跳过 `chown`，注释掉 `network_mode: host`，取消 `ports` 映射注释，并把 `127.0.0.1` 换成 `host.docker.internal`；如果 HTTPS 检查出现数秒延迟，可启用已注释的 `GODEBUG=tlsmlkem=0`，证书校验不变。

升级时先备份 `./data`，再执行 `docker compose pull && docker compose up -d`。迁移在启动时执行且不可自动回退；回滚需恢复备份，并把 `WINDWATCH_IMAGE` 固定到升级前的版本标签或 digest。官方镜像支持 linux/amd64 和 linux/arm64；发布镜像使用 cosign keyless 签名（验证需 cosign v3+），并带 SBOM/provenance 证明。Docker socket、加固和其他选项见 [compose.example.yaml](compose.example.yaml)；挂载 socket 是 Docker 监控的前提，也会授予较高的宿主机控制权限。

### 单文件二进制（Linux amd64 和 arm64）

单文件只提供 Linux amd64 和 arm64 版本，其他平台请使用容器镜像。发布包包含 `windwatch-linux-amd64-vX.Y.Z.tar.gz`、`windwatch-linux-arm64-vX.Y.Z.tar.gz` 和 `SHA256SUMS`；用 `sha256sum -c --strict SHA256SUMS` 校验，解压对应架构后用 `windwatch version` 查看版本。

`make dist` 会执行格式检查、vet 和测试，然后生成两个压缩包和 `dist/SHA256SUMS`；每个压缩包内含静态链接的 `windwatch` 和 `THIRD_PARTY_NOTICES.md`。HTTPS 检查使用系统 CA 证书，最小化系统需要安装 `ca-certificates`。

在 systemd 主机上安装（把 `X.Y.Z` 换成实际版本）：

```bash
tar -xzf windwatch-linux-amd64-vX.Y.Z.tar.gz -C /tmp
sudo install -m 0755 /tmp/windwatch /usr/local/bin/windwatch
sudo useradd --system --user-group --home-dir /var/lib/windwatch --shell /usr/sbin/nologin windwatch
sudo install -d -o windwatch -g windwatch -m 0750 /var/lib/windwatch
sudo install -m 0644 deploy/windwatch.service /etc/systemd/system/windwatch.service
sudo systemctl daemon-reload
sudo systemctl enable --now windwatch
```

该 unit 使用 `/var/lib/windwatch` 保存数据并监听 `:8080`，可信代理和 Docker 监控选项见 unit 注释。用 `journalctl -u windwatch -f` 查看日志。升级时备份数据目录、停止服务、替换二进制后重启，并用 `windwatch version` 确认；不要让两个实例共用同一个数据目录。

## 配置

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP 监听地址 |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | 持久化数据目录 |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | 每日日志，保留 30 天 |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | 不设置 | 可信代理 IP 或 CIDR，只有这些来源的 `X-Forwarded-*` 生效 |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | `windwatch healthcheck` 请求的地址 |
| — | `DOCKER_HOST` | Linux 为 Unix socket | Docker Engine 端点；支持 `unix://` 和 `tcp://` |
| — | `GOMEMLIMIT` | 不设置 | Go 软内存上限；内存受限容器中有用 |

业务配置（用户、公开页、通知方式、出站代理和监控）都在 Web 界面中管理并保存在 SQLite 中，不使用配置文件。

## 监控

每个监控都有检查间隔、超时、失败阈值和恢复阈值。默认值为 60 秒、10 秒、连续失败 2 次判定 `DOWN`、连续成功 1 次恢复。间隔范围为 20 秒到 24 小时；超时范围为 1 秒到 5 分钟，且必须小于间隔。主动检查在判定 `DOWN` 前会自动复核一次，复核成功则不判定 `DOWN`；Push 监控无法复核。

**HTTP(S)** 支持 `GET` 和 `HEAD`、期望状态码或范围（默认 `200-299`）、响应关键字、跳转控制，以及 TLS 校验（默认开启）。可选的证书检查会在证书临期（默认提前 14 天）时告警；只有自签名内网服务才建议跳过 TLS 校验。

**TCP** 检查 `host:port` 是否可连接，只验证端口可达，不验证代理出口。容器内的 `127.0.0.1` 指容器自身。

**Docker** 按容器名称或 ID 检查，并可要求 healthy；`starting` 不视为失败。`DOCKER_HOST` 支持 Unix socket 和 `tcp://`；Windows 部署请使用容器镜像或 `tcp://` 端点。检查只调用只读 inspect API，始终直连，不使用代理。在示例容器中启用 Docker 监控时，取消 socket 和 `group_add` 注释，并设置 `DOCKER_GID`。

**Push / 心跳** 会生成一个秘密 URL，显示在监控详情页：

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

支持 `GET` 和 `POST`；Token 是唯一凭据，同一 Token 每秒最多一次有效心跳，未认证请求按客户端 IP 限流。首次心跳前监控保持 `PENDING`，不会产生首次超时告警；心跳 URL 本身是密钥，不要写入应用或反向代理日志。

**出站代理**：在**设置 → 出站代理**中配置 HTTP 或 SOCKS5 代理，名称唯一，可带凭据；HTTP/TCP 监控和通知方式可分别选择代理。代理不可用时检查直接失败，不会回退直连；Docker 和 Push 不使用代理。

## 通知

WindWatch 在 `DOWN`、恢复和证书临期时通知。通知方式在**设置 → 通知**中添加，名称唯一。Telegram 使用 Bot Token 和管理员 Chat ID；Webhook 支持通用、钉钉、企微和飞书，通用格式发送包含事件、监控、消息、文本和时间戳的版本化 JSON，并可签名，钉钉和飞书使用平台签名参数。每种方式独立配置代理、语言和**测试**按钮；每个监控选择通知方式和重复提醒，关闭通知只暂停发送、不丢失选择。

发送先立即尝试一次，可重试失败会按退避在后台重试，最长约 1 小时；同一监控、方式和目标的状态告警保持顺序，重复事件去重，不可重试错误立即失败。**设置 → 发送记录**显示最近 100 条，保留 365 天；通知语言独立于界面语言。

## 运维

**数据与备份**：数据目录包含 `windwatch.db`（及 WAL、SHM 文件）、`secret.key` 和 `logs/` 下的每日日志。`secret.key` 加密代理密码、Push Token 和通知凭据，必须与数据库一起备份，丢失后无法恢复。停止 WindWatch 后复制整个目录；迁移在启动时执行，升级前先备份，不支持自动降级。原始检查保留 7 天，30 分钟聚合、事件和发送记录保留 365 天。

**时区**：在**设置 → 常规**选择 IANA 时区（默认 `UTC`），控制界面时间、通知时间和日图边界；存储仍为 UTC。

**账号**：在 `/setup` 创建的第一个账号是管理员。管理员管理监控、代理、通知、用户和公开页；观察者只能查看仪表盘、详情和事件，不能修改配置，也看不到 Push Token、通知方式名称或代理名称。至少保留一个启用的管理员；停用账号或重置密码会注销其会话。后台重置密码不会重新启用停用账号，离线恢复命令会重新启用；不支持自助注册、邮件邀请或 OIDC。

**公开状态页**：在**设置 → 公开页**启用、填写标题和说明并选择监控。`/status` 无需登录，每 30 秒刷新，只展示名称、类型、状态和 24h/7d/30d/90d 可用率，并标记 `noindex, nofollow`；关闭后返回 404。

**密码恢复**：管理员无法登录时，停止服务后运行：

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

命令会隐藏输入并提示新密码；脚本可用 `--password-stdin`，多账号时用 `--username NAME` 指定。重置会重新启用账号、替换密码哈希并注销全部会话。

**反向代理与安全**：内置服务只提供 HTTP，请用 Caddy、Nginx 或 Traefik 终止 TLS。只有直接来源在 `WINDWATCH_TRUSTED_PROXIES` 中时才信任 `X-Forwarded-*`；只列出你控制的代理地址，不要填写客户端网段。有效客户端地址用于登录限流，转发协议和主机用于 Secure Cookie 和 Push URL。一个数据目录只运行一个实例，使用只对该目录有写权限的非特权用户，并确保目录和 `secret.key` 不可被其他用户读取。`/healthz` 报告进程、数据库状态和构建版本，不检查调度器；不要在未认证的情况下暴露管理后台。

## 开发

```bash
make check
make race
make build
```

`make check` 执行格式检查、vet 和测试；`make vuln` 运行固定版本的 `govulncheck`；`make dist` 和 `make docker-build` 负责部署构建；`make version` 输出当前版本。代码位于 `cmd/windwatch` 和 `internal/`；测试使用本地替身覆盖 HTTP、TCP、Docker、Telegram 和代理。SQLite 使用纯 Go 的 `modernc.org/sqlite` 驱动，因此支持 `CGO_ENABLED=0` 构建。

## 许可

基于 MIT 许可发布，见 [LICENSE](LICENSE)。
