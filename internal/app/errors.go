package app

import "errors"

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e exitError) Unwrap() error {
	return e.err
}

func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return exitError{code: code, err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee exitError
	if errors.As(err, &ee) && ee.code != 0 {
		return ee.code
	}
	return 1
}
