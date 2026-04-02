import { Link, Outlet } from 'react-router-dom'

export function AppShell() {
  return (
    <div className="app-shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">SharePier</p>
          <h1>文件上传与稳定分享入口</h1>
        </div>
        <nav className="topnav">
          <Link to="/admin">管理台</Link>
          <Link to="/admin/login">登录</Link>
          <Link to="/share/demo/demo.zip">分享页示例</Link>
        </nav>
      </header>
      <main className="page-wrap">
        <Outlet />
      </main>
    </div>
  )
}
