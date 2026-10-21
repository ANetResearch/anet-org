package org

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a refusal with a short machine code. The server writes it as the
// anet service module's error shape: an HTTP 4xx (never 429 or 503, which
// the daemon turns into "busy" and so would lose the code) with the body
// {"error": "<code>", "message": "<sentence>"}. The daemon passes the code
// to the caller word for word; the message stays in the node's log and is
// read by direct callers and the tests.
type Error struct {
	Code       string
	Status     int
	Message    string
	RetryAfter int // seconds, for rate_limited; sent as Retry-After
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ErrorCode returns the code and the message (for the assistant's tools).
func (e *Error) ErrorCode() (string, string) { return e.Code, e.Message }

func newErr(status int, code, format string, a ...any) *Error {
	return &Error{Code: code, Status: status, Message: fmt.Sprintf(format, a...)}
}

// AsError returns err as an *Error, or nil.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// Error codes. Every code matches ^[a-z][a-z0-9_]{0,29}$, the form the
// daemon passes on verbatim.
const (
	CodeUnauthorized       = "unauthorized"
	CodeCallerRequired     = "caller_required"
	CodeBadCaller          = "bad_caller"
	CodeUnknownCapability  = "unknown_capability"
	CodeCapabilityMismatch = "capability_mismatch"
	CodeBadJSON            = "bad_json"
	CodeBadArgs            = "bad_args"
	CodeUnknownField       = "unknown_field"
	CodeMissingField       = "missing_field"
	CodeTooLong            = "too_long"
	CodeFilesNotAccepted   = "files_not_accepted"
	CodeNotAMember         = "not_a_member"
	CodeNotAdmin           = "not_admin"
	CodeNotPermitted       = "not_permitted"
	CodeAlreadyMember      = "already_member"
	CodeJoinRefused        = "join_refused"
	CodeInviteUnknown      = "invite_unknown"
	CodeInviteExpired      = "invite_expired"
	CodeInviteRevoked      = "invite_revoked"
	CodeInviteUsedUp       = "invite_used_up"
	CodeInvalidHandle      = "invalid_handle"
	CodeHandleReserved     = "handle_reserved"
	CodeHandleTaken        = "handle_taken"
	CodeRenameTooSoon      = "rename_too_soon"
	CodeConfirmRequired    = "confirm_required"
	CodeRecipientUnknown   = "recipient_unknown"
	CodeItemUnknown        = "item_unknown"
	CodeItemClosed         = "item_closed"
	CodeAlreadyClaimed     = "already_claimed"
	CodeProjectUnknown     = "project_unknown"
	CodeProjectExists      = "project_exists"
	CodeMemberUnknown      = "member_unknown"
	CodeRateLimited        = "rate_limited"
	CodeBudgetExhausted    = "budget_exhausted"
	CodeAssistantFailed    = "assistant_failed"
	CodeInternal           = "internal_error"
	CodeBusy               = "busy"
	CodeInvalidValue       = "invalid_value"
	CodeLimitReached       = "limit_reached"
	CodeAssistantOff       = "assistant_unavailable"
	CodeMailboxFull        = "mailbox_full"
	CodeTooManyUnread      = "too_many_unread"
	CodeQuotaExceeded      = "quota_exceeded"
	CodeNoteUnknown        = "note_unknown"
	CodeForwardRefused     = "forward_refused"
)

func errCallerRequired() *Error {
	return newErr(http.StatusForbidden, CodeCallerRequired,
		"This capability is available only to authenticated callers on the network.")
}

func errNotMember() *Error {
	return newErr(http.StatusForbidden, CodeNotAMember,
		"The caller is not a member of this organisation. Join with org.join and an invite code.")
}

func errNotAdmin() *Error {
	return newErr(http.StatusForbidden, CodeNotAdmin, "The caller is not an administrator of this organisation.")
}

func errNotPermitted(what string) *Error {
	return newErr(http.StatusForbidden, CodeNotPermitted, "Only %s may do this.", what)
}

func errRate(retry int) *Error {
	e := newErr(http.StatusForbidden, CodeRateLimited, "The rate limit for this kind of call was reached. Retry in %d seconds.", retry)
	e.RetryAfter = retry
	return e
}

func errTooLong(field string, max int) *Error {
	return newErr(http.StatusBadRequest, CodeTooLong, "The field %q is longer than %d bytes.", field, max)
}

func errMissing(field string) *Error {
	return newErr(http.StatusBadRequest, CodeMissingField, "The field %q is required.", field)
}

func errInvalid(field, why string) *Error {
	return newErr(http.StatusBadRequest, CodeInvalidValue, "The field %q is not valid: %s", field, why)
}

func errInternal() *Error {
	return newErr(http.StatusInternalServerError, CodeInternal, "The organisation agent could not complete the request.")
}
