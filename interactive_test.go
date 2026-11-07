//go:build linux || windows || darwin

package main

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func runningFixture() error { return &claudeRunningError{Processes: []claudeProcess{{PID: 12345}}} }
func TestInteractiveRetry(t *testing.T) {
	checks := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("r\n")), &out, func() error {
		checks++
		if checks == 1 {
			return runningFixture()
		}
		return nil
	}, func() error { t.Fatal("retry force-quit processes"); return nil })
	if e != nil || checks != 2 || !strings.Contains(out.String(), "[F] Force quit") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveForceQuit(t *testing.T) {
	stopped := false
	calls := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("f\n")), &out, func() error {
		if !stopped {
			return runningFixture()
		}
		return nil
	}, func() error { calls++; stopped = true; return nil })
	if e != nil || calls != 1 || !strings.Contains(out.String(), "Stopping detected Claude processes") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveCancelAndEOF(t *testing.T) {
	for _, input := range []string{"c\n", ""} {
		var out bytes.Buffer
		e := waitForClaude(bufio.NewReader(strings.NewReader(input)), &out, runningFixture, func() error { t.Fatal("cancel force-quit processes"); return nil })
		if e == nil {
			t.Fatal("cancel accepted")
		}
	}
}
func TestInteractiveInvalidAndFailedForceThenRetry(t *testing.T) {
	checks := 0
	quitCalls := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("invalid\nf\nr\n")), &out, func() error {
		checks++
		if checks <= 3 {
			return runningFixture()
		}
		return nil
	}, func() error { quitCalls++; return errors.New("permission denied") })
	if e != nil || quitCalls != 1 || !strings.Contains(out.String(), "Invalid choice") || !strings.Contains(out.String(), "Force quit failed") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveInspectionFailureDoesNotOfferForce(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("cannot inspect")
	if e := waitForClaude(bufio.NewReader(strings.NewReader("f\n")), &out, func() error { return want }, func() error { t.Fatal("unexpected force"); return nil }); e != want || out.Len() != 0 {
		t.Fatal(e, out.String())
	}
}
func TestEnglishMenuRoutesOperations(t *testing.T) {
	var calls [][]string
	var out bytes.Buffer
	e := interactiveMenu(bufio.NewReader(strings.NewReader("1\n2\n3\n4\n5\n20261025T120000.000000000Z\nq\n")), &out, func(a []string) error { calls = append(calls, a); return nil })
	if e != nil || len(calls) != 5 {
		t.Fatal(e, calls)
	}
	if calls[0][0] != "plan" || calls[1][0] != "sync" || calls[3][2] != "chat" || calls[4][0] != "undo" {
		t.Fatal(calls)
	}
	if !strings.Contains(out.String(), "Back up and synchronize") || !strings.Contains(out.String(), "Backup ID:") {
		t.Fatal("English labels missing")
	}
}
func TestMenuRecoversFromErrorAndInvalidChoice(t *testing.T) {
	var out bytes.Buffer
	n := 0
	e := interactiveMenu(bufio.NewReader(strings.NewReader("wrong\n1\nq\n")), &out, func([]string) error { n++; return errors.New("test failure") })
	if e != nil || n != 1 || !strings.Contains(out.String(), "Error: test failure") {
		t.Fatal(e, out.String())
	}
}
