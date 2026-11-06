package events

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestListenKeepsUp: a listener that takes no events holds no publisher up,
// and then gets each distinct event once.
func TestListenKeepsUp(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := Listen[JobChanged](ctx, b, TopicJobOutput)
	if err != nil {
		t.Fatal(err)
	}
	// A message no subscriber takes keeps its delivery goroutine. delivering must see it, or the wait below
	// passes for nothing (a Watermill upgrade that renamed sendMessage).
	stuck, unstick := context.WithCancel(ctx)
	if _, err := b.pubsub.Subscribe(stuck, "test.stuck"); err != nil {
		t.Fatal(err)
	}
	b.Publish("test.stuck", JobChanged{})
	if !delivering() {
		t.Fatal("delivering does not see Watermill's delivery goroutines")
	}
	unstick()
	for i := range 1000 {
		b.Publish(TopicJobOutput, JobChanged{JobID: int64(1 + i%2)})
	}
	// Watermill hands each message over in goroutines of its own, which end once the relay has taken it.
	for deadline := time.Now().Add(5 * time.Second); delivering(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("messages still wait for the relay")
		}
	}
	got := map[int64]int{}
	for range 2 {
		got[(<-changes).JobID]++
	}
	select {
	case e := <-changes:
		t.Fatalf("a repeat came through: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if got[1] != 1 || got[2] != 1 {
		t.Fatalf("events: %v", got)
	}
}

// TestSubscribeSurvives: a handler that panics, or a payload it cannot read,
// is logged and its message acknowledged; later messages still come.
func TestSubscribeSurvives(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan int64, 3)
	Subscribe(b, "test", TopicJobOutput, func(_ context.Context, e JobChanged) error {
		if e.JobID == 1 {
			panic("a bad job")
		}
		got <- e.JobID
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	<-b.Running()
	b.Publish(TopicJobOutput, JobChanged{JobID: 1})
	b.Publish(TopicJobOutput, "not a job")
	b.Publish(TopicJobOutput, JobChanged{JobID: 2})
	select {
	case id := <-got:
		if id != 2 {
			t.Fatalf("got job %d", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing came after the panic")
	}
	// An unacknowledged message is sent again and again, which keeps its delivery goroutine.
	for deadline := time.Now().Add(5 * time.Second); delivering(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a message was not acknowledged")
		}
	}
}

// delivering says whether Watermill still has goroutines handing messages over.
func delivering() bool {
	buf := make([]byte, 1<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "gochannel.(*GoChannel).sendMessage")
}
