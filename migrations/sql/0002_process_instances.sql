-- bpm_process_instance - экземпляры процессов: тип, бизнес-ключ и статус. Строку пишет движок вместе с историей
-- (HistoryConfig.ToDB); у встроенного подпроцесса и процесса callActivity свой экземпляр со ссылкой на родителя
create table bpm_process_instance
(
    instance_uid        uuid primary key,
    -- process_name - id процесса в BPMN, у встроенного подпроцесса "<процесс>.<подпроцесс>"
    process_name        text        not null,
    business_key        text,
    parent_instance_uid uuid,
    status              text        not null default 'running' check (status in ('running', 'completed', 'failed')),
    created_at          timestamptz not null default now(),
    -- updated_at - последний шаг экземпляра или смена статуса
    updated_at          timestamptz not null default now()
);

-- instance_uid - UUID v7, растёт со временем старта: по нему выборка идёт от новых к старым
create index bpm_process_instance_status_idx on bpm_process_instance (status, process_name, instance_uid);
create index bpm_process_instance_process_idx on bpm_process_instance (process_name, instance_uid);
