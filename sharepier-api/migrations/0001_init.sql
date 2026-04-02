create table if not exists users (
    id bigserial primary key,
    username text not null unique,
    password_hash text not null,
    role text not null default 'admin',
    created_at timestamptz not null default now()
);

create table if not exists objects (
    id bigserial primary key,
    storage_key text not null unique,
    sha256 text not null,
    size bigint not null,
    content_type text not null,
    created_at timestamptz not null default now()
);

create table if not exists files (
    id bigserial primary key,
    object_id bigint not null references objects(id) on delete cascade,
    owner_user_id bigint references users(id) on delete set null,
    original_name text not null,
    display_name text not null,
    public_id text not null unique,
    visibility text not null default 'public',
    status text not null default 'active',
    download_count bigint not null default 0,
    expires_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create table if not exists upload_sessions (
    id bigserial primary key,
    user_id bigint references users(id) on delete set null,
    upload_token text not null unique,
    status text not null default 'pending',
    total_size bigint not null default 0,
    received_size bigint not null default 0,
    created_at timestamptz not null default now(),
    expired_at timestamptz
);

create table if not exists sessions (
    id bigserial primary key,
    user_id bigint not null references users(id) on delete cascade,
    token_hash text not null unique,
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);

create table if not exists download_events (
    id bigserial primary key,
    file_id bigint not null references files(id) on delete cascade,
    ip_hash text,
    user_agent text,
    referer text,
    created_at timestamptz not null default now()
);

create index if not exists idx_files_public_id on files(public_id);
create index if not exists idx_files_status on files(status);
create index if not exists idx_download_events_file_id on download_events(file_id);
create index if not exists idx_sessions_token_hash on sessions(token_hash);
