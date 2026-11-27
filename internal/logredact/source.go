package logredact

import "regexp"

// Keep the whole URL out of diagnostics, including signed queries and
// plugin IDs such as nzb:http://… that embed the download URL.
var sourceURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s'"<>]+`)

// Source removes source URLs and other recognized credentials from text.
func Source(s string) string {
	s = sourceURL.ReplaceAllString(s, "[url]")
	return String(encodedURL.ReplaceAllString(s, "[url]"))
}

// SourceError preserves the error chain while redacting the displayed message.
func SourceError(err error) error {
	if err == nil {
		return nil
	}
	original := err.Error()
	if message := Source(original); message != original {
		return &sourceError{message: message, cause: err}
	}
	return err
}

type sourceError struct {
	message string
	cause   error
}

func (e *sourceError) Error() string { return e.message }
func (e *sourceError) Unwrap() error { return e.cause }
