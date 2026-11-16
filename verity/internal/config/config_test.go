package config

import "testing"

func TestValidation(t *testing.T) {
	c := Defaults()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.LogLevel = "trace"
	if c.Validate() == nil {
		t.Fatal("expected invalid level")
	}
}
