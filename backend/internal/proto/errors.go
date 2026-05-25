package proto

import "errors"

var ErrUnknownEventType = errors.New("proto: unknown upstream event type")

var ErrNotImplemented = errors.New("proto: not implemented")
