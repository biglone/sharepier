alter table files
    add column if not exists access_password_hash text,
    add column if not exists max_downloads bigint;

create index if not exists idx_files_expires_at on files(expires_at);
