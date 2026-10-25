package parser

import (
	"encoding/xml"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/bpmn"
)

func ParseBpm(source []byte) (*bpmn.Definitions, error) {
	var definitions bpmn.Definitions
	err := xml.Unmarshal(source, &definitions)
	return &definitions, err
}
