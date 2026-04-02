alter table upload_sessions
    add column if not exists original_name text not null default '',
    add column if not exists display_name text not null default '',
    add column if not exists content_type text not null default 'application/octet-stream';
