-- операции над экземплярами и статистика. Отмена экземпляра: экземпляр, его дочерние экземпляры и их открытые задачи пользователей становятся cancelled
alter table bpm_process_instance drop constraint bpm_process_instance_status_check;
alter table bpm_process_instance add constraint bpm_process_instance_status_check
    check (status in ('running', 'completed', 'failed', 'cancelled'));

alter table bpm_user_task drop constraint bpm_user_task_status_check;
alter table bpm_user_task add constraint bpm_user_task_status_check
    check (status in ('created', 'completed', 'cancelled'));

-- дочерние экземпляры: дерево экземпляра для отмены и поиска упавших задач
create index bpm_process_instance_parent_idx on bpm_process_instance (parent_instance_uid)
    where parent_instance_uid is not null;

-- незавершённые задачи очереди по экземпляру токена: у системных задач токен в payload -> 'message',
-- у задач service task - в самом payload. Выражение - то же, что в запросах engine (instanceOfTask)
create index bpm_queue_instance_idx on bpm_queue ((coalesce(payload -> 'message' ->> 'InstanceUid', payload ->> 'InstanceUid')))
    where status in ('pending', 'failed');

-- экземпляры процесса за период: динамика по дням и термокарта
create index bpm_process_instance_created_idx on bpm_process_instance (process_name, created_at);
