-- bpm_queue - очередь задач движка (пакет queue): шаги процессов, service task, таймеры
create table bpm_queue
(
    id         bigserial primary key,
    kind       text        not null,
    -- key - ключ идемпотентности: задача с ключом, который уже есть у задачи этого вида, не ставится
    key        text,
    payload    jsonb       not null,
    status     text        not null default 'pending' check (status in ('pending', 'done', 'failed', 'cancelled')),
    -- attempt - сколько раз задачу выдавали воркеру
    attempt    integer     not null default 0,
    -- run_at - когда задачу можно выдать; у выданной задачи - конец аренды
    run_at     timestamptz not null default now(),
    last_error text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create unique index bpm_queue_key_idx on bpm_queue (kind, key) where key is not null;
create index bpm_queue_pending_idx on bpm_queue (kind, run_at, id) where status = 'pending';
create index bpm_queue_finished_idx on bpm_queue (updated_at) where status in ('done', 'cancelled');

-- bpm_parallel_sync - слияние веток параллельного шлюза: сколько веток пришло и их переменные
create table bpm_parallel_sync
(
    hash       text primary key,
    arrived    integer     not null,
    variables  jsonb       not null default '{}',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- bpm_wait - задачи с граничными таймерами, которые ждут завершения. Задача и её таймеры гоняются:
-- кто первым удалил строку, тот и ведёт токен дальше, остальные завершаются молча
create table bpm_wait
(
    wait_id      uuid primary key,
    instance_uid uuid        not null,
    element_id   text        not null,
    created_at   timestamptz not null default now()
);

-- bpm_history - история выполнения элементов процессов
create table bpm_history
(
    id                  bigserial primary key,
    process_name        text        not null,
    instance_uid        uuid        not null,
    -- parent_instance_uid, parent_element_id - экземпляр и элемент родителя для подпроцесса и callActivity
    parent_instance_uid uuid,
    parent_element_id   text,
    element_id          text        not null,
    event               text        not null check (event in ('activated', 'completed', 'terminated')),
    -- variables - только у старта процесса и задач, у остальных записей null
    variables           jsonb,
    created_at          timestamptz not null default now()
);

create index bpm_history_instance_uid_idx on bpm_history (instance_uid, id);
create index bpm_history_parent_instance_uid_idx on bpm_history (parent_instance_uid) where parent_instance_uid is not null;

-- bpm_rules_history - история бизнес-правил: какие правила решения DMN сработали при выполнении задачи
create table bpm_rules_history
(
    id           bigserial primary key,
    process_name text        not null,
    instance_uid uuid        not null,
    element_id   text        not null,
    decision_id  text        not null,
    -- rule_ids - id сработавших правил в порядке таблицы
    rule_ids     jsonb       not null,
    result       jsonb,
    created_at   timestamptz not null default now()
);

create index bpm_rules_history_instance_element_idx on bpm_rules_history (instance_uid, element_id, id);
