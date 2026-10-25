package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestUserTasks проходит демо-процесс TestUserTask через API задач: согласование кандидатом из группы, возврат
// на исправление исполнителю из переменной initiator, повторное согласование и End
func TestUserTasks(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	on := func() bool { return true }

	svc := &services.Services{}
	processes := bpmgen.Processes(svc)
	q := queue.New(pool, queue.Options{PollInterval: 200 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{History: engine.HistoryConfig{ToDB: on}, Logger: log}, processes, bpmgen.Queues(svc))
	if err != nil {
		t.Fatal(err)
	}
	q.Start(ctx)
	defer q.Stop()
	tasks := engine.NewUserTaskService(pool, e)
	reader := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)

	id, err := e.StartProcess(ctx, "TestUserTask", map[string]any{"initiator": "alice"}, engine.WithBusinessKey("order-7"))
	if err != nil {
		t.Fatal(err)
	}
	// openTask ждёт единственную открытую задачу экземпляра: токен до неё ведёт очередь
	openTask := func(elementId string) engine.UserTask {
		t.Helper()
		waitQueue(t, pool, 10*time.Second)
		list, err := tasks.ListTasks(ctx, engine.TaskFilter{InstanceUid: id})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].ElementId != elementId {
			t.Fatalf("open tasks = %+v, want one %s", list, elementId)
		}
		return list[0]
	}
	wantErr := func(err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}

	approve := openTask("Approve")
	if approve.Name != "Согласовать заявку" || approve.FormKey != "approve-form" || approve.BusinessKey != "order-7" ||
		approve.Assignee != "" || !slices.Equal(approve.CandidateGroups, []string{"managers", "admins"}) {
		t.Errorf("approve = %+v", approve)
	}
	// очередь группы: задача видна менеджеру и не видна бухгалтеру
	for groups, want := range map[string]int{"managers": 1, "accounting": 0} {
		list, err := tasks.ListTasks(ctx, engine.TaskFilter{CandidateUser: "bob", CandidateGroups: []string{groups}})
		if err != nil || len(list) != want {
			t.Errorf("queue of %s = %+v, err %v, want %d", groups, list, err, want)
		}
	}

	wantErr(tasks.Claim(ctx, approve.TaskUid, "carol", []string{"accounting"}), engine.ErrNotCandidate)
	wantErr(tasks.Complete(ctx, approve.TaskUid, "bob", nil), engine.ErrNotAssignee)
	if err := tasks.Claim(ctx, approve.TaskUid, "bob", []string{"managers"}); err != nil {
		t.Fatal(err)
	}
	wantErr(tasks.Claim(ctx, approve.TaskUid, "dave", []string{"managers"}), engine.ErrTaskClaimed)
	// взятая задача ушла из очереди группы и видна как задача bob
	if list, _ := tasks.ListTasks(ctx, engine.TaskFilter{CandidateGroups: []string{"managers"}}); len(list) != 0 {
		t.Errorf("claimed task is still in the group queue: %+v", list)
	}
	if list, _ := tasks.ListTasks(ctx, engine.TaskFilter{Assignee: "bob"}); len(list) != 1 {
		t.Errorf("tasks of bob = %+v", list)
	}
	wantErr(tasks.Unclaim(ctx, approve.TaskUid, "dave"), engine.ErrNotAssignee)
	if err := tasks.Unclaim(ctx, approve.TaskUid, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Claim(ctx, approve.TaskUid, "bob", []string{"managers"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Complete(ctx, approve.TaskUid, "bob", map[string]any{"approved": false, "comment": "нет суммы"}); err != nil {
		t.Fatal(err)
	}
	wantErr(tasks.Complete(ctx, approve.TaskUid, "bob", nil), engine.ErrTaskCompleted)

	// не согласовано - исправляет инициатор: задача сразу назначена ему, с переменными из формы согласования
	fix := openTask("Fix")
	if fix.Assignee != "alice" {
		t.Errorf("fix = %+v, want assignee alice", fix)
	}
	full, err := tasks.GetTask(ctx, fix.TaskUid)
	if err != nil {
		t.Fatal(err)
	}
	if full.Variables["comment"] != "нет суммы" || full.Variables["initiator"] != "alice" {
		t.Errorf("fix variables = %v", full.Variables)
	}
	if err := tasks.Complete(ctx, fix.TaskUid, "alice", map[string]any{"amount": 100}); err != nil {
		t.Fatal(err)
	}

	approve = openTask("Approve")
	if err := tasks.Claim(ctx, approve.TaskUid, "erin", []string{"admins"}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Complete(ctx, approve.TaskUid, "erin", map[string]any{"approved": true}); err != nil {
		t.Fatal(err)
	}
	waitQueue(t, pool, 10*time.Second)

	instance, err := reader.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Status != engine.InstanceCompleted {
		t.Errorf("instance = %+v, want completed", instance)
	}
	// история назначения: задача на каждый приход токена, время назначения - и у взятой, и у назначенной сразу
	approves, err := reader.GetUserTasks(ctx, id, "Approve")
	if err != nil {
		t.Fatal(err)
	}
	if len(approves) != 2 || approves[0].CompletedBy != "bob" || approves[1].CompletedBy != "erin" ||
		approves[0].ClaimedAt == nil || approves[1].Assignee != "erin" || !slices.Equal(approves[1].CandidateGroups, []string{"managers", "admins"}) {
		t.Errorf("approve tasks = %+v", approves)
	}
	fixes, err := reader.GetUserTasks(ctx, id, "Fix")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixes) != 1 || fixes[0].Assignee != "alice" || fixes[0].ClaimedAt == nil || fixes[0].ClaimedAt.Before(fixes[0].CreatedAt) {
		t.Errorf("fix tasks = %+v, want assigned to alice at creation", fixes)
	}
	done, err := tasks.ListTasks(ctx, engine.TaskFilter{InstanceUid: id, Status: engine.UserTaskCompleted})
	if err != nil {
		t.Fatal(err)
	}
	var completedBy []string
	for _, task := range done {
		completedBy = append(completedBy, task.ElementId+" "+task.CompletedBy)
	}
	// от новых к старым
	if want := []string{"Approve erin", "Fix alice", "Approve bob"}; !slices.Equal(completedBy, want) {
		t.Errorf("completed tasks = %v, want %v", completedBy, want)
	}
	// без initiator «Исправить» создаётся без исполнителя и кандидатов: её видит в очереди и может взять любой
	id, err = e.StartProcess(ctx, "TestUserTask", nil)
	if err != nil {
		t.Fatal(err)
	}
	approve = openTask("Approve")
	if err := tasks.Claim(ctx, approve.TaskUid, "bob", []string{"managers"}); err != nil {
		t.Fatal(err)
	}
	// approved не передан: в модели false, процесс уходит на исправление
	if err := tasks.Complete(ctx, approve.TaskUid, "bob", nil); err != nil {
		t.Fatal(err)
	}
	fix = openTask("Fix")
	if fix.Assignee != "" || len(fix.CandidateUsers) != 0 || len(fix.CandidateGroups) != 0 {
		t.Errorf("fix without initiator = %+v, want no assignee and no candidates", fix)
	}
	queue, err := tasks.ListTasks(ctx, engine.TaskFilter{CandidateUser: "zoe", CandidateGroups: []string{"accounting"}})
	if err != nil || !slices.ContainsFunc(queue, func(task engine.UserTask) bool { return task.TaskUid == fix.TaskUid }) {
		t.Errorf("task open to all is not in the queue of zoe: %+v, err %v", queue, err)
	}
	if err := tasks.Claim(ctx, fix.TaskUid, "zoe", nil); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Complete(ctx, fix.TaskUid, "zoe", nil); err != nil {
		t.Fatal(err)
	}
	openTask("Approve")

	if _, err := tasks.GetTask(ctx, uuid.New()); !errors.Is(err, engine.ErrTaskNotFound) {
		t.Errorf("unknown task: err = %v", err)
	}
	if _, err := tasks.ListTasks(ctx, engine.TaskFilter{Status: "open"}); !errors.Is(err, engine.ErrInvalidTaskStatus) {
		t.Errorf("unknown status: err = %v", err)
	}
}
