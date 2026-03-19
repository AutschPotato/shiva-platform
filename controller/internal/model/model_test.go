package model

import "testing"

func TestK6StatusIsReadyForStart(t *testing.T) {
	t.Run("paused and not stopped", func(t *testing.T) {
		status := &K6Status{Status: []byte(`2`), Paused: true, Stopped: false}
		if !status.IsReadyForStart() {
			t.Fatalf("expected paused worker with stopped=false to be ready for start")
		}
	})

	t.Run("paused but stopped", func(t *testing.T) {
		status := &K6Status{Paused: true, Stopped: true}
		if status.IsReadyForStart() {
			t.Fatalf("expected stopped worker to be rejected as start-ready")
		}
	})

	t.Run("not paused", func(t *testing.T) {
		status := &K6Status{Status: []byte(`2`), Paused: false, Stopped: false}
		if status.IsReadyForStart() {
			t.Fatalf("expected non-paused worker to be rejected as start-ready")
		}
	})

	t.Run("finished status", func(t *testing.T) {
		status := &K6Status{Status: []byte(`7`), Paused: true, Stopped: false}
		if status.IsReadyForStart() {
			t.Fatalf("expected terminal worker status to be rejected as start-ready")
		}
	})
}
