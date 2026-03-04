package main

import (
	"strings"
	"testing"
)

func TestDispatchVersion(t *testing.T) {
	if err := dispatch([]string{"version"}); err != nil {
		t.Fatalf("dispatch version: %v", err)
	}
}

func TestDispatchNoArgs(t *testing.T) {
	err := dispatch(nil)
	if err == nil {
		t.Fatal("expected error with no arguments")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	err := dispatch([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDispatchList(t *testing.T) {
	if err := dispatch([]string{"list"}); err != nil {
		t.Fatalf("dispatch list: %v", err)
	}
}

func TestDispatchLsAlias(t *testing.T) {
	if err := dispatch([]string{"ls"}); err != nil {
		t.Fatalf("dispatch ls: %v", err)
	}
}

func TestDispatchStartMissingName(t *testing.T) {
	err := dispatch([]string{"start"})
	if err == nil {
		t.Fatal("expected error for start without --name")
	}
}

func TestDispatchStopMissingName(t *testing.T) {
	err := dispatch([]string{"stop"})
	if err == nil {
		t.Fatal("expected error for stop without --name")
	}
}

func TestDispatchPauseMissingName(t *testing.T) {
	err := dispatch([]string{"pause"})
	if err == nil {
		t.Fatal("expected error for pause without --name")
	}
}

func TestDispatchResumeMissingName(t *testing.T) {
	err := dispatch([]string{"resume"})
	if err == nil {
		t.Fatal("expected error for resume without --name")
	}
}

func TestDispatchSnapshotMissingName(t *testing.T) {
	err := dispatch([]string{"snapshot"})
	if err == nil {
		t.Fatal("expected error for snapshot without --name")
	}
}

func TestDispatchSnapshotLs(t *testing.T) {
	if err := dispatch([]string{"snapshot", "ls"}); err != nil {
		t.Fatalf("dispatch snapshot ls: %v", err)
	}
}

func TestDispatchSnapshotDeleteMissingID(t *testing.T) {
	err := dispatch([]string{"snapshot", "delete"})
	if err == nil {
		t.Fatal("expected error for snapshot delete without --id")
	}
}
