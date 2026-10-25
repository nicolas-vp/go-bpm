package parser

import (
	"encoding/xml"

	"github.com/nicolas-vp/go-bpm/internal/generator/models/dmn"
)

func ParseDmn(source []byte) (*dmn.Definitions, error) {
	var definitions dmn.Definitions
	err := xml.Unmarshal(source, &definitions)
	return &definitions, err
}
