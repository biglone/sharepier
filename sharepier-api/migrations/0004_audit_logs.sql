create table if not exists audit_logs (
    id bigserial primary key,
    actor_user_id bigint,
    actor_username text,
    action text not null,
    file_id bigint,
    file_public_id text,
    file_display_name text,
    metadata jsonb not null default '{}'::jsonb,
    ip_hash text,
    user_agent text,
    created_at timestamptz not null default now()
);

create index if not exists idx_audit_logs_created_at on audit_logs(created_at desc);
create index if not exists idx_audit_logs_action on audit_logs(action);
create index if not exists idx_audit_logs_file_id on audit_logs(file_id);
