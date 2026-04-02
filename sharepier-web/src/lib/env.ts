const fallbackApiBaseUrl = 'http://localhost:38080'

export const env = {
  apiBaseUrl: import.meta.env.VITE_API_BASE_URL || fallbackApiBaseUrl,
}
