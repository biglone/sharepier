import { env } from './env'

export class ApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export interface HealthResponse {
  status: string
  name?: string
  version?: string
  environment?: string
  timestamp?: string
  message?: string
  storageRoot?: string
}

export interface AuthUser {
  id: number
  username: string
  role: string
}

export interface AuthResponse {
  status: string
  user?: AuthUser
  message?: string
  timestamp?: string
}

export interface FileRecord {
  id: number
  publicId: string
  originalName: string
  displayName: string
  status: string
  visibility: string
  contentType: string
  size: number
  downloadCount: number
  downloadUrl: string
  createdAt: string
  updatedAt: string
}

export interface PublicFileRecord {
  publicId: string
  originalName: string
  displayName: string
  contentType: string
  size: number
  downloadCount: number
  sha256: string
  downloadUrl: string
  createdAt: string
  updatedAt: string
}

export interface FilesResponse {
  status: string
  items?: FileRecord[]
  item?: FileRecord
  message?: string
  timestamp?: string
}

export interface PublicFileResponse {
  status: string
  item?: PublicFileRecord
  message?: string
  timestamp?: string
}

export async function getHealth(): Promise<HealthResponse> {
  const response = await fetch(`${env.apiBaseUrl}/api/v1/health`, {
    headers: {
      Accept: 'application/json',
    },
  })

  if (!response.ok) {
    throw new Error(`Health check failed: ${response.status}`)
  }

  return (await response.json()) as HealthResponse
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: HeadersInit = {
    Accept: 'application/json',
    ...(init.body ? { 'Content-Type': 'application/json' } : {}),
    ...(init.headers ?? {}),
  }

  const response = await fetch(`${env.apiBaseUrl}${path}`, {
    credentials: 'include',
    ...init,
    headers,
  })

  const payload = (await response.json().catch(() => null)) as {
    message?: string
  } | null

  if (!response.ok) {
    throw new ApiError(payload?.message || `Request failed: ${response.status}`, response.status)
  }

  return payload as T
}

export function getCurrentUser(): Promise<AuthResponse> {
  return requestJSON<AuthResponse>('/api/v1/auth/me')
}

export function login(username: string, password: string): Promise<AuthResponse> {
  return requestJSON<AuthResponse>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  })
}

export function logout(): Promise<AuthResponse> {
  return requestJSON<AuthResponse>('/api/v1/auth/logout', {
    method: 'POST',
  })
}

export function listFiles(): Promise<FilesResponse> {
  return requestJSON<FilesResponse>('/api/v1/files')
}

export function getPublicFile(publicId: string): Promise<PublicFileResponse> {
  return requestJSON<PublicFileResponse>(`/api/v1/public/files/${encodeURIComponent(publicId)}`)
}

export function setFileStatus(fileId: number, status: 'active' | 'disabled'): Promise<FilesResponse> {
  return requestJSON<FilesResponse>(`/api/v1/files/${fileId}`, {
    method: 'PATCH',
    body: JSON.stringify({ status }),
  })
}

export function deleteFile(fileId: number): Promise<FilesResponse> {
  return requestJSON<FilesResponse>(`/api/v1/files/${fileId}`, {
    method: 'DELETE',
  })
}

export async function uploadFile(file: File, displayName: string): Promise<FilesResponse> {
  const formData = new FormData()
  formData.append('file', file)
  if (displayName.trim() !== '') {
    formData.append('displayName', displayName.trim())
  }

  const response = await fetch(`${env.apiBaseUrl}/api/v1/uploads`, {
    method: 'POST',
    credentials: 'include',
    body: formData,
  })

  const payload = (await response.json().catch(() => null)) as FilesResponse | null
  if (!response.ok) {
    throw new ApiError(payload?.message || `Upload failed: ${response.status}`, response.status)
  }

  return payload as FilesResponse
}
