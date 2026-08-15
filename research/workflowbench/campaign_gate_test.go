package workflowbench

import (
	"context"
	"testing"
	"time"
)

func TestPhysicalGateCancellationAdvancesFIFOQueue(t *testing.T) {
	gate := newPhysicalGate(1)
	firstRelease, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := gate.acquire(cancelled)
		secondDone <- err
	}()
	time.Sleep(time.Millisecond)
	thirdDone := make(chan error, 1)
	go func() {
		release, err := gate.acquire(context.Background())
		if err == nil {
			release()
		}
		thirdDone <- err
	}()
	cancel()
	firstRelease()
	select {
	case err := <-secondDone:
		if err == nil {
			t.Fatal("cancelled waiter acquired physical slot")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	select {
	case err := <-thirdDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled ticket blocked later FIFO waiter")
	}
}
