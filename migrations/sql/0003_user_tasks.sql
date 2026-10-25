-- bpm_user_task - задачи пользователей. Строку вставляет движок, когда токен приходит в userTask, а завершает API:
-- в той же tx ставится системная задача, которая ведёт токен дальше. Завершённые задачи не удаляются
create table bpm_user_task
(
    -- task_uid - UUID v7, растёт со временем создания: по нему выборка идёт от новых к старым
    task_uid         uuid primary key,
    instance_uid     uuid        not null,
    process_name     text        not null,
    element_id       text        not null,
    name             text,
    business_key     text,
    form_key         text,
    -- assignee - исполнитель; пока его нет, задачу может взять кандидат
    assignee         text,
    candidate_users  text[]      not null default '{}',
    candidate_groups text[]      not null default '{}',
    status           text        not null default 'created' check (status in ('created', 'completed')),
    -- message - токен экземпляра, с которым процесс продолжится после завершения задачи
    message          jsonb       not null,
    completed_by     text,
    created_at       timestamptz not null default now(),
    claimed_at       timestamptz,
    completed_at     timestamptz,
    updated_at       timestamptz not null default now()
);

-- очередь открытых задач: назначенные исполнителю и доступные кандидатам
create index bpm_user_task_assignee_idx on bpm_user_task (assignee, task_uid) where status = 'created';
create index bpm_user_task_candidate_users_idx on bpm_user_task using gin (candidate_users) where status = 'created';
create index bpm_user_task_candidate_groups_idx on bpm_user_task using gin (candidate_groups) where status = 'created';
create index bpm_user_task_process_idx on bpm_user_task (process_name, task_uid) where status = 'created';
create index bpm_user_task_instance_idx on bpm_user_task (instance_uid, task_uid);
