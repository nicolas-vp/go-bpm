package bpmn

import "encoding/xml"

// Message - bpmn:message в definitions: имя сообщения и, в Zeebe, ключ корреляции (zeebe:subscription)
type Message struct {
	XMLName       xml.Name       `xml:"message"`
	Id            string         `xml:"id,attr"`
	Name          string         `xml:"name,attr"`
	Subscriptions []Subscription `xml:"extensionElements>subscription"`
}

// Subscription - gobpm:subscription или zeebe:subscription: correlationKey - выражение ключа корреляции
type Subscription struct {
	XMLName        xml.Name `xml:"subscription"`
	CorrelationKey string   `xml:"correlationKey,attr"`
}

func (s Subscription) xmlName() xml.Name { return s.XMLName }

// MessageEventDefinition - событие сообщения, MessageRef ссылается на bpmn:message в definitions
type MessageEventDefinition struct {
	XMLName    xml.Name `xml:"messageEventDefinition"`
	MessageRef string   `xml:"messageRef,attr"`
}

// CorrelationKey - correlationKey gobpm:subscription, без него - zeebe:subscription; пусто - не задан
func (m *Message) CorrelationKey() string {
	subscription, _ := pickExtension(m.Subscriptions)
	return subscription.CorrelationKey
}
