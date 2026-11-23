package model

import (
	"errors"
	"strings"
	"testing"
)

func TestParseExpectedStatusBoundsLength(t *testing.T) {
	if _, _, err := ParseExpectedStatus(strings.Repeat("9", MaxExpectedStatusLength+1)); !errors.Is(err, ErrHTTPStatus) {
		t.Fatalf("overlong expected status err = %v, want ErrHTTPStatus", err)
	}
	minCode, maxCode, err := ParseExpectedStatus("200-299")
	if err != nil || minCode != 200 || maxCode != 299 {
		t.Fatalf("ParseExpectedStatus(200-299) = %d,%d,%v", minCode, maxCode, err)
	}
}
