import { Link } from 'react-router-dom'
import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import {
  ApiError,
  deleteFile,
  getCurrentUser,
  getHealth,
  listFiles,
  logout,
  setFileStatus,
  uploadFile,
  type AuthUser,
  type FileRecord,
  type HealthResponse,
} from '../lib/api'
import { env } from '../lib/env'

type LoadState = 'idle' | 'loading' | 'success' | 'error'
type AuthState = 'idle' | 'loading' | 'authenticated' | 'unauthenticated' | 'error'
type FilesState = 'idle' | 'loading' | 'success' | 'error'

const milestones = [
  'M0-M7 已完成：骨架、登录、上传、下载、禁用/删除、正式域名、分享页、静态前端服务',
  'M8: 文件搜索、筛选、批量操作',
  'M9: 大文件分片上传 / 断点续传',
  'M10: S3 兼容对象存储切换能力',
  'M11: 细化权限与分享策略（过期、密码、单次下载）',
]

export function DashboardPage() {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [state, setState] = useState<LoadState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [viewer, setViewer] = useState<AuthUser | null>(null)
  const [authState, setAuthState] = useState<AuthState>('idle')
  const [authError, setAuthError] = useState<string | null>(null)
  const [files, setFiles] = useState<FileRecord[]>([])
  const [filesState, setFilesState] = useState<FilesState>('idle')
  const [filesError, setFilesError] = useState<string | null>(null)
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [displayName, setDisplayName] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadMessage, setUploadMessage] = useState<string | null>(null)
  const [fileActionBusyId, setFileActionBusyId] = useState<number | null>(null)
  const [fileActionMessage, setFileActionMessage] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    async function loadHealth() {
      setState('loading')
      try {
        const result = await getHealth()
        if (!cancelled) {
          setHealth(result)
          setState('success')
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Unknown error')
          setState('error')
        }
      }
    }

    async function loadViewer() {
      setAuthState('loading')
      try {
        const result = await getCurrentUser()
        if (!cancelled) {
          setViewer(result.user ?? null)
          setAuthError(null)
          setAuthState(result.user ? 'authenticated' : 'unauthenticated')
        }
      } catch (err) {
        if (cancelled) {
          return
        }

        if (err instanceof ApiError && err.status === 401) {
          setViewer(null)
          setAuthError('当前未登录')
          setAuthState('unauthenticated')
          return
        }

        setViewer(null)
        setAuthError(err instanceof Error ? err.message : 'Unknown error')
        setAuthState('error')
      }
    }

    void loadHealth()
    void loadViewer()

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (authState !== 'authenticated') {
      setFiles([])
      setFilesState('idle')
      return
    }

    let cancelled = false

    async function loadFiles() {
      setFilesState('loading')
      try {
        const result = await listFiles()
        if (!cancelled) {
          setFiles(result.items ?? [])
          setFilesError(null)
          setFilesState('success')
        }
      } catch (err) {
        if (!cancelled) {
          setFiles([])
          setFilesError(err instanceof Error ? err.message : 'Unknown error')
          setFilesState('error')
        }
      }
    }

    void loadFiles()

    return () => {
      cancelled = true
    }
  }, [authState])

  async function handleLogout() {
    try {
      await logout()
      setViewer(null)
      setAuthError('当前未登录')
      setAuthState('unauthenticated')
      setFiles([])
      setFilesState('idle')
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : 'Unknown error')
      setAuthState('error')
    }
  }

  async function handleUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selectedFile) {
      setUploadMessage('请选择要上传的文件')
      return
    }

    setUploading(true)
    setUploadMessage(null)
    try {
      const result = await uploadFile(selectedFile, displayName)
      const uploaded = result.item
      setSelectedFile(null)
      setDisplayName('')
      setUploadMessage(uploaded ? `上传完成：${uploaded.displayName}` : '上传完成')

      const next = await listFiles()
      setFiles(next.items ?? [])
      setFilesState('success')
      setFilesError(null)
    } catch (err) {
      setUploadMessage(err instanceof Error ? err.message : '上传失败')
    } finally {
      setUploading(false)
    }
  }

  async function refreshFiles() {
    const next = await listFiles()
    setFiles(next.items ?? [])
    setFilesState('success')
    setFilesError(null)
  }

  async function handleToggleFileStatus(item: FileRecord) {
    const nextStatus = item.status === 'disabled' ? 'active' : 'disabled'
    setFileActionBusyId(item.id)
    setFileActionMessage(null)
    try {
      await setFileStatus(item.id, nextStatus)
      await refreshFiles()
      setFileActionMessage(nextStatus === 'disabled' ? `已禁用：${item.displayName}` : `已重新启用：${item.displayName}`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '更新文件状态失败')
    } finally {
      setFileActionBusyId(null)
    }
  }

  async function handleDeleteFile(item: FileRecord) {
    const shouldDelete = window.confirm(`确认删除文件「${item.displayName}」吗？此操作不可恢复。`)
    if (!shouldDelete) {
      return
    }

    setFileActionBusyId(item.id)
    setFileActionMessage(null)
    try {
      await deleteFile(item.id)
      await refreshFiles()
      setFileActionMessage(`已删除：${item.displayName}`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '删除文件失败')
    } finally {
      setFileActionBusyId(null)
    }
  }

  return (
    <div className="page-grid">
      <section className="card hero-card">
        <div className="hero-copy">
          <p className="eyebrow">Admin Console</p>
          <h2>管理员上传后，已经可以拿到正式 HTTPS 分享页与下载直链</h2>
          <p>
            当前管理台已经接通登录、上传、禁用、删除、公开分享页和正式公网域名，并连上
            <code>{env.apiBaseUrl}</code> 的健康检查。
          </p>
        </div>
        <div className="stat-grid">
          <article className="stat-card">
            <span className="stat-label">Web</span>
            <strong>React 19 + Vite 8</strong>
          </article>
          <article className="stat-card">
            <span className="stat-label">API</span>
            <strong>Go + chi skeleton</strong>
          </article>
          <article className="stat-card">
            <span className="stat-label">Storage</span>
            <strong>Local object storage first</strong>
          </article>
          <article className="stat-card">
            <span className="stat-label">Tunnel</span>
            <strong>Single domain via Cloudflare</strong>
          </article>
        </div>
      </section>

      <section className="card">
        <div className="section-title-row">
          <div>
            <p className="eyebrow">Auth Status</p>
            <h3>管理员会话</h3>
          </div>
          <span className={`status-pill status-${authState === 'authenticated' ? 'success' : authState === 'loading' ? 'loading' : authState === 'error' ? 'error' : 'idle'}`}>
            {authState}
          </span>
        </div>
        {authState === 'authenticated' && viewer ? (
          <div className="auth-summary">
            <p>
              当前登录用户：<strong>{viewer.username}</strong>（{viewer.role}）
            </p>
            <button type="button" className="ghost-button" onClick={handleLogout}>
              登出
            </button>
          </div>
        ) : null}
        {authState === 'unauthenticated' ? (
          <div className="auth-summary">
            <p>{authError}</p>
            <Link className="inline-link" to="/admin/login">
              前往登录
            </Link>
          </div>
        ) : null}
        {authState === 'error' ? <p className="error-text">{authError}</p> : null}
        {authState === 'loading' ? <p>正在检查管理员会话。</p> : null}
      </section>

      <section className="card">
        <div className="section-title-row">
          <div>
            <p className="eyebrow">Upload</p>
            <h3>上传文件</h3>
          </div>
          <span className={`status-pill status-${uploading ? 'loading' : 'idle'}`}>{uploading ? 'uploading' : 'ready'}</span>
        </div>
        {authState === 'authenticated' ? (
          <form className="upload-form" onSubmit={handleUpload}>
            <label>
              选择文件
              <input
                type="file"
                onChange={(event) => {
                  const file = event.target.files?.[0] ?? null
                  setSelectedFile(file)
                  if (file && displayName.trim() === '') {
                    setDisplayName(file.name)
                  }
                }}
              />
            </label>
            <label>
              展示名称
              <input
                type="text"
                placeholder="保持为空时默认使用原文件名"
                value={displayName}
                onChange={(event) => setDisplayName(event.target.value)}
              />
            </label>
            <button type="submit" disabled={uploading}>
              {uploading ? '上传中…' : '上传并生成分享链接'}
            </button>
          </form>
        ) : (
          <p>请先登录管理员账号后再上传文件。</p>
        )}
        {uploadMessage ? <p className="notice-text">{uploadMessage}</p> : null}
      </section>

      <section className="card">
        <div className="section-title-row">
          <div>
            <p className="eyebrow">Service Status</p>
            <h3>API 健康检查</h3>
          </div>
          <span className={`status-pill status-${state}`}>{state}</span>
        </div>
        {state === 'success' && health ? (
          <dl className="kv-grid">
            <div>
              <dt>Name</dt>
              <dd>{health.name}</dd>
            </div>
            <div>
              <dt>Version</dt>
              <dd>{health.version}</dd>
            </div>
            <div>
              <dt>Environment</dt>
              <dd>{health.environment}</dd>
            </div>
            <div>
              <dt>Storage Root</dt>
              <dd>{health.storageRoot}</dd>
            </div>
          </dl>
        ) : null}
        {state === 'loading' ? <p>正在请求健康检查接口。</p> : null}
        {state === 'error' ? <p className="error-text">{error}</p> : null}
      </section>

      <section className="card">
        <div className="section-title-row">
          <div>
            <p className="eyebrow">Files</p>
            <h3>文件列表</h3>
          </div>
          <span className={`status-pill status-${filesState === 'success' ? 'success' : filesState === 'loading' ? 'loading' : filesState === 'error' ? 'error' : 'idle'}`}>
            {filesState}
          </span>
        </div>
        {filesState === 'loading' ? <p>正在拉取文件列表。</p> : null}
        {filesState === 'error' ? <p className="error-text">{filesError}</p> : null}
        {fileActionMessage ? <p className="notice-text">{fileActionMessage}</p> : null}
        {files.length > 0 ? (
          <div className="files-list">
            {files.map((item) => (
              <article key={item.id} className="file-item">
                <div className="file-main">
                  <strong>{item.displayName}</strong>
                  <span>{formatBytes(item.size)} · {item.contentType}</span>
                  <span>状态：{item.status} · 下载次数：{item.downloadCount}</span>
                </div>
                <div className="file-links">
                  <a href={buildShareUrl(item.publicId, item.displayName)} target="_blank" rel="noreferrer">
                    打开分享页
                  </a>
                  <code>{buildShareUrl(item.publicId, item.displayName)}</code>
                  <a href={item.downloadUrl} target="_blank" rel="noreferrer">
                    打开下载直链
                  </a>
                  <code>{item.downloadUrl}</code>
                </div>
                <div className="file-actions">
                  <button
                    type="button"
                    className="ghost-button"
                    disabled={fileActionBusyId === item.id}
                    onClick={() => handleToggleFileStatus(item)}
                  >
                    {fileActionBusyId === item.id ? '处理中…' : item.status === 'disabled' ? '重新启用' : '禁用下载'}
                  </button>
                  <button
                    type="button"
                    className="danger-button"
                    disabled={fileActionBusyId === item.id}
                    onClick={() => handleDeleteFile(item)}
                  >
                    删除文件
                  </button>
                </div>
              </article>
            ))}
          </div>
        ) : filesState === 'success' ? (
          <p>还没有上传任何文件。</p>
        ) : null}
      </section>

      <section className="card">
        <p className="eyebrow">Milestones</p>
        <h3>推荐开发顺序</h3>
        <ol className="list-block ordered-list">
          {milestones.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ol>
      </section>

      <section className="card">
        <p className="eyebrow">Reserved Routes</p>
        <h3>已预留的路由</h3>
        <ul className="list-block">
          <li>`/admin` 管理台首页</li>
          <li>`/admin/login` 管理员登录页</li>
          <li>`/share/:publicId/:filename?` 公开分享页</li>
          <li>`/f/:publicId/:filename?` 下载直链</li>
          <li>`/api/v1/health` API 健康检查</li>
        </ul>
      </section>
    </div>
  )
}

function formatBytes(size: number): string {
  if (size < 1024) {
    return `${size} B`
  }

  const units = ['KB', 'MB', 'GB', 'TB']
  let value = size / 1024
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }

  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unitIndex]}`
}

function buildShareUrl(publicId: string, fileName: string): string {
  return new URL(`/share/${publicId}/${encodeURIComponent(fileName)}`, window.location.origin).href
}
