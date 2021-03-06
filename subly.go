// Package subly helps with subscribing methods on a struct type as callbacks for NATS, with some naming conventions.
//
// Assuming we have a service like:
//
//	type someService struct{}
//	func (*someService) SubActionMessage(p *person) {}
//	func (*someService) RepActionMessageQueue(subject, reply string, p *person) {}
//
// then SubActionMessage would get subscribed to subject:
//
//	someservice.subaction
//
// and RepActionMessageQueue would get subscribed to subject:
//
//	someservice.repaction
//
// subject naming convension is <struct type name>.<method name> all lower case,
// with words message and queue removed from the end.
//
// If a method name ends in Message, it will subscribe to subject as a normall
// subscriber (just receiving). If a method name ends in MessageQueue, it will subscribe
// to subject as a member of a queue and the queue name will be <struct type name>_<method name>.
//
// Message methods are expected to have one of four signatures.
//
//	type person struct {
//		Name string `json:"name,omitempty"`
//		Age  uint   `json:"age,omitempty"`
//	}
//
//	handler := func(m *Msg)
//	handler := func(p *person)
//	handler := func(subject string, o *obj)
//	handler := func(subject, reply string, o *obj)
//
// Which are NATS's conventions for callbacks. A sample usage would look like:
//
//	s := NewSubscriber(ctx, econn)
//	s.Subscribe(&timeService{econn})
//
// And the callback methods will unsubscribe from subject when context got canceled.
package subly

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"

	nats "github.com/nats-io/go-nats"
)

func polishKindName(name string, take, drop int) string {
	ix := strings.LastIndex(name, "/")
	if ix > 0 && (ix+1) < len(name) {
		name = name[ix+1:]
	}
	rp := strings.NewReplacer("(", "", ")", "", "*", "")
	name = rp.Replace(name)
	parts := strings.Split(name, ".")
	if take < 0 {
		take = 0
	}
	if take < len(parts) {
		parts = parts[(len(parts) - take):]
	}
	if drop > 0 && drop < len(parts) {
		parts = parts[:(len(parts) - drop)]
	}
	name = strings.Join(parts, ".")
	return name
}

type serviceMessage struct {
	queue                    bool
	serviceName, messageName string
	message                  interface{}
}

func getMessages(service interface{}) []serviceMessage {
	var res []serviceMessage

	t := reflect.TypeOf(service)
	val := reflect.ValueOf(service)
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)

		var isMessage, isMessageQueue bool
		if strings.HasSuffix(m.Name, "Message") {
			isMessage = true
		}
		if strings.HasSuffix(m.Name, "MessageQueue") {
			isMessageQueue = true
		}
		if !isMessage && !isMessageQueue {
			continue
		}

		messageName := strings.TrimSuffix(m.Name, "Queue")
		messageName = strings.TrimSuffix(messageName, "Message")
		messageName = strings.ToLower(messageName)

		sm := serviceMessage{
			message: val.MethodByName(m.Name).Interface(),
			serviceName: strings.ToLower(
				polishKindName(t.String(), 1, 0)),
			messageName: messageName,
		}
		if isMessageQueue {
			sm.queue = true
		}

		res = append(res, sm)
	}

	return res
}

func sub(
	ctx context.Context,
	econn *nats.EncodedConn,
	subject string,
	x interface{}) {
	sub, err := econn.Subscribe(subject, x)
	if err != nil {
		log.Println("error:", err)
		return
	}
	go func() {
		<-ctx.Done()
		err := sub.Unsubscribe()
		if err != nil {
			log.Println("error:", err)
		}
	}()
}

func qsub(
	ctx context.Context,
	econn *nats.EncodedConn,
	queue, subject string,
	x interface{}) {
	sub, err := econn.QueueSubscribe(subject, queue, x)
	if err != nil {
		log.Println("error:", err)
		return
	}
	go func() {
		<-ctx.Done()
		err := sub.Unsubscribe()
		if err != nil {
			log.Println("error:", err)
		}
	}()
}

// Subscriber subscribes methods on a struct type as callbacks for NATS
type Subscriber struct {
	ctx   context.Context
	econn *nats.EncodedConn
}

// NewSubscriber creates new Subscriber
func NewSubscriber(ctx context.Context, econn *nats.EncodedConn) *Subscriber {
	return &Subscriber{
		ctx:   ctx,
		econn: econn,
	}
}

// Subscribe subscribes methods on a struct type as callbacks for NATS.
// Message func signature must follow NATS conventions as described in package documentation.
func (s *Subscriber) Subscribe(service interface{}) {
	messages := getMessages(service)
	for _, v := range messages {
		v := v
		subject := fmt.Sprintf("%s.%s", v.serviceName, v.messageName)
		if v.queue {
			queueName := fmt.Sprintf("%s_%s", v.serviceName, v.messageName)
			qsub(
				s.ctx,
				s.econn,
				queueName,
				subject,
				v.message)
			continue
		}
		sub(
			s.ctx,
			s.econn,
			subject,
			v.message)
	}
}

// SubscribeFunc subscribes methods in values of the provided map as callbacks for NATS.
// If queue name is provided, methods will get subscribed in the queue.
// Message func signature must follow NATS conventions as described in package documentation.
func (s *Subscriber) SubscribeFunc(messages map[string]interface{}, queue ...string) {
	var queueName string
	if len(queue) > 0 {
		queueName = queue[0]
	}
	for sb, m := range messages {
		sb, m := sb, m
		subject := sb
		if queueName != "" {
			qsub(
				s.ctx,
				s.econn,
				queueName,
				subject,
				m)
			continue
		}
		sub(
			s.ctx,
			s.econn,
			subject,
			m)
	}
}
