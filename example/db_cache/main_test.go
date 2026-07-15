package main

import (
	"testing"

	"github.com/thkhxm/tgf/v2/db"
)

func TestFailureQueueReplayWithoutExternalServices(t *testing.T) {
	queue := db.NewMemoryFailureQueue()
	payload := db.FailurePayload([]byte(`{"table":"t_user","count":1,"values":["u001","Alice"]}`))
	if err := queue.Enqueue(payload); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	called := 0
	if err := db.ReplayFailureQueue(queue, func(got db.FailurePayload) error {
		called++
		_, err := db.DecodeFailurePayload(got)
		return err
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if called != 1 || queue.Len() != 0 {
		t.Fatalf("called=%d queue.Len=%d", called, queue.Len())
	}
}
