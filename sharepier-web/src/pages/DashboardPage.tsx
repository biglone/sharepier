import { Link } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import {
  ApiError,
  batchUpdateFiles,
  completeResumableUpload,
  createResumableUploadSession,
  deleteFile,
  getCurrentUser,
  getHealth,
  getResumableUploadSession,
  listFiles,
  logout,
  setFileStatus,
  updateSharePolicy,
  uploadResumableChunk,
  uploadFile,
  type AuthUser,
  type FileRecord,
  type HealthResponse,
  type UploadSessionRecord,
} from '../lib/api'
import { env } from '../lib/env'

type LoadState = 'idle' | 'loading' | 'success' | 'error'
type AuthState = 'idle' | 'loading' | 'authenticated' | 'unauthenticated' | 'error'
type FilesState = 'idle' | 'loading' | 'success' | 'error'
type FileStatusFilter = 'all' | 'active' | 'disabled'

const milestones = [
  'M0-M10 已完成：骨架、登录、普通上传、分片上传/续传、搜索/筛选、批量操作、下载、禁用/删除、正式域名、分享页、静态前端服务、S3 兼容对象存储切换能力',
  'M11 已完成：分享策略（过期、密码、单次下载）',
  'M12: 审计日志与上传/下载操作记录页',
]

const resumableUploadStorageKey = 'sharepier.resumable-upload'

type SharePolicyDraft = {
  expiresAt: string
  downloadMode: 'unlimited' | 'single' | 'custom'
  maxDownloads: string
  passwordMode: 'keep' | 'clear' | 'set'
  password: string
}

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
  const [searchTerm, setSearchTerm] = useState('')
  const [statusFilter, setStatusFilter] = useState<FileStatusFilter>('all')
  const [selectedFile, setSelectedFile] = useState<File | null>(null)
  const [displayName, setDisplayName] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadMessage, setUploadMessage] = useState<string | null>(null)
  const [resumableFile, setResumableFile] = useState<File | null>(null)
  const [resumableDisplayName, setResumableDisplayName] = useState('')
  const [resumableUploading, setResumableUploading] = useState(false)
  const [resumableMessage, setResumableMessage] = useState<string | null>(null)
  const [resumableProgress, setResumableProgress] = useState(0)
  const [resumableSessionToken, setResumableSessionToken] = useState<string | null>(null)
  const [resumableSession, setResumableSession] = useState<UploadSessionRecord | null>(null)
  const [selectedFileIds, setSelectedFileIds] = useState<number[]>([])
  const [fileActionBusyId, setFileActionBusyId] = useState<number | null>(null)
  const [batchActionBusy, setBatchActionBusy] = useState(false)
  const [sharePolicyBusyId, setSharePolicyBusyId] = useState<number | null>(null)
  const [sharePolicyDrafts, setSharePolicyDrafts] = useState<Record<number, SharePolicyDraft>>({})
  const [fileActionMessage, setFileActionMessage] = useState<string | null>(null)

  const filteredFiles = useMemo(() => {
    const normalizedQuery = searchTerm.trim().toLowerCase()

    return files.filter((item) => {
      if (statusFilter !== 'all' && item.status !== statusFilter) {
        return false
      }

      if (normalizedQuery === '') {
        return true
      }

      return [
        item.displayName,
        item.originalName,
        item.publicId,
        item.contentType,
      ].some((value) => value.toLowerCase().includes(normalizedQuery))
    })
  }, [files, searchTerm, statusFilter])

  const allVisibleSelected =
    filteredFiles.length > 0 &&
    filteredFiles.every((item) => selectedFileIds.includes(item.id))

  const actionsBusy = batchActionBusy || fileActionBusyId !== null || sharePolicyBusyId !== null

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
      setSelectedFileIds([])
      setSharePolicyDrafts({})
      setFilesState('idle')
      return
    }

    let cancelled = false

    async function loadFiles() {
      setFilesState('loading')
      try {
        const result = await listFiles()
        if (!cancelled) {
          applyFiles(result.items ?? [])
        }
      } catch (err) {
        if (!cancelled) {
          setFiles([])
          setSelectedFileIds([])
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

  function applyFiles(nextFiles: FileRecord[]) {
    setFiles(nextFiles)
    setSelectedFileIds((current) => current.filter((id) => nextFiles.some((item) => item.id === id)))
    setSharePolicyDrafts((current) =>
      Object.fromEntries(
        Object.entries(current).filter(([key]) => nextFiles.some((item) => item.id === Number(key))),
      ) as Record<number, SharePolicyDraft>,
    )
    setFilesError(null)
    setFilesState('success')
  }

  async function refreshFiles() {
    const next = await listFiles()
    applyFiles(next.items ?? [])
  }

  function getSharePolicyDraft(item: FileRecord): SharePolicyDraft {
    return sharePolicyDrafts[item.id] ?? createSharePolicyDraft(item)
  }

  function updateSharePolicyDraft(fileId: number, patch: Partial<SharePolicyDraft>) {
    setSharePolicyDrafts((current) => {
      const base = current[fileId] ?? createSharePolicyDraft(files.find((item) => item.id === fileId))
      return {
        ...current,
        [fileId]: {
          ...base,
          ...patch,
        },
      }
    })
  }

  async function handleLogout() {
    try {
      await logout()
      setViewer(null)
      setAuthError('当前未登录')
      setAuthState('unauthenticated')
      setFiles([])
      setSelectedFileIds([])
      setSharePolicyDrafts({})
      setFilesState('idle')
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : 'Unknown error')
      setAuthState('error')
    }
  }

  async function handleSharePolicySubmit(item: FileRecord) {
    const draft = getSharePolicyDraft(item)
    let maxDownloads: number | null = null

    if (draft.downloadMode === 'single') {
      maxDownloads = 1
    } else if (draft.downloadMode === 'custom') {
      const parsed = Number.parseInt(draft.maxDownloads, 10)
      if (!Number.isFinite(parsed) || parsed <= 0) {
        setFileActionMessage(`文件 ${item.displayName} 的下载次数限制无效`)
        return
      }
      maxDownloads = parsed
    }

    setSharePolicyBusyId(item.id)
    setFileActionMessage(null)
    try {
      const result = await updateSharePolicy(item.id, {
        expiresAt: draft.expiresAt ? new Date(draft.expiresAt).toISOString() : null,
        maxDownloads,
        passwordMode: draft.passwordMode,
        password: draft.password,
      })

      if (result.item) {
        setFiles((current) => current.map((entry) => (entry.id === result.item?.id ? result.item : entry)))
        setSharePolicyDrafts((current) => ({
          ...current,
          [item.id]: createSharePolicyDraft(result.item),
        }))
      }
      setFileActionMessage(`已更新 ${item.displayName} 的分享策略`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '更新分享策略失败')
    } finally {
      setSharePolicyBusyId(null)
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

      await refreshFiles()
    } catch (err) {
      setUploadMessage(err instanceof Error ? err.message : '上传失败')
    } finally {
      setUploading(false)
    }
  }

  function handleResumableFileSelect(file: File | null) {
    setResumableFile(file)
    setResumableMessage(null)
    if (!file) {
      setResumableSessionToken(null)
      setResumableSession(null)
      setResumableProgress(0)
      return
    }

    if (resumableDisplayName.trim() === '') {
      setResumableDisplayName(file.name)
    }

    const stored = loadStoredResumableUpload()
    if (stored && stored.fingerprint === getFileFingerprint(file)) {
      setResumableSessionToken(stored.uploadToken)
      setResumableMessage('检测到未完成的大文件上传，会优先尝试断点续传。')
      return
    }

    setResumableSessionToken(null)
    setResumableSession(null)
    setResumableProgress(0)
  }

  async function resolveResumableSession(file: File): Promise<UploadSessionRecord> {
    const fingerprint = getFileFingerprint(file)
    const stored = loadStoredResumableUpload()
    const candidateToken =
      stored && stored.fingerprint === fingerprint ? stored.uploadToken : resumableSessionToken

    if (candidateToken) {
      try {
        const existing = await getResumableUploadSession(candidateToken)
        if (existing.session) {
          setResumableSessionToken(existing.session.uploadToken)
          setResumableSession(existing.session)
          setResumableProgress(buildUploadProgress(existing.session.receivedSize, existing.session.totalSize))
          persistResumableUpload({
            uploadToken: existing.session.uploadToken,
            fingerprint,
            displayName: resumableDisplayName.trim() || file.name,
          })
          return existing.session
        }
      } catch (err) {
        if (err instanceof ApiError && (err.status === 404 || err.status === 410)) {
          clearStoredResumableUpload()
          setResumableSessionToken(null)
          setResumableSession(null)
        } else {
          throw err
        }
      }
    }

    const created = await createResumableUploadSession({
      originalName: file.name,
      displayName: resumableDisplayName.trim() || file.name,
      contentType: file.type || 'application/octet-stream',
      totalSize: file.size,
    })
    if (!created.session) {
      throw new Error('上传会话创建成功，但服务端没有返回会话信息')
    }

    setResumableSessionToken(created.session.uploadToken)
    setResumableSession(created.session)
    setResumableProgress(0)
    persistResumableUpload({
      uploadToken: created.session.uploadToken,
      fingerprint,
      displayName: resumableDisplayName.trim() || file.name,
    })

    return created.session
  }

  async function handleResumableUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!resumableFile) {
      setResumableMessage('请选择要分片上传的文件')
      return
    }

    setResumableUploading(true)
    setResumableMessage(null)

    try {
      const file = resumableFile
      let session = await resolveResumableSession(file)
      let offset = session.receivedSize
      setResumableProgress(buildUploadProgress(offset, file.size))

      while (offset < file.size) {
        const nextChunk = file.slice(offset, offset + session.chunkSize)
        const result = await uploadResumableChunk(session.uploadToken, offset, nextChunk)
        if (!result.session) {
          throw new Error('分片已上传，但服务端没有返回最新会话状态')
        }

        session = result.session
        offset = session.receivedSize
        setResumableSession(session)
        setResumableProgress(buildUploadProgress(offset, file.size))
      }

      const completed = await completeResumableUpload(session.uploadToken)
      await refreshFiles()
      clearStoredResumableUpload()
      setResumableSessionToken(null)
      setResumableSession(null)
      setResumableProgress(0)
      setResumableFile(null)
      setResumableDisplayName('')
      setResumableMessage(completed.item ? `分片上传完成：${completed.item.displayName}` : '分片上传完成')
    } catch (err) {
      setResumableMessage(err instanceof Error ? `${err.message}。会话已保留，可继续续传。` : '分片上传失败，会话已保留。')
    } finally {
      setResumableUploading(false)
    }
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
      setSelectedFileIds((current) => current.filter((id) => id !== item.id))
      setFileActionMessage(`已删除：${item.displayName}`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '删除文件失败')
    } finally {
      setFileActionBusyId(null)
    }
  }

  async function handleBatchSetStatus(status: 'active' | 'disabled') {
    if (selectedFileIds.length === 0) {
      return
    }

    setBatchActionBusy(true)
    setFileActionMessage(null)
    try {
      const result = await batchUpdateFiles('set_status', selectedFileIds, status)
      const affectedCount = result.affectedCount ?? selectedFileIds.length
      await refreshFiles()
      setSelectedFileIds([])
      setFileActionMessage(status === 'disabled' ? `已批量禁用 ${affectedCount} 个文件` : `已批量启用 ${affectedCount} 个文件`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '批量更新文件状态失败')
    } finally {
      setBatchActionBusy(false)
    }
  }

  async function handleBatchDelete() {
    if (selectedFileIds.length === 0) {
      return
    }

    const shouldDelete = window.confirm(`确认批量删除 ${selectedFileIds.length} 个文件吗？此操作不可恢复。`)
    if (!shouldDelete) {
      return
    }

    setBatchActionBusy(true)
    setFileActionMessage(null)
    try {
      const result = await batchUpdateFiles('delete', selectedFileIds)
      const affectedCount = result.affectedCount ?? selectedFileIds.length
      await refreshFiles()
      setSelectedFileIds([])
      setFileActionMessage(`已批量删除 ${affectedCount} 个文件`)
    } catch (err) {
      setFileActionMessage(err instanceof Error ? err.message : '批量删除文件失败')
    } finally {
      setBatchActionBusy(false)
    }
  }

  function handleToggleFileSelection(fileID: number) {
    setSelectedFileIds((current) =>
      current.includes(fileID) ? current.filter((id) => id !== fileID) : [...current, fileID],
    )
  }

  function handleToggleVisibleSelection() {
    const visibleIDs = filteredFiles.map((item) => item.id)
    setSelectedFileIds((current) => {
      const next = new Set(current)
      if (allVisibleSelected) {
        visibleIDs.forEach((fileID) => next.delete(fileID))
      } else {
        visibleIDs.forEach((fileID) => next.add(fileID))
      }

      return Array.from(next)
    })
  }

  function resetFilters() {
    setSearchTerm('')
    setStatusFilter('all')
  }

  return (
    <div className="page-grid">
      <section className="card hero-card">
        <div className="hero-copy">
          <p className="eyebrow">Admin Console</p>
          <h2>管理员上传后，已经可以直接搜索、筛选并批量管理正式 HTTPS 分享文件</h2>
          <p>
            当前管理台已经接通登录、上传、搜索筛选、批量操作、公开分享页和正式公网域名，并连上
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
            <p className="eyebrow">Resumable Upload</p>
            <h3>大文件分片上传 / 断点续传</h3>
          </div>
          <span className={`status-pill status-${resumableUploading ? 'loading' : resumableSessionToken ? 'success' : 'idle'}`}>
            {resumableUploading ? 'uploading' : resumableSessionToken ? 'resumable' : 'ready'}
          </span>
        </div>
        {authState === 'authenticated' ? (
          <form className="upload-form" onSubmit={handleResumableUpload}>
            <label>
              选择大文件
              <input
                type="file"
                onChange={(event) => handleResumableFileSelect(event.target.files?.[0] ?? null)}
              />
            </label>
            <label>
              展示名称
              <input
                type="text"
                placeholder="保持为空时默认使用原文件名"
                value={resumableDisplayName}
                onChange={(event) => setResumableDisplayName(event.target.value)}
              />
            </label>

            <p className="subtle-text">
              适合 Cloudflare Tunnel 场景下的大文件上传。服务端会按推荐分片大小顺序接收，并在失败后从已接收偏移继续。
            </p>

            {resumableSession ? (
              <dl className="kv-grid compact-kv-grid">
                <div>
                  <dt>Upload Token</dt>
                  <dd>{resumableSession.uploadToken}</dd>
                </div>
                <div>
                  <dt>Chunk Size</dt>
                  <dd>{formatBytes(resumableSession.chunkSize)}</dd>
                </div>
                <div>
                  <dt>Received</dt>
                  <dd>{formatBytes(resumableSession.receivedSize)} / {formatBytes(resumableSession.totalSize)}</dd>
                </div>
                <div>
                  <dt>Status</dt>
                  <dd>{resumableSession.status}</dd>
                </div>
              </dl>
            ) : null}

            {resumableFile ? (
              <div className="progress-panel">
                <div className="progress-copy">
                  <strong>上传进度</strong>
                  <span>{resumableProgress}%</span>
                </div>
                <div className="progress-track" aria-hidden="true">
                  <div className="progress-value" style={{ width: `${resumableProgress}%` }} />
                </div>
              </div>
            ) : null}

            <button type="submit" disabled={resumableUploading}>
              {resumableUploading ? '上传中…' : resumableSessionToken ? '继续分片上传' : '开始分片上传'}
            </button>
          </form>
        ) : (
          <p>请先登录管理员账号后再使用分片上传。</p>
        )}
        {resumableMessage ? <p className="notice-text">{resumableMessage}</p> : null}
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
              <dt>Storage Backend</dt>
              <dd>{health.storageBackend}</dd>
            </div>
            <div>
              <dt>Storage Location</dt>
              <dd>{health.storageLocation}</dd>
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

        {authState === 'authenticated' ? (
          <>
            <div className="files-toolbar">
              <label className="filter-field">
                搜索文件
                <input
                  type="search"
                  placeholder="按文件名、Public ID 或类型筛选"
                  value={searchTerm}
                  onChange={(event) => setSearchTerm(event.target.value)}
                />
              </label>

              <label className="filter-field">
                状态筛选
                <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as FileStatusFilter)}>
                  <option value="all">全部状态</option>
                  <option value="active">仅启用</option>
                  <option value="disabled">仅禁用</option>
                </select>
              </label>

              <button
                type="button"
                className="ghost-button"
                disabled={searchTerm === '' && statusFilter === 'all'}
                onClick={resetFilters}
              >
                清空筛选
              </button>
            </div>

            <div className="bulk-toolbar">
              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={allVisibleSelected}
                  disabled={filteredFiles.length === 0 || actionsBusy}
                  onChange={handleToggleVisibleSelection}
                />
                <span>全选当前结果</span>
              </label>

              <p className="subtle-text">
                当前显示 {filteredFiles.length} / 共 {files.length} 项，已选 {selectedFileIds.length} 项。
              </p>

              <div className="file-actions">
                <button
                  type="button"
                  className="ghost-button"
                  disabled={selectedFileIds.length === 0 || actionsBusy}
                  onClick={() => void handleBatchSetStatus('active')}
                >
                  {batchActionBusy ? '处理中…' : '批量启用'}
                </button>
                <button
                  type="button"
                  className="ghost-button"
                  disabled={selectedFileIds.length === 0 || actionsBusy}
                  onClick={() => void handleBatchSetStatus('disabled')}
                >
                  {batchActionBusy ? '处理中…' : '批量禁用'}
                </button>
                <button
                  type="button"
                  className="danger-button"
                  disabled={selectedFileIds.length === 0 || actionsBusy}
                  onClick={() => void handleBatchDelete()}
                >
                  {batchActionBusy ? '处理中…' : '批量删除'}
                </button>
              </div>
            </div>
          </>
        ) : null}

        {filesState === 'loading' ? <p>正在拉取文件列表。</p> : null}
        {filesState === 'error' ? <p className="error-text">{filesError}</p> : null}
        {fileActionMessage ? <p className="notice-text">{fileActionMessage}</p> : null}
        {filteredFiles.length > 0 ? (
          <div className="files-list">
            {filteredFiles.map((item) => (
              <article key={item.id} className={`file-item${selectedFileIds.includes(item.id) ? ' is-selected' : ''}`}>
                <div className="file-item-header">
                  <label className="checkbox-row">
                    <input
                      type="checkbox"
                      checked={selectedFileIds.includes(item.id)}
                      disabled={actionsBusy}
                      onChange={() => handleToggleFileSelection(item.id)}
                    />
                    <span>选择</span>
                  </label>

                  <div className="file-main">
                    <strong>{item.displayName}</strong>
                    <span>{formatBytes(item.size)} · {item.contentType}</span>
                    <span>状态：{item.status} · 下载次数：{item.downloadCount} · Public ID：{item.publicId}</span>
                    <span>分享策略：{formatSharePolicySummary(item)}</span>
                  </div>

                  <span className={`status-pill status-${item.status === 'active' ? 'success' : 'error'}`}>{item.status}</span>
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

                {item.passwordProtected ? (
                  <p className="subtle-text">已启用访问密码。访客需先打开分享页输入密码，之后下载直链才可用。</p>
                ) : null}

                <div className="file-actions">
                  <button
                    type="button"
                    className="ghost-button"
                    disabled={actionsBusy}
                    onClick={() => void handleToggleFileStatus(item)}
                  >
                    {fileActionBusyId === item.id ? '处理中…' : item.status === 'disabled' ? '重新启用' : '禁用下载'}
                  </button>
                  <button
                    type="button"
                    className="danger-button"
                    disabled={actionsBusy}
                    onClick={() => void handleDeleteFile(item)}
                  >
                    {fileActionBusyId === item.id ? '处理中…' : '删除文件'}
                  </button>
                </div>

                <details className="share-policy-panel">
                  <summary>配置分享策略</summary>
                  <div className="share-policy-grid">
                    <label className="filter-field">
                      失效时间
                      <input
                        type="datetime-local"
                        value={getSharePolicyDraft(item).expiresAt}
                        onChange={(event) => updateSharePolicyDraft(item.id, { expiresAt: event.target.value })}
                      />
                    </label>

                    <label className="filter-field">
                      下载限制
                      <select
                        value={getSharePolicyDraft(item).downloadMode}
                        onChange={(event) =>
                          updateSharePolicyDraft(item.id, {
                            downloadMode: event.target.value as SharePolicyDraft['downloadMode'],
                          })
                        }
                      >
                        <option value="unlimited">不限次数</option>
                        <option value="single">单次下载</option>
                        <option value="custom">自定义次数</option>
                      </select>
                    </label>

                    {getSharePolicyDraft(item).downloadMode === 'custom' ? (
                      <label className="filter-field">
                        最大下载次数
                        <input
                          type="number"
                          min="1"
                          step="1"
                          value={getSharePolicyDraft(item).maxDownloads}
                          onChange={(event) => updateSharePolicyDraft(item.id, { maxDownloads: event.target.value })}
                        />
                      </label>
                    ) : null}

                    <label className="filter-field">
                      密码策略
                      <select
                        value={getSharePolicyDraft(item).passwordMode}
                        onChange={(event) =>
                          updateSharePolicyDraft(item.id, {
                            passwordMode: event.target.value as SharePolicyDraft['passwordMode'],
                            ...(event.target.value !== 'set' ? { password: '' } : {}),
                          })
                        }
                      >
                        {item.passwordProtected ? <option value="keep">保留当前密码</option> : <option value="clear">无访问密码</option>}
                        <option value="set">设置新密码</option>
                        {item.passwordProtected ? <option value="clear">移除访问密码</option> : null}
                      </select>
                    </label>

                    {getSharePolicyDraft(item).passwordMode === 'set' ? (
                      <label className="filter-field">
                        新访问密码
                        <input
                          type="password"
                          autoComplete="new-password"
                          placeholder="仅分享页访客需要输入"
                          value={getSharePolicyDraft(item).password}
                          onChange={(event) => updateSharePolicyDraft(item.id, { password: event.target.value })}
                        />
                      </label>
                    ) : null}
                  </div>

                  <div className="file-actions">
                    <button
                      type="button"
                      className="ghost-button"
                      disabled={actionsBusy}
                      onClick={() =>
                        updateSharePolicyDraft(item.id, {
                          ...createSharePolicyDraft(item),
                        })
                      }
                    >
                      重置表单
                    </button>
                    <button
                      type="button"
                      disabled={actionsBusy}
                      onClick={() => void handleSharePolicySubmit(item)}
                    >
                      {sharePolicyBusyId === item.id ? '保存中…' : '保存分享策略'}
                    </button>
                  </div>
                </details>
              </article>
            ))}
          </div>
        ) : filesState === 'success' && files.length > 0 ? (
          <p>当前筛选条件没有匹配文件。</p>
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

function createSharePolicyDraft(item?: FileRecord): SharePolicyDraft {
  if (!item) {
    return {
      expiresAt: '',
      downloadMode: 'unlimited',
      maxDownloads: '3',
      passwordMode: 'clear',
      password: '',
    }
  }

  const maxDownloads = item.maxDownloads
  return {
    expiresAt: toDatetimeLocalValue(item.expiresAt),
    downloadMode: maxDownloads === 1 ? 'single' : maxDownloads ? 'custom' : 'unlimited',
    maxDownloads: maxDownloads && maxDownloads !== 1 ? String(maxDownloads) : '3',
    passwordMode: item.passwordProtected ? 'keep' : 'clear',
    password: '',
  }
}

function formatSharePolicySummary(item: FileRecord): string {
  const parts = ['永久']

  if (item.expiresAt) {
    parts[0] = `到 ${formatDateTime(item.expiresAt)} 失效`
  }

  if (item.passwordProtected) {
    parts.push('密码保护')
  }

  if (item.maxDownloads === 1) {
    parts.push('单次下载')
  } else if (item.maxDownloads) {
    const remaining = item.remainingDownloads ?? Math.max(item.maxDownloads - item.downloadCount, 0)
    parts.push(`最多 ${item.maxDownloads} 次，剩余 ${remaining} 次`)
  }

  return parts.join(' · ')
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

function toDatetimeLocalValue(value?: string): string {
  if (!value) {
    return ''
  }

  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return ''
  }

  const localDate = new Date(date.getTime() - date.getTimezoneOffset() * 60 * 1000)
  return localDate.toISOString().slice(0, 16)
}

function buildUploadProgress(receivedSize: number, totalSize: number): number {
  if (totalSize <= 0) {
    return 0
  }

  return Math.max(0, Math.min(100, Math.round((receivedSize / totalSize) * 100)))
}

function getFileFingerprint(file: File): string {
  return [file.name, file.size, file.lastModified].join(':')
}

function loadStoredResumableUpload(): StoredResumableUpload | null {
  if (typeof window === 'undefined') {
    return null
  }

  try {
    const raw = window.localStorage.getItem(resumableUploadStorageKey)
    if (!raw) {
      return null
    }

    return JSON.parse(raw) as StoredResumableUpload
  } catch {
    return null
  }
}

function persistResumableUpload(payload: StoredResumableUpload) {
  if (typeof window === 'undefined') {
    return
  }

  window.localStorage.setItem(resumableUploadStorageKey, JSON.stringify(payload))
}

function clearStoredResumableUpload() {
  if (typeof window === 'undefined') {
    return
  }

  window.localStorage.removeItem(resumableUploadStorageKey)
}

interface StoredResumableUpload {
  uploadToken: string
  fingerprint: string
  displayName: string
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }

  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}
