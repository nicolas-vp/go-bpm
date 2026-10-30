package codegen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

// ignoredElements - дочерние элементы процесса, которые не исполняются: документация, лейны, аннотации, данные
var ignoredElements = map[string]bool{
	"documentation":       true,
	"extensionElements":   true,
	"laneSet":             true,
	"textAnnotation":      true,
	"association":         true,
	"group":               true,
	"dataObject":          true,
	"dataObjectReference": true,
	"dataStoreReference":  true,
	"property":            true,
	"ioSpecification":     true,
}

// flowNode - элемент процесса, в который входят и из которого выходят sequence flow.
// incoming и outgoing - nil, если у элемента нет входов (start event, граничное событие) или выходов (end event)
type flowNode struct {
	kind     string
	id       string
	incoming *[]bpmn.Incoming
	outgoing *[]bpmn.Outgoing
	// exclusive - выбирает одну ветку по условиям
	exclusive   bool
	defaultFlow string
	// forks - несколько исходящих переходов без условий: токен идёт во все (parallel gateway, подпроцесс, callActivity)
	forks bool
}

// prepareProcess проверяет то, что генератор иначе пропустил бы молча, а экземпляр упал бы в рантайме, когда до этого
// дойдёт токен: неподдерживаемые элементы, определения событий, циклы, ветвления без шлюза. Переходы элементов без
// <bpmn:incoming>/<bpmn:outgoing> (BPMN, написанный руками) восстанавливаются по sourceRef и targetRef sequence flow
func prepareProcess(process *bpmn.Process) error {
	if err := checkElements(process); err != nil {
		return err
	}
	nodes := flowNodes(process)
	if err := linkFlows(process, nodes); err != nil {
		return err
	}
	return checkBranches(process, nodes)
}

func checkElements(process *bpmn.Process) error {
	for _, element := range process.Other {
		name := element.XMLName.Local
		if ignoredElements[name] {
			continue
		}
		if marker := bpmn.LoopMarker([]bpmn.Element{element}); marker != "" {
			return fmt.Errorf("subProcess %s: циклы (%s) не поддерживаются, только последовательный multi-instance", process.Id, marker)
		}
		return fmt.Errorf("%s %s не поддерживается", name, element.Id)
	}

	switch len(process.StartEvents) {
	case 0:
		return fmt.Errorf("нет start event")
	case 1:
	default:
		var ids []string
		for _, event := range process.StartEvents {
			ids = append(ids, event.Id)
		}
		return fmt.Errorf("несколько start event (%s) не поддерживаются, нужен один", strings.Join(ids, ", "))
	}
	for _, event := range process.StartEvents {
		if other := bpmn.UnsupportedDefinition(event.OtherDefinitions); other != "" {
			return fmt.Errorf("start event %s: %s не поддерживается, только обычный старт", event.Id, other)
		}
	}
	for _, event := range process.EndEvents {
		if other := bpmn.UnsupportedDefinition(event.OtherDefinitions); other != "" {
			return fmt.Errorf("end event %s: %s не поддерживается, только обычный конец", event.Id, other)
		}
	}

	type activity struct {
		name  string
		other []bpmn.Element
	}
	var activities []activity
	for _, task := range process.Tasks {
		activities = append(activities, activity{"task " + task.Id, task.Other})
	}
	for _, task := range process.ServiceTasks {
		activities = append(activities, activity{"serviceTask " + task.Id, task.Other})
	}
	for _, task := range process.ScriptTasks {
		activities = append(activities, activity{"scriptTask " + task.Id, task.Other})
	}
	for _, task := range process.BusinessRuleTasks {
		activities = append(activities, activity{"businessRuleTask " + task.Id, task.Other})
	}
	for _, task := range process.UserTasks {
		activities = append(activities, activity{"userTask " + task.Id, task.Other})
	}
	for _, task := range process.ReceiveTasks {
		activities = append(activities, activity{"receiveTask " + task.Id, task.Other})
	}
	for _, callActivity := range process.CallActivityList {
		activities = append(activities, activity{"callActivity " + callActivity.Id, callActivity.Other})
	}
	for _, a := range activities {
		if marker := bpmn.LoopMarker(a.other); marker != "" {
			return fmt.Errorf("%s: циклы (%s) не поддерживаются, только последовательный multi-instance", a.name, marker)
		}
	}

	for _, subProcess := range process.SubProcessList {
		if len(subProcess.SubProcessList) > 0 {
			return fmt.Errorf("subProcess %s: вложенный подпроцесс %s не поддерживается, вынесите его в callActivity",
				subProcess.Id, subProcess.SubProcessList[0].Id)
		}
	}
	return nil
}

func flowNodes(process *bpmn.Process) []flowNode {
	var nodes []flowNode
	for i := range process.StartEvents {
		e := &process.StartEvents[i]
		nodes = append(nodes, flowNode{kind: "start event", id: e.Id, outgoing: &e.OutgoingList})
	}
	for i := range process.EndEvents {
		e := &process.EndEvents[i]
		nodes = append(nodes, flowNode{kind: "end event", id: e.Id, incoming: &e.IncomingList})
	}
	for i := range process.Tasks {
		t := &process.Tasks[i]
		nodes = append(nodes, flowNode{kind: "task", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.ServiceTasks {
		t := &process.ServiceTasks[i]
		nodes = append(nodes, flowNode{kind: "serviceTask", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.ScriptTasks {
		t := &process.ScriptTasks[i]
		nodes = append(nodes, flowNode{kind: "scriptTask", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.BusinessRuleTasks {
		t := &process.BusinessRuleTasks[i]
		nodes = append(nodes, flowNode{kind: "businessRuleTask", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.UserTasks {
		t := &process.UserTasks[i]
		nodes = append(nodes, flowNode{kind: "userTask", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.ReceiveTasks {
		t := &process.ReceiveTasks[i]
		nodes = append(nodes, flowNode{kind: "receiveTask", id: t.Id, incoming: &t.IncomingList, outgoing: &t.OutgoingList})
	}
	for i := range process.ExclusiveGateways {
		g := &process.ExclusiveGateways[i]
		nodes = append(nodes, flowNode{kind: "exclusiveGateway", id: g.Id, incoming: &g.IncomingList, outgoing: &g.OutgoingList,
			exclusive: true, defaultFlow: g.Default})
	}
	for i := range process.ParallelGateways {
		g := &process.ParallelGateways[i]
		nodes = append(nodes, flowNode{kind: "parallelGateway", id: g.Id, incoming: &g.IncomingList, outgoing: &g.OutgoingList, forks: true})
	}
	for i := range process.SubProcessList {
		s := &process.SubProcessList[i]
		nodes = append(nodes, flowNode{kind: "subProcess", id: s.Id, incoming: &s.IncomingList, outgoing: &s.OutgoingList, forks: true})
	}
	for i := range process.CallActivityList {
		c := &process.CallActivityList[i]
		nodes = append(nodes, flowNode{kind: "callActivity", id: c.Id, incoming: &c.IncomingList, outgoing: &c.OutgoingList, forks: true})
	}
	for i := range process.IntermediateCatchEvents {
		e := &process.IntermediateCatchEvents[i]
		nodes = append(nodes, flowNode{kind: "intermediateCatchEvent", id: e.Id, incoming: &e.IncomingList, outgoing: &e.OutgoingList})
	}
	for i := range process.BoundaryEvents {
		e := &process.BoundaryEvents[i]
		nodes = append(nodes, flowNode{kind: "boundaryEvent", id: e.Id, outgoing: &e.OutgoingList})
	}
	return nodes
}

// linkFlows сверяет <bpmn:incoming>/<bpmn:outgoing> элементов с sourceRef и targetRef sequence flow. Пустые списки
// заполняются по sequence flow в порядке файла; заданные должны совпадать с ними, порядок берётся из элемента -
// по нему проверяются условия шлюза
func linkFlows(process *bpmn.Process, nodes []flowNode) error {
	known := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		known[node.id] = true
	}
	from := make(map[string][]string)
	to := make(map[string][]string)
	for _, flow := range process.SequenceFlowList {
		for _, ref := range []string{flow.SourceRef, flow.TargetRef} {
			if !known[ref] {
				return fmt.Errorf("sequence flow %s: элемент %q не найден в процессе", flow.Id, ref)
			}
		}
		from[flow.SourceRef] = append(from[flow.SourceRef], flow.Id)
		to[flow.TargetRef] = append(to[flow.TargetRef], flow.Id)
	}

	for _, node := range nodes {
		if node.outgoing != nil {
			ids := make([]string, 0, len(*node.outgoing))
			for _, outgoing := range *node.outgoing {
				ids = append(ids, outgoing.Id)
			}
			linked, err := linkRefs(node, "outgoing", "sourceRef", ids, from[node.id])
			if err != nil {
				return err
			}
			*node.outgoing = (*node.outgoing)[:0]
			for _, id := range linked {
				*node.outgoing = append(*node.outgoing, bpmn.Outgoing{Id: id})
			}
		} else if len(from[node.id]) > 0 {
			return fmt.Errorf("%s %s: из него не может выходить sequence flow %s", node.kind, node.id, from[node.id][0])
		}
		if node.incoming != nil {
			ids := make([]string, 0, len(*node.incoming))
			for _, incoming := range *node.incoming {
				ids = append(ids, incoming.Id)
			}
			linked, err := linkRefs(node, "incoming", "targetRef", ids, to[node.id])
			if err != nil {
				return err
			}
			*node.incoming = (*node.incoming)[:0]
			for _, id := range linked {
				*node.incoming = append(*node.incoming, bpmn.Incoming{Id: id})
			}
		} else if len(to[node.id]) > 0 {
			return fmt.Errorf("%s %s: в него не может входить sequence flow %s", node.kind, node.id, to[node.id][0])
		}
	}
	return nil
}

// linkRefs возвращает переходы элемента: заданные в нём (без пробелов по краям) или, если их нет, по sequence flow
func linkRefs(node flowNode, list, ref string, declared, flows []string) ([]string, error) {
	if len(declared) == 0 {
		return flows, nil
	}
	trimmed := make([]string, len(declared))
	for i, id := range declared {
		trimmed[i] = strings.TrimSpace(id)
	}
	if !sameSet(trimmed, flows) {
		return nil, fmt.Errorf("%s %s: переходы в <bpmn:%s> (%s) не совпадают с sequence flow, у которых %s = %s (%s)",
			node.kind, node.id, list, strings.Join(trimmed, ", "), ref, node.id, strings.Join(flows, ", "))
	}
	return trimmed, nil
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// checkBranches: условия на переходах проверяет только exclusive gateway, разветвляют только шлюзы, подпроцесс
// и callActivity. Иначе генератор выбирал бы ветку по условиям, которых нет, и шаг падал бы в рантайме
func checkBranches(process *bpmn.Process, nodes []flowNode) error {
	conditional := make(map[string]bool)
	for _, flow := range process.SequenceFlowList {
		if findFlowCondition(process, flow.Id) != "" {
			conditional[flow.Id] = true
		}
	}
	for _, node := range nodes {
		if node.outgoing == nil {
			continue
		}
		var outgoing []string
		for _, flow := range *node.outgoing {
			outgoing = append(outgoing, flow.Id)
		}
		if !node.exclusive {
			for _, id := range outgoing {
				if conditional[id] {
					return fmt.Errorf("%s %s: условие на переходе %s проверяет только exclusive gateway, поставьте его перед ветками",
						node.kind, node.id, id)
				}
			}
			if len(outgoing) > 1 && !node.forks {
				return fmt.Errorf("%s %s: несколько исходящих переходов (%s), разветвление делается шлюзом: exclusive - одна ветка по условию, parallel - все",
					node.kind, node.id, strings.Join(outgoing, ", "))
			}
			continue
		}
		if node.defaultFlow != "" && !slices.Contains(outgoing, node.defaultFlow) {
			return fmt.Errorf("%s %s: default flow %s не выходит из шлюза", node.kind, node.id, node.defaultFlow)
		}
		if len(outgoing) < 2 {
			continue
		}
		for _, id := range outgoing {
			if !conditional[id] && id != node.defaultFlow {
				return fmt.Errorf("%s %s: у перехода %s нет условия, и он не default flow - по нему токен не пойдёт никогда",
					node.kind, node.id, id)
			}
		}
	}
	return nil
}
