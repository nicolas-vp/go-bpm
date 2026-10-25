// Package dmn - модель DMN-файла (Camunda Modeler): решения с таблицами правил
package dmn

import "encoding/xml"

type Definitions struct {
	XMLName   xml.Name   `xml:"definitions"`
	Id        string     `xml:"id,attr"`
	Name      string     `xml:"name,attr"`
	Decisions []Decision `xml:"decision"`
	// Path - путь файла относительно папки process через "/"
	Path string `xml:"-"`
}

type Decision struct {
	Id            string         `xml:"id,attr"`
	Name          string         `xml:"name,attr"`
	DecisionTable *DecisionTable `xml:"decisionTable"`
	// LiteralExpression - решение выражением вместо таблицы, не поддерживается
	LiteralExpression       *struct{}                `xml:"literalExpression"`
	InformationRequirements []InformationRequirement `xml:"informationRequirement"`
}

// InformationRequirement - зависимость решения: от другого решения (requiredDecision) или входных данных (requiredInput)
type InformationRequirement struct {
	RequiredDecision *Reference `xml:"requiredDecision"`
	RequiredInput    *Reference `xml:"requiredInput"`
}

type Reference struct {
	Href string `xml:"href,attr"`
}

type DecisionTable struct {
	Id string `xml:"id,attr"`
	// HitPolicy - UNIQUE (по умолчанию), FIRST, ANY, PRIORITY, COLLECT, RULE ORDER, OUTPUT ORDER
	HitPolicy string `xml:"hitPolicy,attr"`
	// Aggregation - для COLLECT: SUM, MIN, MAX, COUNT
	Aggregation string   `xml:"aggregation,attr"`
	Inputs      []Input  `xml:"input"`
	Outputs     []Output `xml:"output"`
	Rules       []Rule   `xml:"rule"`
}

type Input struct {
	Id              string          `xml:"id,attr"`
	Label           string          `xml:"label,attr"`
	InputExpression InputExpression `xml:"inputExpression"`
}

type InputExpression struct {
	TypeRef string `xml:"typeRef,attr"`
	Text    string `xml:"text"`
}

type Output struct {
	Id      string `xml:"id,attr"`
	Label   string `xml:"label,attr"`
	Name    string `xml:"name,attr"`
	TypeRef string `xml:"typeRef,attr"`
}

type Rule struct {
	Id            string  `xml:"id,attr"`
	Description   string  `xml:"description"`
	InputEntries  []Entry `xml:"inputEntry"`
	OutputEntries []Entry `xml:"outputEntry"`
}

// Entry - ячейка правила: unary tests у входа, выражение у выхода
type Entry struct {
	Id   string `xml:"id,attr"`
	Text string `xml:"text"`
}
