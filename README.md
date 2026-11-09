<a id="english"></a>

# 听风者 (WindWatch)

**Lightweight, self-hosted monitoring in a single Go binary.**

WindWatch watches websites, TCP ports, Docker containers, and external heartbeats, and alerts you through Telegram or webhooks. It is built for one person or a small team: one binary, one SQLite database, server-rendered pages, and no external services or frontend build chain.

English | [中文](#chinese)

## Features

- **HTTP(S) monitors** — method, expected status, response keyword, redirects, timeout, optional certificate and domain expiry checks, and a per-monitor outbound proxy.
- **TCP monitors** — verify that a `host:port` accepts connections.
- **Docker monitors** — check that a container is running and, optionally, healthy.
- **Push / heartbeat monitors** — a secret URL for scripts, cron jobs, or devices; after the first heartbeat, silence past the interval plus grace period marks the monitor down. Tokens can be regenerated.
- **Notifications** — any number of named Telegram bots and Generic, DingTalk, WeCom, or Feishu webhooks, each with its own language, proxy, and test action. Per-monitor selection, repeat reminders, retries, deduplication, and a delivery log.
- **Dashboard and history** — search, status filters, 24h/7d/30d/90d uptime, latency, recent checks, and timezone-aware charts. Storage remains UTC.
- **Multi-user web UI** — administrators manage everything; read-only viewers inspect dashboards, details, and events. Simplified Chinese and English, responsive layout, theme follows the operating system.
- **Public status page** — an optional login-free `/status` page limited to the monitors you select; names, types, statuses, and uptime only.
- **Small footprint** — about 30 MB idle memory, embedded templates and assets, and SQLite storage. One instance supports up to 500 monitors; performance targets assume about 200.

## Quick start

Requirements: Go 1.27.3 or newer.

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

Open [http://localhost:8080/setup](http://localhost:8080/setup) to create the administrator. There are no default credentials, and setup closes after the first account is created. Additional accounts are created under **Users**. `serve` is the default command, so `./windwatch` is equivalent to `./windwatch serve`.

Tagged [releases](https://github.com/fynhq/windwatch/releases) publish Linux binaries and the GHCR image; to build locally, use the Dockerfile or `make dist` below.

## Deployment

Both paths keep all state in one data directory; monitors, proxies, notifications, and users are managed in the web UI.

### Docker Compose

Copy the tracked [compose.example.yaml](compose.example.yaml) to the gitignored `compose.yaml`, create the data directory, and start:

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
# Linux only: make ./data writable by the container user (UID 65532).
sudo chown 65532:65532 data && sudo chmod 700 data
docker compose up -d
```

Open [http://localhost:8080/setup](http://localhost:8080/setup). Data and daily logs live in `./data` next to `compose.yaml`.

The example uses host networking on Linux so monitors can reach host services such as a local proxy. On Docker Desktop, skip `chown`, comment out `network_mode: host`, uncomment the `ports` mapping, and use `host.docker.internal` instead of `127.0.0.1`. If HTTPS checks show multi-second latency there, enable the commented `GODEBUG=tlsmlkem=0`; certificate verification is unchanged.

To upgrade, back up `./data`, then run `docker compose pull && docker compose up -d`. Migrations run at startup and are not reversible; to roll back, restore the backup and set `WINDWATCH_IMAGE` to the previous tag or digest. The example references the published `ghcr.io/fynhq/windwatch:latest` image, which supports linux/amd64 and linux/arm64; to build locally, run `docker build -t windwatch:latest .` and set `WINDWATCH_IMAGE=windwatch:latest`. Container hardening and Docker socket options are documented in [compose.example.yaml](compose.example.yaml); mounting the socket is required for Docker monitors and grants significant control over the host.

Logs: `docker compose logs -f windwatch`.

### Single binary (Linux amd64 and arm64)

Standalone binaries are provided only for Linux amd64 and arm64; other platforms should use the container image. Releases publish `windwatch-linux-amd64-vX.Y.Z.tar.gz`, `windwatch-linux-arm64-vX.Y.Z.tar.gz`, and `SHA256SUMS`. Verify with `sha256sum -c --strict SHA256SUMS`, extract the archive for your architecture, and run `windwatch version`.

`make dist` runs formatting, vet, and tests first, then writes both archives and `dist/SHA256SUMS`; each archive contains a single static `windwatch` binary. HTTPS checks use the system CA bundle, so minimal hosts need a `ca-certificates` package.

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

The unit stores data in `/var/lib/windwatch` and listens on `:8080`; its comments cover trusted proxies and Docker monitoring. Follow logs with `journalctl -u windwatch -f`. To upgrade, back up the data directory, stop the service, replace the binary, and start it again; run `windwatch version` to confirm. Without systemd, use the quick-start command. Never point two instances at one data directory.

## Configuration

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | Persistent data directory |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | Daily log directory; rotated per day, kept for 30 days |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | unset | Comma-separated proxy IPs or CIDRs whose `X-Forwarded-*` headers are trusted |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | Endpoint used by `windwatch healthcheck` |
| — | `DOCKER_HOST` | Linux Unix socket; Windows named pipe | Docker Engine endpoint; `unix://`, `tcp://`, and Windows `npipe://` are supported |
| — | `GOMEMLIMIT` | unset | Go soft memory limit; useful in a constrained container |

Business settings — users, the public status page, notification methods, outbound proxies, and monitors — are managed in the web UI and stored in SQLite. There is no configuration file.

## Monitoring

Each monitor has an interval, timeout, failure threshold, and recovery threshold. Defaults are 60 seconds, 10 seconds, two failures before `DOWN`, and one success before recovery. Intervals range from 20 seconds to 24 hours; timeouts from 1 second to 5 minutes and must be shorter than the interval. Before an active check marks a monitor `DOWN`, WindWatch rechecks once after a short delay; a successful recheck prevents the transition. Push monitors cannot be rechecked. Each monitor selects its notification methods and can pause notifications without losing that selection.

**HTTP(S)** supports `GET` and `HEAD`, an expected status code or range (default `200-299`), an optional response keyword, redirect control, and TLS verification (enabled by default). Optional checks warn when the TLS certificate (default 14 days) or registered domain (default 30 days) is about to expire; domain checks require a public domain and RDAP data, and lookup failures never change monitor status. Only skip TLS verification for internal services with self-signed certificates.

**TCP** checks that `host:port` accepts a connection. It only verifies reachability, not a proxy's egress path. Inside a container, `127.0.0.1` is the container itself; use an address reachable from it.

**Docker** checks a container by name or ID and can require a healthy status; Docker's `starting` health state is not treated as a failure. `DOCKER_HOST` supports Unix sockets, `tcp://` endpoints, and Windows named pipes (source builds only). Docker checks use read-only inspect calls, always connect directly, and ignore proxy settings. To enable Docker monitoring in the example container, uncomment the socket and `group_add` entries and set `DOCKER_GID` to the socket's group ID.

**Push / heartbeat** generates a secret URL, shown on the monitor page:

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

`GET` and `POST` are accepted; the token is the only credential, valid heartbeats are limited to one per second per token, and unauthenticated floods are capped per client IP. The monitor stays `PENDING` until the first heartbeat arrives; there is no initial timeout alert. Treat the URL as a secret and keep it out of application and reverse-proxy logs.

**Outbound proxies** — HTTP and SOCKS5 proxies, with unique names and optional credentials, are configured under **Settings → Outbound proxies** and can be selected per HTTP/TCP monitor and per notification method. A failed proxy fails the check; there is no direct fallback. Docker and Push checks ignore proxies.

## Notifications

WindWatch notifies on `DOWN`, recovery, and certificate or domain expiry warnings. Add methods under **Settings → Notifications**; names must be unique. Telegram methods use a bot token plus admin chat IDs. Webhooks support Generic, DingTalk, WeCom, and Feishu; Generic sends versioned JSON containing the event type, monitor status and identity, message, text, and timestamp, and can be signed, while DingTalk and Feishu support their platform signature parameters. Each method has its own optional proxy, notification language, and **Test** action. Each monitor chooses its methods and repeat reminders; turning notifications off keeps the selection but pauses delivery.

Delivery is attempted up to three times, then queued and retried with a longer backoff for up to about one hour. State alerts keep their order per monitor, method, and target; duplicate events are deduplicated; non-retryable errors fail immediately. **Settings → Delivery log** shows the last 100 attempts for 365 days. Notification language is independent of the UI language.

## Operations

**Data and backup** — the data directory contains `windwatch.db` (with WAL and SHM files), `secret.key`, and daily logs under `logs/`. `secret.key` encrypts proxy passwords, Push tokens, and notification credentials; back it up together with the database, because lost keys cannot be recovered. Stop WindWatch and copy the whole directory. Migrations run at startup; back up before upgrading, and automatic downgrades are not supported. Raw check results are kept for 7 days; 30-minute rollups, events, and delivery logs for 365 days.

**Timezone** — choose an IANA timezone under **Settings → General** (default `UTC`). It controls displayed timestamps, notification times, and daily chart boundaries; storage remains UTC.

**Accounts** — the first account created at `/setup` is an administrator. Administrators manage monitors, proxies, notifications, users, and the public page. Viewers can sign in and inspect dashboards, details, and events, but cannot change configuration or see Push tokens. At least one active administrator must remain; disabling an account or resetting its password invalidates its sessions. Resetting a password from the web UI keeps a disabled account disabled; the offline recovery command re-enables it. There is no self-registration, email invitation, or OIDC.

**Public status page** — enable it under **Settings → Public page**, set a title and description, and select monitors. `/status` requires no login, refreshes every 30 seconds, shows only names, types, statuses, and 24h/7d/30d/90d uptime, and is marked `noindex, nofollow`. Disabling it returns 404.

**Password recovery** — if no administrator can sign in, stop the service and run:

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

The command prompts for a new password without echoing it; `--password-stdin` supports scripts, and `--username NAME` selects an account when several exist. It re-enables the account, replaces the password hash, and invalidates all of its sessions.

**Reverse proxies and security** — the built-in server speaks HTTP only; terminate TLS with a reverse proxy such as Caddy, Nginx, or Traefik. WindWatch trusts `X-Forwarded-*` only when the direct peer is listed in `WINDWATCH_TRUSTED_PROXIES`; list only proxy addresses you control, never client networks. The effective client address is used for login rate limiting, and the forwarded scheme and host for Secure cookies and the Push URL. Run one instance per data directory, under an unprivileged user with write access only to the data directory, and keep the directory and `secret.key` unreadable by others. The heartbeat URL is a bearer token. Viewers can see monitor targets, certificate/domain details, and events. `/healthz` reports process and database health plus the build version; it does not probe the scheduler, so `windwatch healthcheck` suits systemd or container checks. Do not expose the admin panel anonymously.

## Development

```bash
make check
make race
make build
```

The same checks are available directly with `gofmt -l .`, `go vet ./...`, `go test ./...`, `go test -race ./...`, and `go build ./cmd/windwatch`; `make dist` and `make docker-build` wrap the deployment builds, and `make version` prints the current version. The code lives in `cmd/windwatch` and `internal/{app,web,scheduler,checker,notify,store,i18n,proxy}`. Tests use local fakes for HTTP, TCP, Docker, RDAP, Telegram, and proxies; SQLite uses the pure-Go `modernc.org/sqlite` driver, so `CGO_ENABLED=0` builds are supported.

## License

Released under the MIT License. See [LICENSE](LICENSE).

---

<a id="chinese"></a>

# 听风者 (WindWatch)

**单二进制、轻量、自托管的监控面板。**

WindWatch（听风者）用来监控网站、TCP 端口、Docker 容器和外部心跳，并通过 Telegram 或 Webhook 发出告警。它面向个人和小团队：一个二进制、一个 SQLite 数据库、服务端渲染页面，不依赖外部服务，也没有前端构建链。

[English](#english) | 中文

## 功能

- **HTTP(S) 监控**：支持请求方法、期望状态码、响应关键字、跳转控制、超时、可选的证书与域名到期检查，以及按监控选择的出站代理。
- **TCP 监控**：检查 `host:port` 是否可以建立连接。
- **Docker 监控**：检查容器是否运行，并可要求容器处于 healthy 状态。
- **Push / 心跳监控**：为脚本、定时任务或设备提供秘密 URL；首次心跳后，超过“间隔 + 宽限期”没有心跳时判定异常。Token 可重新生成。
- **通知**：可添加任意数量、名称唯一的 Telegram Bot 和通用、钉钉、企微、飞书 Webhook，每种方式独立配置语言、代理和测试按钮。支持按监控选择通知方式、重复提醒、重试、去重和发送记录。
- **仪表盘与历史**：搜索、状态筛选、24h/7d/30d/90d 可用率、延迟、最近检查和按时区归日的图表；存储始终使用 UTC。
- **多用户 Web 界面**：管理员管理全部配置，只读观察者可查看仪表盘、详情和事件。支持简体中文和英文、响应式布局，主题跟随操作系统。
- **公开状态页**：可选的免登录 `/status` 页面，只展示选定监控的名称、类型、状态和可用率。
- **资源占用小**：空载内存约 30 MB，模板和静态资源内嵌，使用 SQLite 存储。单实例最多 500 个监控，性能目标以约 200 个为基准。

## 快速开始

需要 Go 1.27.3 或更高版本。

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup) 创建管理员账号。系统没有默认密码，创建第一个账号后初始化页面自动关闭，后续账号在**用户**页创建。`serve` 是默认子命令，因此 `./windwatch` 等价于 `./windwatch serve`。

正式发布在 [GitHub Releases](https://github.com/fynhq/windwatch/releases) 提供 Linux 二进制和 GHCR 镜像；需要本地构建时使用 Dockerfile 或下面的 `make dist`。

## 部署

两种方式都只用同一个数据目录保存状态；监控、代理、通知和用户都在 Web 界面中管理。

### Docker Compose

把仓库中的 [compose.example.yaml](compose.example.yaml) 复制成本地、被 Git 忽略的 `compose.yaml`，创建数据目录后启动：

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
# 仅 Linux：让容器用户（UID 65532）可写 ./data。
sudo chown 65532:65532 data && sudo chmod 700 data
docker compose up -d
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup)。数据与日志保存在 `compose.yaml` 旁的 `./data` 目录。

示例在 Linux 上默认使用 host 网络，便于监控宿主机服务（例如本地代理）。在 Docker Desktop 上跳过 `chown`，注释掉 `network_mode: host`、取消 `ports` 映射的注释，并把 `127.0.0.1` 换成 `host.docker.internal`。如果那里 HTTPS 检查出现数秒延迟，可启用已注释的 `GODEBUG=tlsmlkem=0`；证书校验不变。

升级时先备份 `./data`，再执行 `docker compose pull && docker compose up -d`。迁移在启动时执行且不可自动回退；需要回滚时恢复备份，并把 `WINDWATCH_IMAGE` 设为升级前的版本标签或 digest。示例默认引用已发布的 `ghcr.io/fynhq/windwatch:latest` 镜像，支持 linux/amd64 和 linux/arm64；本地构建可执行 `docker build -t windwatch:latest .` 并设置 `WINDWATCH_IMAGE=windwatch:latest`。容器加固和 Docker socket 选项见 [compose.example.yaml](compose.example.yaml)；Docker 监控需要挂载 socket，这会让 WindWatch 获得较高的宿主机控制权限。

查看日志：`docker compose logs -f windwatch`。

### 单文件二进制（Linux amd64 和 arm64）

单文件只提供 Linux amd64 和 arm64 版本，其他平台请使用容器镜像。正式发布提供 `windwatch-linux-amd64-vX.Y.Z.tar.gz`、`windwatch-linux-arm64-vX.Y.Z.tar.gz` 和 `SHA256SUMS`。用 `sha256sum -c --strict SHA256SUMS` 校验，解压对应架构的压缩包，再用 `windwatch version` 查看版本。

`make dist` 会先执行格式检查、vet 和测试，然后生成两个压缩包和 `dist/SHA256SUMS`；每个压缩包内是单个静态链接的 `windwatch` 二进制。HTTPS 检查使用系统 CA 证书，最小化系统需要安装 `ca-certificates` 包。

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

该 unit 使用 `/var/lib/windwatch` 保存数据并监听 `:8080`；可信代理和 Docker 监控的开关见 unit 注释。用 `journalctl -u windwatch -f` 查看日志。升级时先备份数据目录，停止服务，替换二进制后重新启动，并用 `windwatch version` 确认版本；不使用 systemd 时直接采用快速开始中的命令。不要让两个实例共用同一个数据目录。

## 配置

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP 监听地址 |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | 持久化数据目录 |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | 每日日志目录；按天轮转，保留 30 天 |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | 不设置 | 逗号分隔的可信代理 IP 或 CIDR，只有这些来源的 `X-Forwarded-*` 会被信任 |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | `windwatch healthcheck` 请求的地址 |
| — | `DOCKER_HOST` | Linux 为 Unix socket；Windows 为 named pipe | Docker Engine 端点；支持 `unix://`、`tcp://` 和 Windows `npipe://` |
| — | `GOMEMLIMIT` | 不设置 | Go 软内存上限；在内存受限的容器中有用 |

业务配置——用户、公开状态页、通知方式、出站代理和监控定义——都在 Web 界面中管理并保存在 SQLite 中，不使用配置文件。

## 监控

每个监控都有检查间隔、超时、失败阈值和恢复阈值。默认值为 60 秒间隔、10 秒超时、连续失败 2 次判定 `DOWN`、连续成功 1 次恢复。间隔范围为 20 秒到 24 小时；超时范围为 1 秒到 5 分钟，且必须小于检查间隔。主动检查在判定 `DOWN` 前会等待数秒自动复核一次；复核成功则不会判定 `DOWN`。Push 监控无法主动复核。每个监控选择通知方式，也可以暂停通知而不丢失选择。

**HTTP(S)** 支持 `GET` 和 `HEAD`、期望状态码或范围（默认 `200-299`）、响应体关键字、跳转控制，以及 TLS 证书校验（默认开启）。可选检查会在 TLS 证书（默认提前 14 天）或注册域名（默认提前 30 天）临期时告警；域名检查要求公共域名并依赖 RDAP 数据，查询失败不会改变监控状态。只有使用自签名证书的内网服务才建议跳过 TLS 校验。

**TCP** 检查 `host:port` 是否可以建立连接，只验证端口可达，不验证代理的出口路径。容器内的 `127.0.0.1` 指容器自身，应改用容器可访问的地址。

**Docker** 按容器名称或 ID 检查，并可要求容器处于 healthy 状态；Docker 的 `starting` 状态不视为失败。`DOCKER_HOST` 支持 Unix socket、`tcp://` 端点和 Windows named pipe（仅源码构建）。Docker 检查只调用 Docker inspect API，始终直连，不受代理设置影响。在示例容器中启用 Docker 监控时，取消 socket 和 `group_add` 的注释，并把 `DOCKER_GID` 设为 socket 的组 ID。

**Push / 心跳** 会生成一个秘密 URL，显示在监控详情页：

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

支持 `GET` 和 `POST`；Token 是唯一凭据，同一 Token 每秒最多接受一次有效心跳，未认证的请求洪泛按客户端 IP 限流。首次心跳到达前监控保持 `PENDING`，不会产生首次心跳超时告警。心跳 URL 本身就是密钥，不要让它进入应用日志或反向代理日志。

**出站代理**：在**设置 → 出站代理**中配置 HTTP 或 SOCKS5 代理，名称不能重复，可带用户名和密码。每个 HTTP/TCP 监控和每个通知方式都可以选择代理；代理不可用时检查直接失败，不会静默回退到直连。Docker 和 Push 检查不使用代理。

## 通知

WindWatch 在 `DOWN`、恢复、证书临期和域名临期时发送通知。通知方式在**设置 → 通知**中添加，名称不能重复。Telegram 使用 Bot Token 加管理员 Chat ID；Webhook 支持通用、钉钉、企微和飞书，通用格式发送包含事件类型、监控状态和标识、消息、文本和时间戳的版本化 JSON，并可签名，钉钉和飞书使用平台的时间戳与签名字段。每种方式独立配置可选代理、通知语言和**测试**按钮。每个监控选择自己的通知方式和重复提醒；关闭通知开关会暂停发送，但保留选择。

发送最多即时尝试三次，仍失败的可重试错误会保留并在后台按更长退避重试，最长约 1 小时。同一监控、通知方式和目标的状态告警保持顺序；重复事件按方式和目标去重；不可重试错误立即标记失败。**设置 → 发送记录**显示最近 100 条投递记录，保留 365 天。通知语言独立于界面语言。

## 运维

**数据与备份**：数据目录包含 `windwatch.db`（及 WAL、SHM 文件）、`secret.key` 和 `logs/` 下的按天日志。`secret.key` 用于加密代理密码、Push Token 和通知凭据；它必须和数据库一起备份，丢失后无法恢复。备份时停止 WindWatch 并复制整个目录。迁移在启动时自动执行，升级前请先备份；不支持自动降级。原始检查结果保留 7 天，30 分钟聚合、事件和投递日志保留 365 天。

**时区**：在**设置 → 常规**中选择 IANA 时区（默认 `UTC`），它控制界面时间、通知时间和日图边界；存储仍使用 UTC。

**账号**：在 `/setup` 创建的第一个账号是管理员。管理员管理监控、代理、通知、用户和公开页。观察者可以登录并查看仪表盘、详情和事件，但不能修改配置，也看不到 Push Token。系统始终至少保留一个启用的管理员；停用账号或重置密码会让该账号的会话失效。在后台重置密码不会重新启用已停用账号，离线恢复命令会重新启用。不支持自助注册、邮件邀请或 OIDC。

**公开状态页**：在**设置 → 公开页**中启用、填写标题和说明并选择监控。`/status` 无需登录，每 30 秒刷新，只展示名称、类型、状态和 24h/7d/30d/90d 可用率，并标记 `noindex, nofollow`；关闭后返回 404。

**密码恢复**：管理员无法登录时，停止服务后运行：

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

命令会隐藏输入并提示新密码；脚本可用 `--password-stdin`，存在多个账号时用 `--username NAME` 指定。重置会重新启用账号、替换密码哈希并注销它的全部会话。

**反向代理与安全**：内置服务只提供 HTTP，请用 Caddy、Nginx 或 Traefik 等反向代理终止 TLS。只有直接来源位于 `WINDWATCH_TRUSTED_PROXIES` 中时，WindWatch 才信任 `X-Forwarded-*`；只列出你控制的代理地址，不要填写客户端网段。有效客户端地址用于登录限流，转发协议和主机用于 Secure Cookie 和 Push URL。一个数据目录只运行一个实例，并使用只对数据目录有写权限的非特权用户；数据目录和 `secret.key` 不应被其他用户读取。心跳 URL 是 bearer token。观察者可以查看监控目标、证书/域名信息和事件。`/healthz` 报告进程、数据库状态和构建版本；它不检查调度器是否卡死，systemd 或容器健康检查请使用 `windwatch healthcheck`。不要在未认证的情况下暴露管理后台。

## 开发

```bash
make check
make race
make build
```

也可以直接运行 `gofmt -l .`、`go vet ./...`、`go test ./...`、`go test -race ./...` 和 `go build ./cmd/windwatch`；`make dist` 和 `make docker-build` 负责部署构建，`make version` 输出当前版本。代码位于 `cmd/windwatch` 和 `internal/{app,web,scheduler,checker,notify,store,i18n,proxy}`。测试使用本地替身覆盖 HTTP、TCP、Docker、RDAP、Telegram 和代理；SQLite 使用纯 Go 的 `modernc.org/sqlite` 驱动，因此支持 `CGO_ENABLED=0` 构建。

## 开源协议

基于 MIT 许可发布，见 [LICENSE](LICENSE)。
