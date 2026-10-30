<a id="english"></a>

# 听风者 (WindWatch)

**Lightweight, self-hosted monitoring in a single Go binary.**

WindWatch watches websites, TCP ports, Docker containers, and external heartbeats, and lets you know when something goes wrong. It is built for a single person or a small team: one binary, one SQLite database, no external services to operate, and no frontend build chain.

English | [简体中文](#chinese)

## Features

- **HTTP(S) monitors** — method, expected status code or range, response keyword, redirect control, timeout, and an optional per-monitor outbound proxy.
- **TCP port monitors** — verify that a host and port accept connections. This is also how local proxy ports are watched.
- **Docker container monitors** — check that a container is running and, optionally, healthy.
- **Push / heartbeat monitors** — give scripts, cron jobs, or remote devices a secret URL to report to. Silence beyond the heartbeat interval plus grace period marks the monitor down.
- **HTTPS extras** — optional TLS certificate and domain expiry checks, with warning thresholds and expiry details on the monitor page.
- **Dashboard and history** — an overall status band, search, and status filters sit above a monitor card grid that uses two columns on wide screens and one on smaller screens. Each card shows 24-hour and current latency, a relative last-check time, and its latest 24 checks at the configured interval. Monitor details place a full-width status history card above recent events and configuration; the recent, 24-hour, 30-day, and 90-day ranges share one layout and combine uptime bars with a latency line. Display and daily charts use a configurable IANA timezone; storage remains UTC.
- **Notifications** — any number of named Telegram bot and webhook methods; webhooks support Generic, DingTalk, WeCom, and Feishu formats. Each method has its own credentials, notification language, optional outbound proxy, and test action. Monitors choose which methods receive their alerts and configure their own repeat reminders. Retries, deduplication, and delivery history are included.
- **Multi-user web UI** — administrators manage monitors, proxies, notifications, and accounts; read-only viewers can inspect the dashboard, monitor details, and events. Simplified Chinese and English, a fixed desktop sidebar, a mobile top bar with a bottom dock, and a color theme that follows the operating system. Dashboard and event filters run in the browser. Dashboard and public status pages refresh a server-rendered fragment in place every 30 seconds, pause while the tab is hidden, and keep full-page refresh as the no-JavaScript fallback.
- **Public status page** — an optional login-free page with a selected monitor list; it shows names, statuses, 24h/7d/30d/90d uptime, and a 90-day daily uptime strip (the latest 30 days on mobile) without targets, configuration, or secrets.
- **Password recovery** — the `windwatch reset-password` command resets an account offline and invalidates its sessions.
- **Small footprint** — about 30 MB resident memory, server-rendered pages, embedded templates and assets, SQLite storage, and no Redis, Node.js, or external database.

## Quick start

Requirements: Go 1.27 or newer.

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

Open [http://localhost:8080/setup](http://localhost:8080/setup) and create the administrator account. There are no default credentials, and setup closes after the first account is created. Additional accounts are created under **Users**.

Useful commands:

```bash
./windwatch healthcheck --url http://127.0.0.1:8080/healthz
./windwatch reset-password --data-dir ./data
./windwatch version
```

`serve` is the default command, so `./windwatch` is equivalent to `./windwatch serve`.

> Prebuilt release artifacts and the GHCR image are published with tagged releases. To build locally instead, use the Dockerfile or `make dist` as described below.

## Deployment

WindWatch supports two deployment paths: Docker Compose and a single Linux binary. Both keep all state in one data directory; notification, proxy, and monitor settings are managed in the web UI.

### Docker Compose

The included [Dockerfile](Dockerfile) builds a static binary and runs it as an unprivileged user on an Alpine runtime. CA certificates are installed in the runtime image, and templates, static assets, migrations, locales, and time zone data are embedded in the binary.

Copy the tracked [compose.example.yaml](compose.example.yaml) to the local, gitignored `compose.yaml`, create the host data directory, and start:

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
# Linux only: make ./data writable by the container user (UID 65532).
sudo chown 65532:65532 data && sudo chmod 700 data
docker compose up -d
```

On Docker Desktop, skip the `chown` line and use port mapping instead of host networking. Docker Desktop shares host folders through its VM; keep backups of `./data` and never run two instances against the same directory. Existing deployments that used the previous `windwatch_data` volume must copy it into `./data` before switching the mount; see Data and backup.

The example references the published GHCR image. To build locally instead, run `docker build -t windwatch:latest .` and set `WINDWATCH_IMAGE=windwatch:latest` before `docker compose up -d`. To upgrade, back up the `./data` directory, then run `docker compose pull && docker compose up -d`. Database migrations run on startup and are not reversible; to roll back, restore the backup and set `WINDWATCH_IMAGE` to the previous version tag or the digest recorded before the upgrade.

Open [http://localhost:8080/setup](http://localhost:8080/setup) and create the administrator account. Data and daily logs are stored in the local `./data` directory next to `compose.yaml` (`./data/logs` for logs). `docker compose down` leaves the directory in place, and `docker compose down -v` does not delete bind-mounted data; delete `./data` manually if you really want to remove it.

The example uses host networking by default, the simplest way to reach services on the host (such as a local proxy) on Linux. On Docker Desktop, or when you prefer bridge networking, comment out `network_mode: host` and uncomment the `ports` mapping; use `host.docker.internal` instead of `127.0.0.1` to reach the host.

```bash
docker compose logs -f windwatch
docker compose ps
docker compose down
```

Local Docker builds use the standard Go module proxy by default. The image embeds the version from `VERSION` and the current commit; pass `VERSION`, `COMMIT`, and `BUILD_TIME` as build args to override them. To use a mirror, pass `GOPROXY` to `make docker-build` or build with the appropriate `--build-arg`. The service runs with a read-only root filesystem, a 16 MB `/tmp` tmpfs, all capabilities dropped, `no-new-privileges`, a 128 MB memory limit, and `GOMEMLIMIT=96MiB`; adjust these in [compose.example.yaml](compose.example.yaml) or your local copy. If you change the internal listen address, update `WINDWATCH_HEALTH_URL` to match.

The Alpine runtime includes a shell; use `docker compose logs` for output and copy the `./data` directory for backups. If a reverse proxy fronts the container, set `WINDWATCH_TRUSTED_PROXIES` to the proxy's address or network.

Docker container monitoring is off by default. To enable it, uncomment the socket mount and `group_add` entries, then set `DOCKER_GID` to the socket's group ID (`stat -c '%g' /var/run/docker.sock`). Mounting the Docker socket gives WindWatch significant control over the host.

The Dockerfile supports `linux/amd64` and `linux/arm64` builds. With Buildx installed (and QEMU/binfmt for cross-platform builds), you can build for a specific platform:

```bash
docker buildx build --platform linux/arm64 --load -t windwatch:arm64 .
```

### Single binary (Linux amd64 and arm64)

Standalone binaries are provided only for Linux on x86-64 (`amd64`) and ARM64 (`aarch64`). Other platforms should use the container image. Building requires Go 1.27 or newer and `make`.

Tagged releases also publish `windwatch-linux-amd64-vX.Y.Z.tar.gz`, `windwatch-linux-arm64-vX.Y.Z.tar.gz`, and a single `SHA256SUMS` on the [GitHub Releases page](https://github.com/fynhq/windwatch/releases). Verify them with `sha256sum -c --strict SHA256SUMS`, extract the archive for your architecture, and check the running version with `windwatch version`.

Build both binaries and their checksums:

```bash
make dist
```

This writes:

```text
dist/windwatch-linux-amd64-v0.2.0.tar.gz
dist/windwatch-linux-arm64-v0.2.0.tar.gz
dist/SHA256SUMS
```

If module downloads are blocked, set `GOPROXY` to a reachable Go module proxy before building. Verify the archives with `cd dist && sha256sum -c --strict SHA256SUMS`. Each archive contains a single statically linked `windwatch` binary with no runtime dependencies; it embeds the UI, migrations, locales, and time zone data. HTTPS checks use the system CA bundle, so install a `ca-certificates` package on minimal hosts. `make dist` runs the formatting, vet, and test checks first. The version is read from the repository `VERSION` file; pass `VERSION=0.2.0` to override it for a local build.

Install on a systemd host, choosing the binary that matches `uname -m`:

```bash
tar -xzf dist/windwatch-linux-amd64-v$(cat VERSION).tar.gz -C /tmp
sudo install -m 0755 /tmp/windwatch /usr/local/bin/windwatch
sudo useradd --system --user-group --home-dir /var/lib/windwatch --shell /usr/sbin/nologin windwatch
sudo install -d -o windwatch -g windwatch -m 0750 /var/lib/windwatch
sudo install -m 0644 deploy/windwatch.service /etc/systemd/system/windwatch.service
sudo systemctl daemon-reload
sudo systemctl enable --now windwatch
```

Open [http://localhost:8080/setup](http://localhost:8080/setup). Logs go to the journal:

```bash
journalctl -u windwatch -f
```

The unit stores data in `/var/lib/windwatch` and listens on `:8080`. Change `ExecStart` or set `WINDWATCH_ADDR` to bind elsewhere. For Docker monitoring, uncomment `SupplementaryGroups=docker` and make sure the `windwatch` user can access the Docker socket.

To upgrade, back up the data directory, stop the service, replace the binary, and start it again. Database migrations run automatically. Run `windwatch version` after the upgrade to confirm the expected version. Without systemd:

```bash
./windwatch serve --addr :8080 --data-dir ./data
```

A single binary can be copied anywhere; keep its data directory on persistent storage and never point two instances at the same directory.

## Configuration

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP listen address |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | Persistent data directory |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | Directory for daily log files; rotated per day, kept for 30 days |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | unset | Comma-separated proxy IPs or CIDRs whose `X-Forwarded-*` headers are trusted |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | Endpoint used by `windwatch healthcheck` |
| — | `DOCKER_HOST` | `unix:///var/run/docker.sock` (Linux) or `npipe:////./pipe/docker_engine` (Windows) | Docker Engine endpoint; `unix://`, `tcp://`, and Windows `npipe://` are supported |
| — | `GOMEMLIMIT` | unset | Go soft memory limit; useful when running in a constrained container |

Business settings — users, the public status page, notification channels, outbound proxies, and monitor definitions — are managed in the web UI and stored in SQLite. There is no configuration file.

## Monitoring

Each monitor has an interval, timeout, failure threshold, and recovery threshold. The defaults are a 60-second interval, a 10-second timeout, two consecutive failures before `DOWN`, and one success before recovery. Intervals range from 20 seconds to 24 hours; timeouts from 1 second to 5 minutes and must be shorter than the interval. Each monitor selects which notification methods receive its alerts and can pause notifications without losing that selection; scheduling and checks are unchanged.

### HTTP(S)

WindWatch supports `GET` and `HEAD` requests, an expected status code or range (default `200-299`), an optional keyword in the response body, redirect control, and TLS verification (enabled by default).

For HTTPS URLs, two optional checks can be enabled:

- **Certificate expiry** — warns when the certificate expires within the configured number of days (default 14).
- **Domain expiry** — warns when the registered domain expires within the configured number of days (default 30). The domain is derived from the configured URL, not from redirect targets. This requires a public domain name and RDAP data; IP addresses and single-label hosts are rejected, and RDAP coverage varies by TLD. Domain lookup failures are shown in the monitor details but do not change monitor status.

Only enable "skip TLS verification" for internal services with self-signed certificates.

### TCP port

A TCP monitor checks that `host:port` accepts a connection. Use this to watch a local proxy's listening port, for example `127.0.0.1:7890` on a native install; inside a container, `127.0.0.1` is the container itself, so use an address reachable from the container. It only verifies that the port is reachable; use an HTTP monitor with that proxy selected when you need to verify the proxy's actual egress path.

All probes run from the WindWatch process. When WindWatch runs in a container, `127.0.0.1` refers to the container itself; use an address reachable from the container for proxies and TCP targets, or run the container with host networking on Linux.

### Docker container

A Docker monitor checks a container by name or ID and can additionally require a healthy Docker health status. The Docker Engine endpoint comes from `DOCKER_HOST` and supports Unix sockets, `tcp://` endpoints, and Windows named pipes (`npipe:////./pipe/docker_engine`). The default is a Unix socket on Linux and a named pipe on Windows. Named pipes require a Windows build; the official standalone binaries are Linux-only, so Windows users should build from source or use a `tcp://` endpoint. Docker checks always connect directly and ignore outbound proxy settings.

Mounting `/var/run/docker.sock` into a container gives WindWatch significant control over the Docker host. Only do this when Docker monitoring is needed, and restrict access to the socket.

### Push / heartbeat

A Push monitor generates a secret heartbeat URL, shown on the monitor's detail page:

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

Both `GET` and `POST` are accepted. The token is the only credential, valid heartbeats are limited to one per second per token, and unauthenticated request floods are capped per client IP. The monitor stays `PENDING` until the first heartbeat arrives. If no heartbeat arrives within the configured interval plus grace period, the monitor is marked `DOWN`.

Treat the heartbeat URL as a secret: do not put it in public logs, and do not let a reverse proxy record the full request path.

### Outbound proxies

HTTP and SOCKS5 proxies can be configured under **Settings → Outbound proxies**, with optional username and password. Proxy names must be unique. Each HTTP or TCP monitor and each notification method can use a proxy. Docker and Push checks always connect directly. If a selected proxy fails, the check fails; there is no silent fallback to a direct connection.

## Notifications

WindWatch notifies on `DOWN`, recovery, certificate expiry warnings, and domain expiry warnings.

Notification methods are managed under **Settings → Notifications**. You can add any number of methods, and names must be unique:

- **Telegram** — a bot token plus one or more admin chat IDs. Each method is a separate bot.
- **Webhook** — a `POST` endpoint in Generic, DingTalk, WeCom, or Feishu format. Generic sends versioned JSON; the other formats send text messages and verify the platform response code. A signing secret is optional for Generic, DingTalk, and Feishu, and unused for WeCom.

Each method has its own optional outbound proxy, notification language, and a **Test** action. Each monitor selects which methods receive its alerts; new monitors preselect all enabled methods. Turning off notifications for a monitor keeps its selection but pauses delivery. Repeat reminders are configured per monitor under its notification settings: failure reminders apply to every monitor type, and certificate/domain warning reminders appear only for HTTP monitors. The generic webhook payload includes the event type, monitor status, monitor identity, message, text, and timestamp; signing secrets add `X-WindWatch-Signature: sha256=<hex>` for Generic, or the platform's timestamp and signature parameters for DingTalk and Feishu.

Delivery is attempted up to three times with short backoff, duplicate events are deduplicated per method and target, and **Settings → Delivery log** shows the last 100 delivery attempts with the triggering monitor, the sent content in a dialog, and keeps records for 365 days. Notification language is independent of the UI language.

## Data and backup

Runtime data lives in the configured data directory:

```text
data/
├── windwatch.db
├── windwatch.db-wal
├── windwatch.db-shm
├── secret.key
└── logs/
    └── windwatch-YYYY-MM-DD.log
```

`secret.key` encrypts stored secrets such as proxy passwords, Push tokens, and notification credentials. Keep it private and keep it together with the database: if it is lost, those encrypted values cannot be recovered. To back up, stop WindWatch and copy the entire data directory, including `logs/`; to restore, put both the database and the matching key file back in place.

With Docker Compose, data and logs are in the `./data` directory next to `compose.yaml`. On Linux, stop the stack before copying and keep the files owned by UID 65532 (directory 0700); on Docker Desktop, the shared directory is mapped to the container user. `docker compose down` leaves the directory in place, and `down -v` does not delete bind-mounted data. Existing deployments that used the old named volume can stop the stack, run `docker cp windwatch:/data/. ./data/`, run `sudo chown -R 65532:65532 data` on Linux, then switch the mount.

Database migrations run automatically at startup. Back up the data directory before upgrading. Downgrades are not supported automatically.

Raw check results are retained for 7 days, 30-minute rollups for 365 days, and event history for up to 365 days. Notification delivery logs are retained for 365 days, pending deliveries are handled by startup recovery, and expired sessions plus stale certificate/domain caches are cleaned periodically. The UI shows 24-hour, 7-day, 30-day, and 90-day uptime. Long-range uptime and charts use 30-minute rollups; charts group them by the configured timezone, while storage remains UTC.

## Timezone

Choose the IANA timezone under **Settings → General** (default `UTC`); type in the field to filter the list. It controls displayed timestamps, notification times, and daily chart boundaries. 30-minute rollups are used for long-range uptime and charts; legacy hourly and daily rollup rows are removed on upgrade.

## Accounts and access

The first account created at `/setup` is an administrator. Administrators can create, disable, delete, and reset accounts under **Users**. There is no self-registration, email invitation, or OIDC.

- **Administrators** can manage monitors, proxies, notifications, users, and the public status page.
- **Viewers** can sign in, view the dashboard, monitor details, and events, but cannot modify configuration or view Push tokens.

At least one active administrator must remain. Disabling an account or resetting its password invalidates that account's sessions.

## Public status page

Enable the page under **Settings → Public page**, set a title and optional description, and select the monitors to publish. The page is available at `/status` without a login and refreshes its server-rendered content in place every 30 seconds; its full address is shown in settings, and clicking the address copies it.

Only monitor names, types, current status, 24-hour, 7-day, 30-day, and 90-day uptime, and a 90-day daily uptime strip (the latest 30 days on mobile) are shown. Paused monitors are counted separately and do not turn the overall status into an attention state. Target URLs, monitor configuration, notification settings, and secrets are never exposed. The page is marked `noindex, nofollow` while enabled; disabling it makes `/status` return 404.

## Password recovery

If an administrator cannot sign in, stop the service and run:

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

The command prompts for a new password without echoing it; `--password-stdin` supports scripts. When multiple accounts exist, pass `--username NAME`. The account is re-enabled, its password hash is replaced, and all of its sessions are invalidated. Back up the data directory first, and stop the service to avoid concurrent writes.

## Trusted reverse proxies

WindWatch trusts `X-Forwarded-For`, `X-Forwarded-Proto`, and `X-Forwarded-Host` only when the direct peer is listed in `WINDWATCH_TRUSTED_PROXIES` (IPs or CIDRs). The effective client address is used for login rate limiting; the forwarded scheme and host are used for Secure cookies and the Push heartbeat URL.

Only list proxies you control, and list the proxy address rather than client networks. Without this setting, forwarded headers are ignored and the direct connection address is used.

## Security and operations

- Run one instance per data directory. WindWatch is not a distributed monitoring system and does not support multiple replicas sharing one SQLite database.
- The built-in server speaks HTTP only. Terminate TLS with a reverse proxy such as Caddy, Nginx, or Traefik, and do not expose the admin panel anonymously.
- Run the process as an unprivileged user with write access only to the data directory. The provided container uses UID 65532, and the systemd unit uses a dedicated user. The data directory and `secret.key` should not be readable by other users.
- The heartbeat URL is a bearer token. Make sure reverse proxy and access logs do not record the full path.
- When TLS is terminated by a proxy, configure the proxy CIDR and check that the heartbeat URL displayed in the UI uses the scheme and host you expect before sharing it.
- The public status page exposes the names, statuses, and uptime of the monitors you select. Treat those names as public information.
- Viewers can see monitor targets, certificate/domain details, and events. Use the viewer role only for people who should see that information.
- `/healthz` reports process and database health plus the build version. It does not probe the scheduler. `windwatch healthcheck` is suitable for systemd or container health checks.
- Built-in certificate provisioning is not included.

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/windwatch
```

The Makefile provides the same checks and the deployment builds: `make check`, `make race`, `make version`, `make dist`, `make docker-build`.

The test suite uses local fakes for HTTP, TCP, Docker, RDAP, Telegram, and proxies; no external accounts are required. It also covers user roles, public status page filtering, trusted proxy headers, timezone settings, 30-minute rollups, and Windows npipe path parsing. SQLite is provided by the pure-Go `modernc.org/sqlite` driver, so `CGO_ENABLED=0` builds are supported.

| Path | Responsibility |
|---|---|
| `cmd/windwatch` | Entry point and subcommands |
| `internal/app` | Dependency wiring and lifecycle |
| `internal/web` | HTTP handlers, templates, static assets |
| `internal/scheduler` | Check scheduling, state transitions, retention |
| `internal/checker` | HTTP, TCP, and Docker probes |
| `internal/notify` | Telegram and webhook delivery |
| `internal/store` | SQLite queries, migrations, history |
| `internal/i18n` | English and Simplified Chinese catalogs |
| `internal/proxy` | HTTP and SOCKS5 outbound dialing |

## License

Released under the MIT License. See [LICENSE](LICENSE).

---

<a id="chinese"></a>

# 听风者 (WindWatch)

**单二进制、轻量、自托管的监控面板。**

WindWatch（听风者）用来监控网站、TCP 端口、Docker 容器和外部心跳，并在服务出现问题时通知你。它面向个人和小团队：一个二进制、一个 SQLite 数据库，不需要维护外部服务，也没有前端构建链。

[English](#english) | 简体中文

## 功能

- **HTTP(S) 监控**：支持请求方法、期望状态码或范围、响应关键字、跳转控制、超时，以及按监控选择的出站代理。
- **TCP 端口监控**：检查主机和端口是否可连接。本地代理的监听端口也通过这种方式监控。
- **Docker 容器监控**：检查容器是否运行，并可要求容器处于 healthy 状态。
- **Push / 心跳监控**：为脚本、定时任务或远程设备生成一个秘密 URL，由对方主动上报心跳。超过“心跳间隔 + 宽限期”没有上报时判定为异常。
- **HTTPS 附加检查**：可选检查 TLS 证书和域名到期时间，支持告警提前天数，并在详情页展示到期信息。
- **仪表盘与历史**：仪表盘以总体状态带、搜索和状态筛选开头，监控卡片在宽屏为两列、窄屏为单列，每张卡片展示 24 小时可用率、当前延迟、相对最近检查时间和按监控间隔计算的最近 24 次检查。详情页的状态历史卡片通栏，下方是最近事件与配置、证书、域名和危险操作；最近、24 小时、30 天和 90 天四个区间使用同一布局，把可用率柱和延迟线画在同一张图中。界面和日图使用可配置的 IANA 时区，存储仍使用 UTC。
- **通知**：可添加任意数量、名称唯一的 Telegram Bot 和 Webhook 通知方式，Webhook 支持通用、钉钉、企微、飞书四种格式；每种方式独立配置凭据、通知语言、可选代理，并支持测试发送；每个监控选择使用哪些方式，并在监控项中配置重复提醒。支持重试、去重和投递记录。
- **多用户 Web 界面**：管理员管理监控、代理、通知和账号；只读观察者可以查看仪表盘、监控详情和事件。简体中文和英文，桌面固定侧边栏，手机顶部工具栏 + 底部 dock 导航，主题强制跟随操作系统。仪表盘和事件筛选在浏览器内完成；仪表盘和公开状态页每 30 秒局部刷新服务端渲染的 fragment，页面隐藏时暂停，并以整页刷新作为无 JavaScript 回退。
- **公开状态页**：可选的免登录页面，只展示选定监控的名称、状态、24h/7d/30d/90d 可用率和逐日可用率条（桌面最近 90 天，手机默认最近 30 天），不暴露目标 URL、配置或密钥。
- **密码恢复**：`windwatch reset-password` 可离线重置账号密码并注销该账号的会话。
- **资源占用小**：常驻内存约 30 MB；服务端渲染，模板和静态资源内嵌，SQLite 存储，不依赖 Redis、Node.js 或外部数据库。

## 快速开始

需要 Go 1.27 或更高版本。

```bash
CGO_ENABLED=0 go build -trimpath -o windwatch ./cmd/windwatch
./windwatch serve --addr :8080 --data-dir ./data
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup) 创建管理员账号。系统没有默认密码，创建第一个账号后初始化页面会自动关闭。后续账号在**用户**页创建。

常用命令：

```bash
./windwatch healthcheck --url http://127.0.0.1:8080/healthz
./windwatch reset-password --data-dir ./data
./windwatch version
```

`serve` 是默认子命令，因此 `./windwatch` 等价于 `./windwatch serve`。

> 预编译发布产物和 GHCR 镜像随 tagged release 提供；需要本地构建时请使用 Dockerfile 或 `make dist`。

## 部署

WindWatch 支持两种部署方式：Docker Compose 和单文件 Linux 二进制。两者都只依赖一个数据目录；通知、代理和监控配置都在 Web 界面中保存。

### Docker Compose

仓库自带 [Dockerfile](Dockerfile)，使用多阶段构建生成静态二进制，并在 Alpine 运行时中以非 root 用户运行。CA 证书安装在运行镜像中，模板、静态资源、数据库迁移、语言包和时区数据都嵌入在二进制中。

首次运行前，把仓库中的 [compose.example.yaml](compose.example.yaml) 复制成本地、已被 Git 忽略的 `compose.yaml`，创建宿主数据目录后启动：

```bash
cp compose.example.yaml compose.yaml
mkdir -p data
# 仅 Linux：让容器用户（UID 65532）可写 ./data。
sudo chown 65532:65532 data && sudo chmod 700 data
docker compose up -d
```

在 Docker Desktop 上跳过 `chown`，改用端口映射而不是 host 网络。Docker Desktop 通过虚拟机共享宿主目录；请备份 `./data`，并确保同一目录只有一个实例在运行。旧部署如果使用命名卷 `windwatch_data`，必须先把它复制到 `./data` 再切换挂载；步骤见“数据与备份”。

示例默认引用已发布的 GHCR 镜像。需要本地构建时执行 `docker build -t windwatch:latest .`，并在 `docker compose up -d` 前设置 `WINDWATCH_IMAGE=windwatch:latest`。升级时先备份 `./data` 数据目录，再执行 `docker compose pull && docker compose up -d`。数据库迁移在启动时执行且不可自动回退；需要回滚时恢复备份，并把 `WINDWATCH_IMAGE` 设为升级前的版本标签或事先记录的 digest。

打开 [http://localhost:8080/setup](http://localhost:8080/setup) 创建管理员账号。数据与日志保存在 `compose.yaml` 旁的 `./data` 目录（日志在 `./data/logs`）。`docker compose down` 保留该目录，`docker compose down -v` 不会删除绑定目录；确实要清空时手动删除 `./data`。

示例默认使用 host 网络，这是在 Linux 上访问宿主机服务（例如本地代理）最简单的方式。在 Docker Desktop 上，或希望改用 bridge 网络时，请注释掉 `network_mode: host` 并取消 `ports` 映射的注释；访问宿主机请用 `host.docker.internal` 而不是 `127.0.0.1`。

```bash
docker compose logs -f windwatch
docker compose ps
docker compose down
```

本地 Docker 构建默认使用标准 Go 模块代理；镜像会嵌入 `VERSION` 文件中的版本和当前 commit，可用 `VERSION`、`COMMIT`、`BUILD_TIME` 构建参数覆盖。如需镜像源，可给 `make docker-build` 传入 `GOPROXY`，或使用相应的 `--build-arg`。服务默认使用只读根文件系统、16 MB 的 `/tmp` tmpfs、丢弃全部 capabilities、`no-new-privileges`、128 MB 内存限制和 `GOMEMLIMIT=96MiB`；如有需要可在 [compose.example.yaml](compose.example.yaml) 或本地副本中调整。如果修改容器内监听地址，请同时更新 `WINDWATCH_HEALTH_URL`。

Alpine 运行时包含 shell；请使用 `docker compose logs` 查看输出，并直接复制 `./data` 目录备份。如果容器前有反向代理，请把 `WINDWATCH_TRUSTED_PROXIES` 设为代理地址或网段。

Docker 容器监控默认关闭。需要时取消 socket 挂载和 `group_add` 的注释，并把 `DOCKER_GID` 设为 socket 的组 ID（`stat -c '%g' /var/run/docker.sock`）。挂载 Docker socket 会让 WindWatch 获得较高的宿主机控制权限。

Dockerfile 支持 `linux/amd64` 和 `linux/arm64`。安装 Buildx（跨架构构建还需要 QEMU/binfmt）后可以为指定架构构建：

```bash
docker buildx build --platform linux/arm64 --load -t windwatch:arm64 .
```

### 单文件二进制（Linux amd64 和 arm64）

单文件只提供 Linux x86-64（`amd64`）和 ARM64（`aarch64`）版本。其他平台请使用容器镜像。构建需要 Go 1.27 或更高版本以及 `make`。

正式发布同时提供 `windwatch-linux-amd64-vX.Y.Z.tar.gz`、`windwatch-linux-arm64-vX.Y.Z.tar.gz` 和单一 `SHA256SUMS`，可从 [GitHub Releases 页面](https://github.com/fynhq/windwatch/releases)下载。可执行 `sha256sum -c --strict SHA256SUMS` 校验，解压对应架构的压缩包，并用 `windwatch version` 查看当前版本。

构建两个二进制和校验文件：

```bash
make dist
```

输出：

```text
dist/windwatch-linux-amd64-v0.2.0.tar.gz
dist/windwatch-linux-arm64-v0.2.0.tar.gz
dist/SHA256SUMS
```

如果模块下载受阻，请在构建前把 `GOPROXY` 设为可访问的 Go 模块代理。可用 `cd dist && sha256sum -c --strict SHA256SUMS` 校验压缩包。每个压缩包内是一个静态链接的 `windwatch` 二进制，无运行时依赖；界面、迁移、语言包和时区数据都已内嵌。HTTPS 检查使用系统 CA 证书，因此最小化系统需要安装 `ca-certificates` 包。`make dist` 会先执行格式检查、vet 和测试。版本号从仓库 `VERSION` 文件读取；本地构建可用 `VERSION=0.2.0` 覆盖。

在 systemd 主机上安装时，按 `uname -m` 选择对应二进制：

```bash
tar -xzf dist/windwatch-linux-amd64-v$(cat VERSION).tar.gz -C /tmp
sudo install -m 0755 /tmp/windwatch /usr/local/bin/windwatch
sudo useradd --system --user-group --home-dir /var/lib/windwatch --shell /usr/sbin/nologin windwatch
sudo install -d -o windwatch -g windwatch -m 0750 /var/lib/windwatch
sudo install -m 0644 deploy/windwatch.service /etc/systemd/system/windwatch.service
sudo systemctl daemon-reload
sudo systemctl enable --now windwatch
```

打开 [http://localhost:8080/setup](http://localhost:8080/setup)。日志写入 journal：

```bash
journalctl -u windwatch -f
```

该 unit 使用 `/var/lib/windwatch` 保存数据并监听 `:8080`。如需修改监听地址，可调整 `ExecStart` 或设置 `WINDWATCH_ADDR`。启用 Docker 监控时，取消 `SupplementaryGroups=docker` 的注释，并确保 `windwatch` 用户可以访问 Docker socket。

升级时先备份数据目录，停止服务，替换二进制后重新启动；数据库迁移会在启动时自动执行。升级后运行 `windwatch version` 确认版本。不使用 systemd 时可以直接运行：

```bash
./windwatch serve --addr :8080 --data-dir ./data
```

单文件二进制可以复制到任意位置；数据目录必须放在持久化存储上，并且不要让两个实例共用同一个数据目录。

## 配置

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--addr` | `WINDWATCH_ADDR` | `:8080` | HTTP 监听地址 |
| `--data-dir` | `WINDWATCH_DATA_DIR` | `./data` | 持久化数据目录 |
| `--log-level` | `WINDWATCH_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `--log-dir` | `WINDWATCH_LOG_DIR` | `<data-dir>/logs` | 每日日志文件目录；按天轮转，保留最近 30 天 |
| `--trusted-proxies` | `WINDWATCH_TRUSTED_PROXIES` | 不设置 | 逗号分隔的可信代理 IP 或 CIDR，只有这些来源的 `X-Forwarded-*` 会被信任 |
| — | `WINDWATCH_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | `windwatch healthcheck` 请求的地址 |
| — | `DOCKER_HOST` | Linux 为 `unix:///var/run/docker.sock`，Windows 为 `npipe:////./pipe/docker_engine` | Docker Engine 端点；支持 `unix://`、`tcp://` 和 Windows `npipe://` |
| — | `GOMEMLIMIT` | 不设置 | Go 软内存上限；在内存受限的容器中有用 |

业务配置——用户、公开状态页、通知方式、出站代理和监控定义——都在 Web 界面中管理并保存在 SQLite 中，不使用配置文件。

## 监控

每个监控都有检查间隔、超时、失败阈值和恢复阈值。默认值为 60 秒间隔、10 秒超时、连续失败 2 次判定 `DOWN`、连续成功 1 次恢复。检查间隔范围为 20 秒到 24 小时；超时范围为 1 秒到 5 分钟，且必须小于检查间隔。每个监控选择哪些通知方式接收告警，也可以暂停通知而不丢失选择；检查和调度行为不变。

### HTTP(S)

支持 `GET` 和 `HEAD` 请求、期望状态码或范围（默认 `200-299`）、响应体关键字、跳转控制，以及 TLS 证书校验（默认开启）。

对于 HTTPS 地址，还可以启用两项附加检查：

- **证书到期**：当证书在设定天数内到期时告警，默认提前 14 天。
- **域名到期**：当注册域名在设定天数内到期时告警，默认提前 30 天。域名从配置的 URL 中提取，不跟随跳转目标。该检查要求是公共域名并依赖 RDAP 数据；IP 地址和单标签主机名会被拒绝，且 RDAP 覆盖率因顶级域而异。域名查询失败只会在详情页显示信息，不会改变监控状态。

“跳过 TLS 校验”只建议用于使用自签名证书的内网服务。

### TCP 端口

TCP 监控检查 `host:port` 是否可以建立连接。原生部署时可以用它监控本地代理的监听端口，例如 `127.0.0.1:7890`；运行在容器中时，`127.0.0.1` 指容器自身，应改用容器可访问的地址。它只验证端口可达，不能验证代理能否正常访问外部网络；如果需要验证代理出口，请创建 HTTP 监控并选择该代理。

### Docker 容器

Docker 监控按容器名称或 ID 检查容器，可以额外要求容器处于 Docker healthy 状态。Docker Engine 端点来自 `DOCKER_HOST`，支持 Unix socket、`tcp://` 端点和 Windows named pipe（`npipe:////./pipe/docker_engine`）。Linux 默认使用 Unix socket，Windows 默认使用 named pipe。named pipe 需要 Windows 构建；正式单文件只提供 Linux 版本，Windows 用户需要自行构建或改用 `tcp://` 端点。Docker 检查始终直连，不受出站代理设置影响。

把 `/var/run/docker.sock` 挂载进容器，相当于把 Docker 宿主机的较高控制权限交给 WindWatch。只有确实需要 Docker 监控时才挂载，并限制该 socket 的访问范围。

### Push / 心跳

创建 Push 监控后，详情页会显示一个秘密心跳 URL：

```bash
curl -fsS https://windwatch.example.com/api/push/REPLACE_WITH_TOKEN
```

支持 `GET` 和 `POST`。Token 是唯一的凭据，同一个有效 Token 每秒最多接受一次请求，未认证请求另有按客户端 IP 的总量限制。首次心跳到达前监控保持 `PENDING`；如果超过“心跳间隔 + 宽限期”仍未收到心跳，则判定为 `DOWN`。

心跳 URL 本身就是密钥：不要把它写进公开日志，也不要让反向代理记录完整请求路径。

### 出站代理

可以在**设置 → 出站代理**中配置 HTTP 或 SOCKS5 代理，支持可选的用户名和密码，代理名称不能重复。每个 HTTP 或 TCP 监控以及每个通知方式都可以选择代理。Docker 和 Push 检查始终直连。如果选定的代理不可用，检查会直接失败，不会静默回退到直连。代理连接由 WindWatch 进程发起：容器内的 `127.0.0.1` 指容器自身，需要填写容器可访问的代理地址；Linux 上也可以让容器使用 host 网络。

## 通知

WindWatch 会在以下情况发送通知：`DOWN`、恢复、证书临期和域名临期。

通知方式在**设置 → 通知**中管理，可以添加任意数量，名称不能重复：

- **Telegram**：Bot Token 加一个或多个管理员 Chat ID；每个通知方式是一个独立的 Bot。
- **Webhook**：`POST` 地址，格式可选通用、钉钉、企微或飞书。通用格式发送版本化 JSON，其他格式发送文本消息并校验平台返回码；通用、钉钉、飞书可配置签名密钥，企微不使用密钥。

每个通知方式都可以单独选择出站代理和通知语言，并有**测试**按钮。每个监控选择哪些通知方式接收告警；新建监控时默认选中所有已启用的方式。关闭某个监控的通知开关会暂停发送，但保留选择。重复提醒在监控项的通知设置中配置：故障提醒适用于所有监控类型，证书/域名警告提醒只在 HTTP 监控中显示。通用格式的 Webhook 请求体包含事件类型、监控状态、监控信息、消息、文本和时间戳；配置签名密钥后，通用格式附带 `X-WindWatch-Signature: sha256=<hex>`，钉钉和飞书使用平台的时间戳与签名字段。

发送最多尝试三次（包含首次发送），并按通知方式和目标对重复事件去重；**设置 → 发送记录**显示最近 100 条投递记录、触发监控和发送内容（弹窗查看），记录保留 365 天。通知语言独立于界面语言。

## 数据与备份

运行时数据保存在配置的数据目录中：

```text
data/
├── windwatch.db
├── windwatch.db-wal
├── windwatch.db-shm
├── secret.key
└── logs/
    └── windwatch-YYYY-MM-DD.log
```

`secret.key` 用于加密代理密码、Push Token 和通知凭据等敏感配置。请妥善保管，并始终与数据库一起备份：密钥一旦丢失，这些加密字段将无法恢复。备份时先停止 WindWatch，再复制整个数据目录（含 `logs/`）；恢复时把数据库和匹配的密钥文件一起放回原位置。

使用 Docker Compose 时，数据与日志在 `compose.yaml` 旁的 `./data` 目录。Linux 下备份前先停止服务栈，恢复时保持文件属主为 UID 65532（目录 0700）；Docker Desktop 的共享目录会映射给容器用户。`docker compose down` 保留目录，`down -v` 不会删除绑定目录。旧部署如果使用命名卷，可停止服务栈后执行 `docker cp windwatch:/data/. ./data/`，Linux 下再执行 `sudo chown -R 65532:65532 data`，然后切换挂载。

数据库迁移会在启动时自动执行。升级前请先备份数据目录；系统不支持自动降级。

原始检查结果保留 7 天，30 分钟聚合保留 365 天，事件历史最多保留 365 天。通知投递日志保留 365 天，待发送记录由启动恢复处理；过期会话和陈旧的证书/域名缓存会定期清理。界面展示 24 小时、7 天、30 天和 90 天可用率；长期可用率和图表使用 30 分钟聚合，图表按配置时区归入本地日，存储仍使用 UTC。

## 时区

在**设置 → 常规**中选择 IANA 时区，默认 `UTC`；在输入框中键入名称即可筛选。该设置控制界面时间、通知时间和日图边界。长期可用率和图表使用 30 分钟聚合；升级时会删除旧的小时/日聚合记录。

## 账号与权限

首次在 `/setup` 创建的账号是管理员。管理员可以在**用户**页创建、停用、删除账号和重置密码。系统不支持自助注册、邮件邀请或 OIDC。

- **管理员**：可以管理监控、代理、通知、用户和公开状态页。
- **观察者**：可以登录并查看仪表盘、监控详情和事件，但不能修改配置，也看不到 Push Token。

系统始终至少保留一个启用的管理员。停用账号或重置密码会让该账号的现有会话失效。

## 公开状态页

在**设置 → 公开页**中启用页面、填写标题和可选说明，并勾选要公开的监控。页面地址为 `/status`，无需登录，每 30 秒局部刷新服务端渲染的内容；设置页会显示完整地址，点击即可复制。

页面只展示监控名称、类型、当前状态、24 小时、7 天、30 天和 90 天可用率，以及逐日可用率条（桌面最近 90 天，手机默认最近 30 天）；暂停监控单独计数，不会把总体状态变成“需要关注”。页面不会暴露目标 URL、监控配置、通知设置或密钥。启用期间页面禁止搜索引擎索引；关闭后 `/status` 返回 404。

## 密码恢复

管理员无法登录时，停止服务后运行：

```bash
windwatch reset-password --data-dir /var/lib/windwatch
```

命令会隐藏输入并提示确认新密码；脚本可使用 `--password-stdin`。存在多个账号时，需要通过 `--username NAME` 指定账号。重置会重新启用该账号、替换密码哈希并注销它的全部会话。操作前请先备份数据目录，并停止服务以避免并发写入。

## 可信反向代理

只有当请求的直接来源位于 `WINDWATCH_TRUSTED_PROXIES`（IP 或 CIDR）中时，WindWatch 才会信任 `X-Forwarded-For`、`X-Forwarded-Proto` 和 `X-Forwarded-Host`。有效客户端地址用于登录限流；转发协议和主机用于 Secure Cookie 和 Push 心跳 URL。

只应列出你自己控制的反向代理地址，不要填写客户端网段。未配置时所有转发头都会被忽略，直接使用连接来源地址。

## 安全与运维

- 一个数据目录只运行一个实例。WindWatch 不是分布式监控系统，不支持多个副本共享同一个 SQLite 数据库。
- 内置服务只提供 HTTP。请使用 Caddy、Nginx 或 Traefik 等反向代理终止 TLS，并避免在未认证的情况下暴露管理后台。
- 使用非特权用户运行，只授予数据目录写权限。容器使用 UID 65532，systemd unit 使用专用用户。数据目录和 `secret.key` 不应被其他用户读取。
- 心跳 URL 是 bearer token。请确保反向代理和访问日志不会记录完整路径。
- 如果由反向代理终止 TLS，请配置代理 CIDR，并在分享心跳 URL 前确认界面中显示的协议和主机正确。
- 公开状态页会暴露你选定监控的名称、状态和可用率，请把监控名称视为公开信息。
- 观察者可以查看监控目标、证书/域名信息和事件；只应把观察者角色授予可以接触这些信息的人。
- `/healthz` 会报告进程、数据库状态和构建版本，不检查调度器是否卡死；`windwatch healthcheck` 适合用于 systemd 或容器健康检查。
- 当前不包含内置证书申请功能。

## 开发

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/windwatch
```

Makefile 提供相同的检查和部署构建：`make check`、`make race`、`make version`、`make dist`、`make docker-build`。

测试使用本地替身覆盖 HTTP、TCP、Docker、RDAP、Telegram 和代理，不需要外部账号；同时覆盖用户角色、公开页白名单、可信代理头、30 分钟聚合和 Windows npipe 路径解析。SQLite 使用纯 Go 的 `modernc.org/sqlite` 驱动，因此支持 `CGO_ENABLED=0` 构建。

| 路径 | 职责 |
|---|---|
| `cmd/windwatch` | 入口和子命令 |
| `internal/app` | 依赖组装和生命周期 |
| `internal/web` | HTTP 处理器、模板和静态资源 |
| `internal/scheduler` | 检查调度、状态转换和保留清理 |
| `internal/checker` | HTTP、TCP 和 Docker 探测 |
| `internal/notify` | Telegram 和 Webhook 投递 |
| `internal/store` | SQLite 查询、迁移和历史数据 |
| `internal/i18n` | 英文和简体中文文案 |
| `internal/proxy` | HTTP 和 SOCKS5 出站拨号 |

## 开源协议

基于 MIT 许可发布，见 [LICENSE](LICENSE)。
