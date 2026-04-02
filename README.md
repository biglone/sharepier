# SharePier

`SharePier` 是一个自托管文件上传与分享系统，目标是让管理员上传文件后，立即生成稳定的公网下载链接。

## 当前骨架

- `sharepier-web`: `React + Vite + TypeScript` 管理台、搜索/筛选/批量操作与公开分享页，当前走静态构建产物服务
- `sharepier-api`: `Go + chi` API、管理员登录、普通上传、分片上传/续传、列表、禁用、删除、公开下载、公开文件元数据
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

- `postgres` 服务复用本地已有的 `golang:1.26.1-alpine` 镜像，并在容器启动时安装 PostgreSQL 运行时，绕开某些环境下 `dockerproxy.com` / Docker Hub mirror 拉取 `postgres:17-alpine` 失败的问题
- 运行时安装默认使用清华 Alpine 镜像源，可通过 `SHAREPIER_ALPINE_MAIN_REPOSITORY` / `SHAREPIER_ALPINE_COMMUNITY_REPOSITORY` 覆盖
- `sharepier-web` 默认使用 `npm ci`，并通过 `NPM_CONFIG_REGISTRY` 指向 `https://registry.npmmirror.com`，减少容器内首次安装依赖耗时
- `sharepier-web` 使用 `node:22-alpine`，容器启动后会先构建静态资源，再用 Node 静态文件服务对外提供页面
- 默认普通上传入口受 `SHAREPIER_MAX_UPLOAD_SIZE` 控制；大文件分片上传走独立会话，单片大小由 `SHAREPIER_RESUMABLE_CHUNK_SIZE` 控制，会话过期时间由 `SHAREPIER_UPLOAD_SESSION_TTL` 控制

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

## 下一步

- 将管理员密码替换成你自己的长期密码
- 引入 S3 兼容对象存储
