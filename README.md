# SharePier

`SharePier` 是一个自托管文件上传与分享系统，目标是让管理员上传文件后，立即生成稳定的公网下载链接。

## 当前骨架

- `sharepier-web`: `React + Vite + TypeScript` 管理台、搜索/筛选/批量操作、分享策略配置、审计/下载操作记录与公开分享页，当前走静态构建产物服务
- `sharepier-api`: `Go + chi` API、管理员登录、普通上传、分片上传/续传、列表、禁用、删除、公开下载、公开文件元数据、分享策略鉴权、审计日志，以及 `local/S3-compatible` 对象存储后端
- `docker-compose.yml`: 本地开发依赖与容器化运行入口
- `cloudflared/config.yml.example`: Tunnel 配置示例
- `sharepier-api/migrations/0001_init.sql`: 首版数据库表结构草稿

## 项目结构

```text
sharepier/
  sharepier-web/
  sharepier-api/
  cloudflared/
  data/
  docker-compose.yml
  .env.example
```

## 快速开始

### 方式一：全部走 Docker Compose

```bash
docker compose up --build
```

如果你本机通过代理访问外网：

- 当前 `docker-compose.yml` 会把代理变量透传给 `sharepier-api` 和 `sharepier-web`
- 如果你的代理只监听宿主机 `127.0.0.1:7890`，容器内不能直接访问这个地址
- 这时请优先设置：
- `DOCKER_HTTP_PROXY=http://host.docker.internal:7890`
- `DOCKER_HTTPS_PROXY=http://host.docker.internal:7890`
- `DOCKER_ALL_PROXY=socks5://host.docker.internal:7890`

Compose 已为容器注入 `host.docker.internal -> host-gateway`，便于这种场景下复用宿主机代理。

额外说明：

- `postgres` 服务直接使用官方 `postgres:17-alpine` 镜像，数据仍持久化到 `./data/postgres`
- 如果本机 Docker 需要代理拉取镜像，优先设置 `DOCKER_HTTP_PROXY` / `DOCKER_HTTPS_PROXY` / `DOCKER_ALL_PROXY`，必要时先手动执行 `docker pull postgres:17-alpine`
- `sharepier-api` 容器默认使用 `DOCKER_GOPROXY=https://goproxy.cn,direct`，避免容器内拉取较大 Go 依赖时频繁遇到 `unexpected EOF`
- `sharepier-web` 默认使用 `npm ci`，并通过 `NPM_CONFIG_REGISTRY` 指向 `https://registry.npmmirror.com`，减少容器内首次安装依赖耗时
- `sharepier-web` 使用 `node:22-alpine`，容器启动后会先构建静态资源，再用 Node 静态文件服务对外提供页面
- 默认普通上传入口受 `SHAREPIER_MAX_UPLOAD_SIZE` 控制；大文件分片上传走独立会话，单片大小由 `SHAREPIER_RESUMABLE_CHUNK_SIZE` 控制，会话过期时间由 `SHAREPIER_UPLOAD_SESSION_TTL` 控制
- 分享策略支持三类限制：失效时间、访问密码、最大下载次数；密码保护通过短期 HttpOnly Cookie 解锁，密钥由 `SHAREPIER_SHARE_ACCESS_SECRET` 提供
- 存储后端默认是 `local`；将 `SHAREPIER_STORAGE_BACKEND=s3` 后，可切换到任意兼容 `S3 API` 的对象存储（如 `MinIO`、`AWS S3`、`Cloudflare R2`）

启动后默认地址：

- Web: `http://localhost:3300`
- API: `http://localhost:38080/api/v1/health`
- PostgreSQL: `localhost:5432`
- 开发环境默认管理员：`admin / change-me-now`

### 方式二：前端本地，后端走 Docker

```bash
cd sharepier/sharepier-web
npm install
npm run dev
```

后端由于当前环境没有全局 `go`，建议继续走容器：

```bash
cd sharepier
docker compose up postgres sharepier-api
```

## Cloudflare Tunnel

推荐单域名方案，例如：

- 推荐：`sharepier.biglone.tech`
- 备选：`files.biglone.tech`

这样登录、后台管理、公开分享页和下载直链都走同一个 HTTPS 域名，避免跨域 Cookie 和多域名维护成本。

### 1. 准备公网环境变量

```bash
cp .env.public.example .env
```

然后至少把下面几项替换成你的正式域名：

- `SHAREPIER_ALLOWED_ORIGINS`
- `SHAREPIER_PUBLIC_BASE_URL`
- `VITE_API_BASE_URL`
- `SHAREPIER_ADMIN_PASSWORD`
- `SHAREPIER_SHARE_ACCESS_SECRET`

### 2. 准备 tunnel 配置

`cloudflared/config.yml.example` 已按单域名路径分流准备好：

- `/api/*` -> `sharepier-api`
- `/f/*` -> `sharepier-api`
- 其它路径（含 `/share/*`）-> `sharepier-web`

复制并替换成你的真实值：

```bash
cp cloudflared/config.yml.example cloudflared/config.yml
```

需要替换：

- `YOUR_TUNNEL_ID`
- `YOUR_TUNNEL_ID.json`
- `sharepier.example.com`

### 3. 启动服务

```bash
docker compose up -d postgres sharepier-api sharepier-web
docker compose --profile tunnel up -d cloudflared
```

### 4. 绑定域名到 tunnel

如果你决定使用 `sharepier.biglone.tech`，对应命令是：

```bash
cloudflared tunnel route dns <你的-tunnel-name-or-id> sharepier.biglone.tech
```

### 5. 验证

```bash
curl -I https://sharepier.biglone.tech
curl -I https://sharepier.biglone.tech/api/v1/health
```

## S3 兼容对象存储

如果你准备把对象文件切到 `MinIO / AWS S3 / Cloudflare R2`，核心环境变量如下：

- `SHAREPIER_STORAGE_BACKEND=s3`
- `SHAREPIER_S3_ENDPOINT`
- `SHAREPIER_S3_BUCKET`
- `SHAREPIER_S3_ACCESS_KEY_ID`
- `SHAREPIER_S3_SECRET_ACCESS_KEY`
- `SHAREPIER_S3_REGION`
- `SHAREPIER_S3_USE_SSL`
- `SHAREPIER_S3_USE_PATH_STYLE`
- `SHAREPIER_S3_PREFIX`

### 本地用 MinIO 验证

先启动一个本地兼容服务：

```bash
docker compose --profile s3 up -d minio
```

然后把 `.env` 改成类似：

```bash
SHAREPIER_STORAGE_BACKEND=s3
SHAREPIER_STORAGE_ROOT=/data/storage
SHAREPIER_S3_ENDPOINT=minio:9000
SHAREPIER_S3_BUCKET=sharepier
SHAREPIER_S3_ACCESS_KEY_ID=sharepierminio
SHAREPIER_S3_SECRET_ACCESS_KEY=sharepierminiosecret
SHAREPIER_S3_REGION=us-east-1
SHAREPIER_S3_USE_SSL=false
SHAREPIER_S3_USE_PATH_STYLE=true
SHAREPIER_S3_AUTO_CREATE_BUCKET=true
```

再重启 API：

```bash
docker compose up -d sharepier-api
```

说明：

- `SHAREPIER_STORAGE_ROOT` 在 `s3` 模式下仍然保留，用于本地上传会话缓存和临时工作目录
- 公开下载链接仍保持原来的 `/f/:publicId/:filename?`，不会因为切到对象存储而改路径
- 健康检查会返回 `storageBackend` 和 `storageLocation`，可用于确认当前实例实际连接到哪种存储

## 下一步

- 将管理员密码替换成你自己的长期密码
- 将 `SHAREPIER_SHARE_ACCESS_SECRET` 换成独立随机长串
- 增加审计日志筛选 / 导出能力
- 增加存储配额与用量统计
