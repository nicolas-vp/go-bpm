package bpmn

import "encoding/xml"

// Message - bpmn:message в definitions: имя сообщения и, в Zeebe, ключ корреляции (zeebe:subscription)
type Message struct {
	XMLName       xml.Name       `xml:"message"`
	Id            string         `xml:"id,attr"`
	Name          string         `xml:"name,attr"`
	Subscriptions []Subscription `xml:"extensionElements>subscription"`
}

// Subscription - zeebe:subscription: correlationKey - выражение ключа корреляции
type Subscription struct {
	CorrelationKey string `xml:"correlationKey,attr"`
}

// MessageEventDefinition - событие сообщения, MessageRef ссылается на bpmn:message в definitions
type MessageEventDefinition struct {
	XMLName    xml.Name `xml:"messageEventDefinition"`
	MessageRef string   `xml:"messageRef,attr"`
}

// CorrelationKey - zeebe:subscription correlationKey сообщения, пусто - не задан
func (m *Message) CorrelationKey() string {
	for _, subscription := range m.Subscriptions {
		return subscription.CorrelationKey
	}
	return ""
}
