#!/bin/sh
set -eu

MAIN_REPOSITORY="${SHAREPIER_ALPINE_MAIN_REPOSITORY:-https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/main}"
COMMUNITY_REPOSITORY="${SHAREPIER_ALPINE_COMMUNITY_REPOSITORY:-https://mirrors.tuna.tsinghua.edu.cn/alpine/v3.23/community}"

configure_repositories() {
  cat >/etc/apk/repositories <<EOF
${MAIN_REPOSITORY}
${COMMUNITY_REPOSITORY}
EOF
}

ensure_runtime() {
  if command -v initdb >/dev/null 2>&1 && command -v pg_isready >/dev/null 2>&1 && command -v su-exec >/dev/null 2>&1; then
    return 0
  fi

  configure_repositories

  attempt=1
  while [ "$attempt" -le 3 ]; do
    if apk add --no-cache postgresql17 postgresql17-client su-exec; then
      return 0
    fi

    echo "apk add failed on attempt ${attempt}, retrying..." >&2
    rm -rf /var/cache/apk/*
    apk cache clean >/dev/null 2>&1 || true
    attempt=$((attempt + 1))
    sleep 3
  done

  echo "failed to install PostgreSQL runtime after 3 attempts" >&2
  exit 1
}

ensure_runtime
exec sh /usr/local/bin/sharepier-postgres-entrypoint
