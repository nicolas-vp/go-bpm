-- очистка истории по сроку хранения (engine.Retention): завершённые и отменённые корневые экземпляры от старых к новым
create index bpm_process_instance_finished_idx on bpm_process_instance (updated_at)
    where parent_instance_uid is null and status in ('completed', 'cancelled');
