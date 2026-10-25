package model

type DeploymentProcess struct {
	ProcessID    string
	StartElement string
	Expressions  map[string]*Expression
}
