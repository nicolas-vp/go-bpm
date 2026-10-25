-- открытые задачи без исполнителя и кандидатов: их может взять любой, поэтому они в очереди каждого пользователя
create index bpm_user_task_open_to_all_idx on bpm_user_task (task_uid)
    where status = 'created' and assignee is null and candidate_users = '{}' and candidate_groups = '{}';
