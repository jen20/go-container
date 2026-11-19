package container

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// This file holds helpers for the JSON forms that the API server reads and
// writes.

// referenceDate is the epoch of the server dates, 2001-01-01 00:00:00 UTC.
var referenceDate = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// Date is a time that encodes in JSON as a number of seconds since
// 2001-01-01 00:00:00 UTC.
type Date struct {
	time.Time
}

// MarshalJSON encodes the date as seconds since the reference date.
func (d Date) MarshalJSON() ([]byte, error) {
	secs := float64(d.Unix()-referenceDate.Unix()) + float64(d.Nanosecond())/1e9
	return strconv.AppendFloat(nil, secs, 'f', -1, 64), nil
}

// UnmarshalJSON decodes seconds since the reference date.
func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("client: invalid date %s: %w", b, err)
	}
	whole, frac := math.Modf(f)
	nsec := int64(math.Round(frac * 1e9))
	d.Time = time.Unix(referenceDate.Unix()+int64(whole), nsec).UTC()
	return nil
}

// The server encodes an enum with associated values as an object with one
// key, the case name. The value is an object of the associated values, keyed
// by their labels. A case without associated values encodes as an empty
// object, for example {"tmpfs":{}}.

// decodeEnum returns the case name and the payload of an enum value.
func decodeEnum(b []byte) (string, json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return "", nil, err
	}
	if len(m) != 1 {
		return "", nil, fmt.Errorf("client: enum value %s must have exactly one key", b)
	}
	for k, v := range m {
		return k, v, nil
	}
	panic("unreachable")
}

// encodeEnum encodes an enum value. A nil payload encodes as {}.
func encodeEnum(name string, payload any) ([]byte, error) {
	if payload == nil {
		payload = struct{}{}
	}
	return json.Marshal(map[string]any{name: payload})
}

// simpleEnum is an enum without raw values or associated values, such
// as Filesystem.CacheMode. It encodes as {"case":{}}.
type simpleEnum string

func (e simpleEnum) MarshalJSON() ([]byte, error) {
	return encodeEnum(string(e), nil)
}

func (e *simpleEnum) UnmarshalJSON(b []byte) error {
	name, _, err := decodeEnum(b)
	if err != nil {
		return err
	}
	*e = simpleEnum(name)
	return nil
}

// emptyIfNil returns an empty slice for nil. The server requires a value for
// a non-optional array, and Go encodes a nil slice as null.
func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// emptyMapIfNil returns an empty map for nil, for the same reason.
func emptyMapIfNil[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return m
}
