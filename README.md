<p align="center">
  <img src="./web/public/graydeck-logo.svg" alt="Graydeck logo" width="120" />
</p>

<h1 align="center">Graydeck</h1>

<p align="center">
  <code>mihomo</code> 的轻量配置、更新与面板管理工具
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white" alt="Go 1.24" />
  <img src="https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=white" alt="React 19" />
  <img src="https://img.shields.io/badge/Vite-8-646CFF?logo=vite&logoColor=white" alt="Vite 8" />
  <img src="https://img.shields.io/badge/Docker-blue?logo=docker&logoColor=white" alt="Docker" />
  <img src="https://img.shields.io/badge/license-MIT-green" alt="MIT" />
</p>

---

## 这是什么

Graydeck 把 `mihomo` 日常使用中最频繁的几个操作收进同一个 Web 控制台：管理远程订阅配置、更新核心和面板资源、查看运行状态与日志。它不做代理面板本身——而是让配置维护、版本更新和状态监控这些"运维动作"更顺手。

**适合谁用**：保留了 `mihomo` YAML 配置能力、以远程订阅为主要配置来源、希望用轻量 Web 界面替代手动维护的场景。

## 核心功能

<p align="center">
  <table>
    <tr>
      <td><strong>📡 订阅配置管理</strong></td>
      <td>多配置、手动同步与切换、YAML 预览、状态记录</td>
    </tr>
    <tr>
      <td><strong>🛡 配置校验</strong></td>
      <td>启动或切换前生成最终运行配置，交由 <code>mihomo</code> 做合法性校验</td>
    </tr>
    <tr>
      <td><strong>🔄 更新中心</strong></td>
      <td>自动检查并安装 <code>mihomo</code> 核心与 <code>Zashboard</code> 资源，支持自定义地址和上传本地文件</td>
    </tr>
    <tr>
      <td><strong>🧩 面板整合</strong></td>
      <td>内置 <code>Zashboard</code>，通过 Graydeck 同域接入，可按配置屏蔽设置菜单</td>
    </tr>
    <tr>
      <td><strong>⚙ 基础配置注入</strong></td>
      <td>通过 <code>base.yaml</code> 管理端口、<code>dns</code>、<code>tun</code> 等基础运行配置的覆盖与合并</td>
    </tr>
    <tr>
      <td><strong>📊 运行可观测</strong></td>
      <td>登录鉴权、运行状态控制、持久日志、按 IP 或用户查询周期流量与连接明细</td>
    </tr>
  </table>
</p>

## 快速开始

### Docker（推荐）

```bash
git clone https://github.com/ydfk/gray-deck.git
cd gray-deck
mkdir -p data
cp docker-compose.example.yml docker-compose.yml

# 启动前修改 config/graydeck.yaml 中的登录密码，
# 并修改 docker-compose.yml 中的 GRAYDECK_SECRET。
docker compose up -d
```

示例默认使用官方镜像 `ghcr.io/ydfk/graydeck:latest`。需要固定版本时，可在 `.env` 中设置 `GRAYDECK_IMAGE=ghcr.io/ydfk/graydeck:1.3.0`。从源码构建时，可以先运行 `docker build -t graydeck:local .`，再把 `GRAYDECK_IMAGE` 设为 `graydeck:local`。

管理页默认监听 `8080` 端口。示例 compose 文件还映射了常用代理端口：

| 宿主机 | 容器内 | 用途 |
| --- | --- | --- |
| `8080` | `8080` | Graydeck Web |
| `7890` | `17890` | mixed port |
| `7891` | `17891` | SOCKS |
| `7892` | `17892` | redir |
| `7893` | `17893` | tproxy |

> 只使用 HTTP/SOCKS 代理时可以移除 `NET_ADMIN` 和 `/dev/net/tun`；需要 TUN 或透明代理时保留。

#### 更新现有 Docker 实例

在原 Compose 目录操作，沿用原来的 `./data:/data` 和 `./config:/config` 挂载。旧版示例如果仍有 `build` 和占位镜像，请删除 `build` 段并把 `image` 改为 `ghcr.io/ydfk/graydeck:latest`。使用固定版本时，先调整镜像标签。

```bash
docker compose pull graydeck
docker compose up -d graydeck
```

首次升级到 `1.3.0` 时会自动迁移已有数据。升级前可先运行 `docker compose stop graydeck`，再备份原配置目录和数据目录。停止时会将缓冲中的流量和日志写入数据库。

### 非 Docker 安装与更新

#### Linux x86_64（Debian / Ubuntu，systemd）

一键下载最新版并安装为 `graydeck.service`，默认工作目录为 `/opt/graydeck`。需要 Bash、curl 和 systemd。

```bash
( script="$(mktemp)" && trap 'rm -f -- "$script"' EXIT &&
  curl -fsSL https://raw.githubusercontent.com/ydfk/gray-deck/main/scripts/install-debian-systemd.sh -o "$script" &&
  sudo bash "$script" install )
```

默认访问 `http://服务器地址:8080`。首次安装会生成 `admin` 的密码，脚本完成后会显示登录配置文件路径。可用 `systemctl status graydeck.service` 查看服务状态。

已有 systemd 安装可在控制台「Graydeck」一栏点击 **更新 Graydeck**。也可在原服务器运行以下命令，脚本从服务配置找到原可执行文件，保留原工作目录、配置和数据。

```bash
( script="$(mktemp)" && trap 'rm -f -- "$script"' EXIT &&
  curl -fsSL https://raw.githubusercontent.com/ydfk/gray-deck/main/scripts/install-debian-systemd.sh -o "$script" &&
  sudo bash "$script" update )
```

非默认服务名可在 sudo 后传入 `env GRAYDECK_SERVICE_NAME=自定义服务名`。手动运行 Linux 单文件的用户，可把最后一行改成 `bash "$script" update /原路径/graydeck-linux-amd64`，再从原工作目录启动。更新会保留一个 `.previous` 可执行文件备份。

#### Windows x64（PowerShell）

一键安装到 `%LOCALAPPDATA%\Graydeck` 并启动。默认访问 `http://localhost:8080`，首次安装会生成 `admin` 的密码，并显示登录配置文件路径。

```powershell
$script = Join-Path $env:TEMP ('graydeck-install-' + [Guid]::NewGuid().ToString('N') + '.ps1')
try {
    Invoke-WebRequest 'https://raw.githubusercontent.com/ydfk/gray-deck/main/scripts/install-standalone.ps1' -UseBasicParsing -OutFile $script -ErrorAction Stop
    powershell.exe -NoProfile -ExecutionPolicy Bypass -File $script -Mode Install
} finally {
    Remove-Item -LiteralPath $script -ErrorAction SilentlyContinue
}
```

更新时把 `-Mode Install` 改成 `-Mode Update`。之前手动安装在其他位置的实例，追加 `-BinaryPath 'D:\Graydeck\graydeck-windows-amd64.exe'`。如果原工作目录与程序目录不同，再传入 `-WorkingDirectory`。脚本会重启原先正在运行的实例，并保留原配置、数据和一份 `.previous` 可执行文件备份。

也可在 [GitHub Releases](https://github.com/ydfk/gray-deck/releases/latest) 下载带内置管理页的独立程序。手动更新时，退出原程序及其 Mihomo 核心，把新文件替换到原路径，再从原工作目录启动。

## 配置

**`config/graydeck.yaml`** — Graydeck 自身配置：

```yaml
server:
  port: 8080
auth:
  enabled: true
  username: admin
  password: admin123
update:
  prefer-proxy: true
  proxy-url: https://ghfast.top/
zashboard:
  hide-settings: true
```

**`config/base.yaml`** — 参与运行配置生成，适合声明端口、`external-controller`、`dns`、`tun` 等基础项。`base.yaml` 中的字段会覆盖订阅配置中的同名字段（列表字段以 `base.yaml` 为准，未声明的保留订阅原值）。

## 管理流程

1. 添加远程订阅配置
2. Graydeck 拉取订阅并生成最终运行配置
3. `mihomo` 校验通过后启动或切换到该配置
4. 在控制台管理核心版本、配置状态、日志和 `Zashboard` 面板

### 流量统计

在 Graydeck 导航栏打开 **流量统计**，选择当前或过去的周期，查看上传、下载和合计。可按来源 IP 汇总，也可按 Mihomo 代理认证用户名汇总，未认证的连接在用户视图中按 IP 显示。

默认周期从每月 1 日零点到次月 1 日零点。可在 `config/graydeck.yaml` 设置以下选项，重启 Graydeck 后生效。

```yaml
traffic:
  start: "15 08:30"
  end: "10 18:00"
  retention-months: 12
  max-size-mib: 100
```

- `start` 和 `end` 使用「日 HH:MM」格式。结束设定晚于开始时使用同月日期，其余情况使用次月日期。示例会从每月 15 日 08:30 统计到次月 10 日 18:00，间隔中的用量不进入该周期。省略 `end` 时，每期在下一期开始时截止。较短月份把超出的日期落到月底。
- 时区依次取 `GRAYDECK_TIMEZONE`、`TZ` 和系统时区。省略 `start` 时仍支持原来的 `GRAYDECK_TRAFFIC_CYCLE_DAY`。已有历史按新配置汇总，切换前未按新时刻分段的旧记录只能按原有时间粒度归入周期。
- 后台每秒采集，约每 15 秒保存。月度汇总和连接明细共用 `graydeck.sqlite`，默认数据库上限为 100 MiB，空间不足时优先淘汰最旧的连接明细。可配置保留 1–120 个周期及 1–256 MiB 的共享上限。SQLite 写入时会短暂使用事务日志。
- 面板日志存入同一数据库，默认保留最近 400 条。Compose 示例启用容器日志轮转，每个文件 10 MB、最多 3 个。已有容器需采用新配置重新创建后才会使用这组设置。
- 用量从启用后开始累计。Mihomo 只返回活跃连接，采样间隔内结束的连接和连接关闭前尚未采集的字节可能漏计。跨周期的字节增量按采到它的时间归入对应周期。

#### 连接明细

在流量统计下方按所选周期或全部保留记录查询，可按使用者、目标域名、IP、节点或规则筛选。每条记录显示连接的开始及最后采样时间、目标和端口、TCP/UDP 与入站类型、实际节点、策略组链路、命中规则和累计流量。有域名嗅探结果时也会显示。Mihomo 的连接接口不提供 HTTP 方法、完整 URL、请求头或正文。

点击汇总表中的 IP 或用户名进入 **用量分析**。可以从每日下钻到每小时、每分钟，再按协议或域名筛选，找出当前范围内占用流量最多的连接及所走节点。

明细采用共享保留周期和大小上限，不限制总条数。已有配置中显式填写的 `max-size-mib` 继续生效。旧的独立明细上限不再生效，`detail-retention-days: 0` 仍可停用明细采集和查询。旧版的月度合计和连接累计值无法还原为每分钟用量。

### 后台数据与升级

Graydeck 在数据目录中的 `graydeck.sqlite` 保存订阅清单和同步状态、流量汇总与明细、版本记录、面板日志和登录会话。Docker 数据目录为 `/data`。Graydeck 设置、Mihomo 基础配置、订阅 YAML 和生成的运行配置继续使用 YAML 文件。

升级后首次启动会读取旧的 `subscriptions.json`、`traffic-history.json`、`traffic-connections.jsonl` 或 `traffic-connections.sqlite`。已迁移的记录之后由 `graydeck.sqlite` 读写。旧文件会保留供回滚使用，并单独占用磁盘空间。旧版登录会话保存在内存中，首次升级后需重新登录。

备份时可先停止服务，再复制数据目录和配置目录。单文件部署使用原数据目录升级即可自动迁移，Docker 升级时沿用原有 `/data` 卷。

## 技术栈

| 层 | 技术 |
| --- | --- |
| 后端 | Go 1.24 + SQLite |
| 前端 | React 19 + TypeScript + Vite 8 |
| 面板 | 内置 Zashboard 静态资源 |
| 部署 | Docker / 单文件 standalone |

## 开发

```bash
# 后端
go run ./cmd/graydeck

# 前端（开发模式）
cd web && npm run dev
```

项目设计记录见 [DOCKER_IMAGE_DESIGN.md](./DOCKER_IMAGE_DESIGN.md)，前端样式规范见 [web/DESIGN.md](./web/DESIGN.md)。
