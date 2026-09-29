package xerr

import "fmt"

type Error struct {
	Status  int
	Code    string
	Message string
	Err     error
}

func New(status int, code string, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func Wrap(status int, code string, message string, err error) *Error {
	return &Error{Status: status, Code: code, Message: message, Err: err}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	return e.Err
}
