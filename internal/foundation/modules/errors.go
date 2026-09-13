package modules

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func apiError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}
