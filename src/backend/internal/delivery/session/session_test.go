package session

import (
	"errors"
	"testing"
	"time"
)

func TestAValueForWorkIsNotACookieAndTheOtherWayRound(t *testing.T) {
	s, err := NewSealer(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	sess := Session{UserID: 7, Token: "tok", Expires: time.Now().Add(time.Hour).Unix()}
	cookie, err := s.Seal(sess)
	if err != nil {
		t.Fatal(err)
	}

	work, err := s.SealForWork(sess)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.Open(cookie); err != nil || got.Token != "tok" {
		t.Fatalf("a cookie opens as a cookie: %+v %v", got, err)
	}

	if got, err := s.OpenWork(work); err != nil || got.UserID != 7 {
		t.Fatalf("a value for work opens for work: %+v %v", got, err)
	}

	if _, err := s.Open(work); !errors.Is(err, ErrInvalid) {
		t.Fatalf("what Temporal keeps must not work as a cookie: %v", err)
	}

	if _, err := s.OpenWork(cookie); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a cookie is not a value for work: %v", err)
	}
}

func TestAnExpiredSessionDoesNotOpen(t *testing.T) {
	s, _ := NewSealer(make([]byte, 32))
	old, _ := s.SealForWork(Session{UserID: 7, Expires: time.Now().Add(-time.Minute).Unix()})
	if _, err := s.OpenWork(old); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
}
