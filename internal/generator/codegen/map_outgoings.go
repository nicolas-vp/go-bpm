package codegen

import (
	"strings"

	"github.com/nicolas-vp/go-bpm/bpm"
	model "github.com/nicolas-vp/go-bpm/internal/generator/models"
	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

func MapOutgoings(
	outgoingList *[]bpmn.Outgoing,
	defaultFlow string,
	process *bpmn.Process,
	isParallel bool) (bpm.CompleteType, []string, *model.Condition) {
	outgoings := make([]string, 0)
	if len(*outgoingList) > 1 && !isParallel {
		return bpm.CompleteTypeScript, outgoings, createCondition(outgoingList, defaultFlow, process)
	} else {
		for _, s := range *outgoingList {
			outgoings = append(outgoings, s.Id)
		}
		return bpm.CompleteTypeStraight, outgoings, nil
	}
}

// createCondition собирает условия исходящих flow; flow без условия попадает только как default
func createCondition(outgoingList *[]bpmn.Outgoing, defaultFlow string, process *bpmn.Process) *model.Condition {
	condition := &model.Condition{DefaultFlow: defaultFlow}
	for _, cond := range *outgoingList {
		if expression := findFlowCondition(process, cond.Id); expression != "" {
			condition.Branches = append(condition.Branches, model.ConditionBranch{
				FlowId:     cond.Id,
				Expression: expression,
			})
		}
	}
	return condition
}

func findFlowCondition(process *bpmn.Process, flowName string) string {
	for _, s := range process.SequenceFlowList {
		if s.Id == flowName && s.ConditionList != nil {
			for _, c := range s.ConditionList {
				return removeFirstEqual(replaceSpecialXmlSymbols(c.Condition))
			}
		}
	}
	return ""
}

func replaceSpecialXmlSymbols(inputString string) string {
	replacements := [][]string{
		{"&lt;", "<"}, {"&gt;", ">"}, {"&amp;", "&"}, {"&quot;", "\""}, {"&apos;", "'"},
	}

	for _, s := range replacements {
		inputString = strings.ReplaceAll(inputString, s[0], s[1])
	}
	return inputString
}

func removeFirstEqual(condition string) string {
	if len(condition) > 1 && condition[0:1] == "=" {
		return condition[1:]
	} else {
		return condition
	}
}
