import { createServer } from 'node:http'
import { createReadStream, existsSync, statSync } from 'node:fs'
import path from 'node:path'

const host = process.env.HOST || '0.0.0.0'
const port = Number(process.env.PORT || 3000)
const rootDir = path.resolve(process.cwd(), 'dist')

const contentTypes = new Map([
  ['.css', 'text/css; charset=utf-8'],
  ['.html', 'text/html; charset=utf-8'],
  ['.ico', 'image/x-icon'],
  ['.js', 'text/javascript; charset=utf-8'],
  ['.json', 'application/json; charset=utf-8'],
  ['.png', 'image/png'],
  ['.svg', 'image/svg+xml'],
  ['.txt', 'text/plain; charset=utf-8'],
  ['.woff', 'font/woff'],
  ['.woff2', 'font/woff2'],
])

function sendFile(request, response, filePath) {
  const extension = path.extname(filePath).toLowerCase()
  const stat = statSync(filePath)
  response.statusCode = 200
  response.setHeader('Content-Length', stat.size)
  response.setHeader('Content-Type', contentTypes.get(extension) || 'application/octet-stream')

  if (filePath.includes(`${path.sep}assets${path.sep}`)) {
    response.setHeader('Cache-Control', 'public, max-age=31536000, immutable')
  } else {
    response.setHeader('Cache-Control', 'no-cache')
  }

  if (request.method === 'HEAD') {
    response.end()
    return
  }

  createReadStream(filePath).pipe(response)
}

function notFound(response) {
  response.statusCode = 404
  response.setHeader('Content-Type', 'text/plain; charset=utf-8')
  response.end('Not Found')
}

function isSpaRoute(pathname) {
  return pathname === '/share' || pathname.startsWith('/share/')
}

const server = createServer((request, response) => {
  if (!request.url) {
    notFound(response)
    return
  }

  if (request.method !== 'GET' && request.method !== 'HEAD') {
    response.statusCode = 405
    response.setHeader('Allow', 'GET, HEAD')
    response.end()
    return
  }

  const url = new URL(request.url, 'http://localhost')
  if (url.pathname === '/healthz') {
    response.statusCode = 200
    response.setHeader('Content-Type', 'text/plain; charset=utf-8')
    response.end('ok')
    return
  }

  const normalized = path.normalize(decodeURIComponent(url.pathname)).replace(/^(\.\.[/\\])+/, '')
  const requestedPath = path.join(rootDir, normalized)
  const isWithinRoot = requestedPath === rootDir || requestedPath.startsWith(`${rootDir}${path.sep}`)

  if (isWithinRoot && existsSync(requestedPath) && statSync(requestedPath).isFile()) {
    sendFile(request, response, requestedPath)
    return
  }

  if (path.extname(url.pathname) !== '' && !isSpaRoute(url.pathname)) {
    notFound(response)
    return
  }

  const indexPath = path.join(rootDir, 'index.html')
  if (!existsSync(indexPath)) {
    response.statusCode = 500
    response.setHeader('Content-Type', 'text/plain; charset=utf-8')
    response.end('dist/index.html not found')
    return
  }

  sendFile(request, response, indexPath)
})

server.listen(port, host, () => {
  console.log(`SharePier static web listening on http://${host}:${port}`)
})
