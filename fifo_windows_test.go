package main

import "errors"

func makeFIFO(path string, mode uint32) error {
	return errors.New("POSIX FIFOs are unavailable on Windows")
}
