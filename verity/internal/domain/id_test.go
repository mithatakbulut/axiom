package domain

import "testing"

func TestNewIDAndValidate(t *testing.T) {
	id, err := NewID("wf")
	if err != nil {
		t.Fatal(err)
	}
	if err = id.Validate("wf"); err != nil {
		t.Fatal(err)
	}
	if ID("wf_bad").Validate("wf") == nil {
		t.Fatal("expected malformed ID rejection")
	}
}
