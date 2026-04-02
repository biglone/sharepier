import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { login } from '../lib/api'

export function LoginPage() {
  const navigate = useNavigate()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    setMessage('')

    try {
      const result = await login(username, password)
      setMessage(`登录成功：${result.user?.username || username}`)
      navigate('/admin')
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="card auth-card">
      <div>
        <p className="eyebrow">Admin Auth</p>
        <h2>管理员登录骨架</h2>
        <p>
          当前页面已经接到真实登录接口。开发环境默认管理员用户名是
          <code>admin</code>，密码请按你的环境变量配置为准。
        </p>
      </div>

      <form className="auth-form" onSubmit={handleSubmit}>
        <label>
          用户名
          <input
            name="username"
            type="text"
            placeholder="admin"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </label>
        <label>
          密码
          <input
            name="password"
            type="password"
            placeholder="••••••••"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        <button type="submit" disabled={submitting}>
          {submitting ? '登录中…' : '登录'}
        </button>
      </form>

      {error ? <p className="error-text">{error}</p> : null}
      {message ? <p className="notice-text">{message}</p> : null}
    </section>
  )
}
