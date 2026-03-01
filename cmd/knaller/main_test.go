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
	// List with no running VMs should succeed and just print the table header.
	if err := dispatch([]string{"list"}); err != nil {
		t.Fatalf("dispatch list: %v", err)
	}
}

func TestDispatchLsAlias(t *testing.T) {
	// "ls" should work the same as "list".
	if err := dispatch([]string{"ls"}); err != nil {
		t.Fatalf("dispatch ls: %v", err)
	}
}
