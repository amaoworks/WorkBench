package main

import "testing"

func TestApplyControlStateIsPerRegistration(t *testing.T) {
	mod := &module{registrationID: "old", generation: 4, enabled: false}
	if err := mod.applyControlState("new", 1, true); err != nil {
		t.Fatalf("new registration rejected: %v", err)
	}
	if mod.registrationID != "new" || mod.generation != 1 || !mod.enabled {
		t.Fatalf("state = %+v", *mod)
	}
	if err := mod.applyControlState("new", 0, false); err != errStaleGeneration {
		t.Fatalf("same registration accepted older generation: %v", err)
	}
	mod.enabled = true
	if err := mod.applyControlState("other", 1, true); err != errRegistrationConflict {
		t.Fatalf("enabled module accepted new registration: %v", err)
	}
}
