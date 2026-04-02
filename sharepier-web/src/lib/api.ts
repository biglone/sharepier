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
  expiresAt?: string
  maxDownloads?: number
  remainingDownloads?: number
  passwordProtected: boolean
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
  expiresAt?: string
  maxDownloads?: number
  remainingDownloads?: number
  passwordProtected: boolean
  createdAt: string
  updatedAt: string
}

export interface FilesResponse {
  status: string
  items?: FileRecord[]
  item?: FileRecord
  affectedCount?: number
  message?: string
  timestamp?: string
}

export interface PublicFileResponse {
  status: string
  item?: PublicFileRecord
  passwordRequired?: boolean
  message?: string
  timestamp?: string
}

export interface UploadSessionRecord {
  uploadToken: string
  originalName: string
  displayName: string
  contentType: string
  totalSize: number
  receivedSize: number
  chunkSize: number
  status: string
  createdAt: string
  expiredAt: string
}

export interface UploadSessionResponse {
  status: string
  session?: UploadSessionRecord
  item?: FileRecord
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

export function unlockPublicFile(publicId: string, password: string): Promise<PublicFileResponse> {
  return requestJSON<PublicFileResponse>(`/api/v1/public/files/${encodeURIComponent(publicId)}/unlock`, {
    method: 'POST',
    body: JSON.stringify({ password }),
  })
}

export function batchUpdateFiles(
  action: 'set_status' | 'delete',
  fileIds: number[],
  status?: 'active' | 'disabled',
): Promise<FilesResponse> {
  return requestJSON<FilesResponse>('/api/v1/files/batch', {
    method: 'POST',
    body: JSON.stringify({
      action,
      fileIds,
      ...(status ? { status } : {}),
    }),
  })
}

export function updateSharePolicy(
  fileId: number,
  payload: {
    expiresAt: string | null
    maxDownloads: number | null
    passwordMode: 'keep' | 'clear' | 'set'
    password: string
  },
): Promise<FilesResponse> {
  return requestJSON<FilesResponse>(`/api/v1/files/${fileId}/share-policy`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  })
}

export function createResumableUploadSession(params: {
  originalName: string
  displayName: string
  contentType: string
  totalSize: number
}): Promise<UploadSessionResponse> {
  return requestJSON<UploadSessionResponse>('/api/v1/uploads/resumable/sessions', {
    method: 'POST',
    body: JSON.stringify(params),
  })
}

export function getResumableUploadSession(uploadToken: string): Promise<UploadSessionResponse> {
  return requestJSON<UploadSessionResponse>(`/api/v1/uploads/resumable/${encodeURIComponent(uploadToken)}`)
}

export async function uploadResumableChunk(
  uploadToken: string,
  offset: number,
  chunk: Blob,
): Promise<UploadSessionResponse> {
  const response = await fetch(`${env.apiBaseUrl}/api/v1/uploads/resumable/${encodeURIComponent(uploadToken)}`, {
    method: 'PUT',
    credentials: 'include',
    headers: {
      'X-Upload-Offset': String(offset),
    },
    body: chunk,
  })

  const payload = (await response.json().catch(() => null)) as UploadSessionResponse | null
  if (!response.ok) {
    throw new ApiError(payload?.message || `Chunk upload failed: ${response.status}`, response.status)
  }

  return payload as UploadSessionResponse
}

export function completeResumableUpload(uploadToken: string): Promise<UploadSessionResponse> {
  return requestJSON<UploadSessionResponse>(`/api/v1/uploads/resumable/${encodeURIComponent(uploadToken)}/complete`, {
    method: 'POST',
  })
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
