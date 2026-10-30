package engine

import (
	"fmt"
	"maps"

	"github.com/nicolas-vp/go-bpm/bpm"
)

// Последовательный multi-instance (bpm.Loop). Номер выполнения хранится в токене (instanceMessage.Loops), поэтому
// цикл переживает рестарт и откат шага вместе с остальным состоянием токена. Элемент выполняется как обычно,
// а completeElement вместо перехода дальше активирует его снова, пока цикл не закончен

// loopKey - ключ счётчика цикла элемента в экземпляре: callActivity может вызвать тот же процесс,
// и id элемента повторится в дочернем экземпляре
func loopKey(message *instanceMessage, expression *bpm.Expression) string {
	return message.InstanceUid.String() + "/" + expression.ElementId
}

// enterLoop вызывается при каждой активации элемента с циклом: первая начинает цикл с 1,
// номер выполнения кладётся в переменную bpm.LoopCounterVariable
func enterLoop(message *instanceMessage, expression *bpm.Expression) {
	key := loopKey(message, expression)
	if message.Loops == nil {
		message.Loops = make(map[string]int)
	}
	if message.Loops[key] == 0 {
		message.Loops[key] = 1
	}
	setLoopCounter(message, message.Loops[key])
}

// nextIteration вызывается, когда элемент с циклом выполнен: true - выполнить его ещё раз (счётчик уже увеличен),
// false - цикл закончен, счётчик и переменная номера удалены
func nextIteration(message *instanceMessage, expression *bpm.Expression) (bool, error) {
	loop := expression.Loop
	key := loopKey(message, expression)
	// счётчика нет у токена, который пришёл в элемент до того, как у него появился цикл: это первое выполнение
	counter := max(message.Loops[key], 1)
	done := loop.Cardinality > 0 && counter >= loop.Cardinality
	if !done && loop.CompletionCondition != nil {
		setLoopCounter(message, counter)
		var err error
		if done, err = loop.CompletionCondition(message.Variables); err != nil {
			return false, fmt.Errorf("process %q: element %q: loop completion condition: %w", message.ProcessName, expression.ElementId, err)
		}
	}
	if done {
		leaveLoop(message, expression)
		return false, nil
	}
	if message.Loops == nil {
		message.Loops = make(map[string]int)
	}
	message.Loops[key] = counter + 1
	return true, nil
}

// leaveLoop убирает из токена состояние цикла: цикл закончен или элемент прервало граничное событие
func leaveLoop(message *instanceMessage, expression *bpm.Expression) {
	delete(message.Loops, loopKey(message, expression))
	if len(message.Loops) == 0 {
		message.Loops = nil
	}
	if _, found := message.Variables[bpm.LoopCounterVariable]; found {
		message.Variables = maps.Clone(message.Variables)
		delete(message.Variables, bpm.LoopCounterVariable)
	}
}

// setLoopCounter пишет номер выполнения в переменные. Переменные копируются: map может делить параллельная ветка
// или снимок переменных родителя
func setLoopCounter(message *instanceMessage, counter int) {
	variables := make(map[string]any, len(message.Variables)+1)
	maps.Copy(variables, message.Variables)
	variables[bpm.LoopCounterVariable] = counter
	message.Variables = variables
}
