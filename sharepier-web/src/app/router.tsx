import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from './AppShell'
import { DashboardPage } from '../pages/DashboardPage'
import { LoginPage } from '../pages/LoginPage'
import { PublicDownloadPage } from '../pages/PublicDownloadPage'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/admin" replace /> },
      { path: 'admin', element: <DashboardPage /> },
      { path: 'admin/login', element: <LoginPage /> },
      { path: 'share/:publicId/:filename?', element: <PublicDownloadPage /> },
    ],
  },
])
