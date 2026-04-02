import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError, getPublicFile, unlockPublicFile, type PublicFileRecord } from '../lib/api'

export function PublicDownloadPage() {
  const { publicId } = useParams()
  const [item, setItem] = useState<PublicFileRecord | null>(null)
  const [state, setState] = useState<'idle' | 'loading' | 'locked' | 'success' | 'error'>('idle')
  const [error, setError] = useState<string | null>(null)
  const [copyMessage, setCopyMessage] = useState<string | null>(null)
  const [password, setPassword] = useState('')
  const [unlocking, setUnlocking] = useState(false)

  useEffect(() => {
    const targetPublicId = publicId
    if (!targetPublicId) {
      return
    }
    const publicFileId: string = targetPublicId

    let cancelled = false

    async function loadPublicFile() {
      setState('loading')
      setError(null)
      setCopyMessage(null)

      try {
        const result = await getPublicFile(publicFileId)
        if (!cancelled) {
          setItem(result.item ?? null)
          setState(result.item ? 'success' : 'error')
          setError(result.item ? null : '文件不存在或已不可用')
        }
      } catch (err) {
        if (cancelled) {
          return
        }

        if (err instanceof ApiError && err.status === 401) {
          setItem(null)
          setState('locked')
          setError('此分享已启用访问密码，请先输入密码再下载。')
          return
        }

        if (err instanceof ApiError && err.status === 404) {
          setItem(null)
          setState('error')
          setError('文件不存在、已被禁用，或分享链接已失效。')
          return
        }

        if (err instanceof ApiError && err.status === 410) {
          setItem(null)
          setState('error')
          setError('该分享已过期，或允许的下载次数已经用尽。')
          return
        }

        setItem(null)
        setState('error')
        setError(err instanceof Error ? err.message : '加载分享页失败')
      }
    }

    void loadPublicFile()

    return () => {
      cancelled = true
    }
  }, [publicId])

  const shareUrl = useMemo(() => {
    if (!item || typeof window === 'undefined') {
      return ''
    }

    return buildShareUrl(item.publicId, item.displayName)
  }, [item])

  async function handleCopyShareLink() {
    if (!shareUrl || typeof navigator === 'undefined' || !navigator.clipboard) {
      setCopyMessage('当前环境不支持自动复制，请手动复制下方链接。')
      return
    }

    try {
      await navigator.clipboard.writeText(shareUrl)
      setCopyMessage('分享链接已复制')
    } catch {
      setCopyMessage('复制失败，请手动复制下方链接。')
    }
  }

  async function handleUnlock(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!publicId) {
      return
    }
    if (password.trim() === '') {
      setError('请输入访问密码')
      return
    }

    setUnlocking(true)
    setError(null)
    try {
      const result = await unlockPublicFile(publicId, password)
      setItem(result.item ?? null)
      setPassword('')
      setState(result.item ? 'success' : 'error')
      setError(result.item ? null : '密码验证成功，但文件仍不可用')
    } catch (err) {
      setItem(null)
      setState('locked')
      if (err instanceof ApiError && err.status === 401) {
        setError('访问密码错误')
      } else if (err instanceof ApiError && err.status === 410) {
        setState('error')
        setError('该分享已过期，或允许的下载次数已经用尽。')
      } else if (err instanceof ApiError && err.status === 404) {
        setState('error')
        setError('文件不存在、已被禁用，或分享链接已失效。')
      } else {
        setError(err instanceof Error ? err.message : '密码验证失败')
      }
    } finally {
      setUnlocking(false)
    }
  }

  if (!publicId) {
    return (
      <section className="card download-card public-download-card">
        <div className="public-empty-state">
          <p className="eyebrow">SharePier Public Link</p>
          <h2>分享链接无效</h2>
          <p>缺少文件标识，当前地址无法解析出目标文件。</p>
          <div className="public-actions">
            <Link className="ghost-button" to="/">
              返回首页
            </Link>
          </div>
        </div>
      </section>
    )
  }

  return (
    <section className="card download-card public-download-card">
      {state === 'loading' ? (
        <div className="public-empty-state">
          <p className="eyebrow">SharePier Public Link</p>
          <h2>正在加载文件信息</h2>
          <p>请稍候，正在从分享链接解析文件元数据。</p>
        </div>
      ) : null}

      {state === 'error' ? (
        <div className="public-empty-state">
          <p className="eyebrow">SharePier Public Link</p>
          <h2>文件暂时不可用</h2>
          <p>{error}</p>
          <div className="public-actions">
            <Link className="ghost-button" to="/">
              返回首页
            </Link>
          </div>
        </div>
      ) : null}

      {state === 'locked' ? (
        <div className="public-empty-state public-locked-state">
          <p className="eyebrow">SharePier Protected Link</p>
          <h2>此分享已启用访问密码</h2>
          <p>输入分享方提供的访问密码后，当前浏览器会获得短期访问权限，随后可直接下载文件。</p>
          <form className="public-password-form" onSubmit={(event) => void handleUnlock(event)}>
            <label>
              访问密码
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="请输入访问密码"
              />
            </label>
            <button type="submit" disabled={unlocking}>
              {unlocking ? '验证中…' : '验证并继续'}
            </button>
          </form>
          {error ? <p className="error-text">{error}</p> : null}
          <div className="public-actions">
            <Link className="ghost-button" to="/">
              返回首页
            </Link>
          </div>
        </div>
      ) : null}

      {state === 'success' && item ? (
        <>
          <div className="public-file-hero">
            <div className="public-file-badge" aria-hidden="true">
              {getFileExtension(item.displayName)}
            </div>
            <div className="public-file-copy">
              <p className="eyebrow">SharePier Public Link</p>
              <h2>{item.displayName}</h2>
              <p>{buildPublicSummary(item)}</p>
            </div>
          </div>

          <div className="public-actions">
            <a className="primary-button" href={item.downloadUrl}>
              下载文件
            </a>
            <button type="button" className="ghost-button" onClick={handleCopyShareLink}>
              复制分享链接
            </button>
          </div>

          {copyMessage ? <p className="notice-text">{copyMessage}</p> : null}

          <div className="public-stat-grid">
            <article className="stat-card">
              <span className="stat-label">文件大小</span>
              <strong>{formatBytes(item.size)}</strong>
            </article>
            <article className="stat-card">
              <span className="stat-label">文件类型</span>
              <strong>{formatContentType(item.contentType)}</strong>
            </article>
            <article className="stat-card">
              <span className="stat-label">下载次数</span>
              <strong>{item.downloadCount}</strong>
            </article>
            <article className="stat-card">
              <span className="stat-label">最近更新</span>
              <strong>{formatDateTime(item.updatedAt)}</strong>
            </article>
            {item.expiresAt ? (
              <article className="stat-card">
                <span className="stat-label">失效时间</span>
                <strong>{formatDateTime(item.expiresAt)}</strong>
              </article>
            ) : null}
            {item.maxDownloads ? (
              <article className="stat-card">
                <span className="stat-label">剩余下载</span>
                <strong>{item.remainingDownloads ?? Math.max(item.maxDownloads - item.downloadCount, 0)} / {item.maxDownloads}</strong>
              </article>
            ) : null}
          </div>

          <dl className="kv-grid public-meta-grid">
            <div>
              <dt>原始文件名</dt>
              <dd>{item.originalName}</dd>
            </div>
            <div>
              <dt>Public ID</dt>
              <dd>{item.publicId}</dd>
            </div>
            <div>
              <dt>分享链接</dt>
              <dd>
                <a href={shareUrl}>{shareUrl}</a>
              </dd>
            </div>
            <div>
              <dt>下载直链</dt>
              <dd>
                <a href={item.downloadUrl}>{item.downloadUrl}</a>
              </dd>
            </div>
            <div>
              <dt>分享策略</dt>
              <dd>{formatSharePolicy(item)}</dd>
            </div>
            <div>
              <dt>SHA-256</dt>
              <dd className="hash-value">{item.sha256}</dd>
            </div>
            <div>
              <dt>创建时间</dt>
              <dd>{formatDateTime(item.createdAt)}</dd>
            </div>
          </dl>
        </>
      ) : null}
    </section>
  )
}

function buildShareUrl(publicId: string, fileName: string): string {
  return new URL(`/share/${publicId}/${encodeURIComponent(fileName)}`, window.location.origin).href
}

function buildPublicSummary(item: PublicFileRecord): string {
  const notes: string[] = []

  if (item.passwordProtected) {
    notes.push('当前链接已通过访问密码解锁。')
  }
  if (item.expiresAt) {
    notes.push(`分享将在 ${formatDateTime(item.expiresAt)} 失效。`)
  }
  if (item.maxDownloads) {
    notes.push(`还剩 ${item.remainingDownloads ?? Math.max(item.maxDownloads - item.downloadCount, 0)} / ${item.maxDownloads} 次下载机会。`)
  }

  if (notes.length === 0) {
    return '文件已就绪。你可以直接下载，也可以继续分发当前分享页链接或底部的下载直链。'
  }

  return `文件已就绪。${notes.join(' ')}`
}

function formatSharePolicy(item: PublicFileRecord): string {
  const parts = ['永久可下载']

  if (item.expiresAt) {
    parts[0] = `到 ${formatDateTime(item.expiresAt)} 失效`
  }
  if (item.passwordProtected) {
    parts.push('访问密码已启用')
  }
  if (item.maxDownloads === 1) {
    parts.push('单次下载')
  } else if (item.maxDownloads) {
    parts.push(`最多 ${item.maxDownloads} 次`)
  }

  return parts.join(' · ')
}

function getFileExtension(fileName: string): string {
  const parts = fileName.split('.')
  if (parts.length < 2) {
    return 'FILE'
  }

  return parts.at(-1)?.slice(0, 4).toUpperCase() || 'FILE'
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

function formatContentType(value: string): string {
  return value.trim() || 'application/octet-stream'
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
