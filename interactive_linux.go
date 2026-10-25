//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var consoleInput = bufio.NewReader(os.Stdin)

func terminalInput() bool {
	st, e := os.Stdin.Stat()
	return e == nil && st.Mode()&os.ModeCharDevice != 0
}
func readChoice(in *bufio.Reader) (string, error) {
	s, e := in.ReadString('\n')
	if e != nil && len(s) == 0 {
		return "", e
	}
	return strings.TrimSpace(s), nil
}
func waitForClaude(in *bufio.Reader, out io.Writer, check func() error, quit func() error) error {
	for {
		e := check()
		if e == nil {
			return nil
		}
		var running *claudeRunningError
		if !errors.As(e, &running) {
			return e
		}
		fmt.Fprintln(out, "\nClaude is still running. Synchronization has not started.")
		fmt.Fprintln(out, "[R] Retry after closing Claude")
		fmt.Fprintln(out, "[F] Force quit Claude Desktop and Claude workers for your Linux user")
		fmt.Fprintln(out, "    Force quit can interrupt active work. Remaining processes are killed after 6 seconds.")
		fmt.Fprintln(out, "[C] Cancel")
		fmt.Fprint(out, "Choose R, F or C: ")
		choice, e := readChoice(in)
		if e != nil {
			return errors.New("cancelled: input closed")
		}
		switch strings.ToLower(choice) {
		case "r", "retry":
			continue
		case "f", "force":
			fmt.Fprintln(out, "Stopping detected Claude processes...")
			if e := quit(); e != nil {
				fmt.Fprintln(out, "Force quit failed:", e)
			}
		case "c", "cancel", "q":
			return errors.New("cancelled; no new synchronization was started")
		default:
			fmt.Fprintln(out, "Invalid choice. Enter R, F or C.")
		}
	}
}
func interactiveMenu(in *bufio.Reader, out io.Writer, execute func([]string) error) error {
	for {
		fmt.Fprintln(out, "\nClaude Desktop Session Sync "+version+" — Linux")
		fmt.Fprintln(out, "[1] Preview synchronization — all session configuration")
		fmt.Fprintln(out, "[2] Back up and synchronize — all session configuration")
		fmt.Fprintln(out, "[3] Preview synchronization — chat history only")
		fmt.Fprintln(out, "[4] Back up and synchronize — chat history only")
		fmt.Fprintln(out, "[5] Undo a synchronization backup")
		fmt.Fprintln(out, "[Q] Quit")
		fmt.Fprint(out, "Choose an option: ")
		choice, e := readChoice(in)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		var args []string
		switch strings.ToLower(choice) {
		case "1":
			args = []string{"plan", "--copy", "all", "--summary", "--interactive"}
		case "2":
			args = []string{"sync", "--copy", "all", "--apply", "--offline", "--summary", "--interactive"}
		case "3":
			args = []string{"plan", "--copy", "chat", "--summary", "--interactive"}
		case "4":
			args = []string{"sync", "--copy", "chat", "--apply", "--offline", "--summary", "--interactive"}
		case "5":
			fmt.Fprint(out, "Backup ID: ")
			id, e := readChoice(in)
			if e != nil {
				return e
			}
			args = []string{"undo", "--backup", id, "--apply", "--offline", "--interactive"}
		case "q", "quit":
			return nil
		default:
			fmt.Fprintln(out, "Invalid option.")
			continue
		}
		if e := execute(args); e != nil {
			fmt.Fprintln(out, "Error:", e)
		}
	}
}
