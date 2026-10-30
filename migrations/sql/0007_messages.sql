-- сообщения: подписки токенов, которые ждут сообщение (receive task, промежуточное событие сообщения),
-- и сообщения, пришедшие раньше подписки (буфер на ttl). Сообщение находит подписку по имени и ключу корреляции
create table bpm_message_subscription
(
    -- subscription_uid - UUID v7: подписки одного сообщения получают его в порядке создания
    subscription_uid uuid primary key,
    message_name     text        not null,
    correlation_key  text        not null,
    instance_uid     uuid        not null,
    process_name     text        not null,
    element_id       text        not null,
    -- message - токен, который продолжится, когда придёт сообщение
    message          jsonb       not null,
    created_at       timestamptz not null default now()
);

create index bpm_message_subscription_key_idx on bpm_message_subscription (message_name, correlation_key, subscription_uid);
create index bpm_message_subscription_instance_idx on bpm_message_subscription (instance_uid);

create table bpm_message
(
    id              bigserial primary key,
    message_name    text        not null,
    correlation_key text        not null,
    variables       jsonb       not null default '{}',
    expires_at      timestamptz not null,
    created_at      timestamptz not null default now()
);

create index bpm_message_key_idx on bpm_message (message_name, correlation_key, id);
create index bpm_message_expires_idx on bpm_message (expires_at);
