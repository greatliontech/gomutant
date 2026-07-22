package counting

import (
	"os"
	"testing"
)

func TestCounting(t *testing.T) {
	if path := os.Getenv("GOMUTANT_EXECUTION_COUNTER"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString("x\n"); err != nil {
			t.Fatal(err)
		}
	}
	_ = Value()
}

func TestCountingStrict(t *testing.T) {
	if path := os.Getenv("GOMUTANT_EXECUTION_COUNTER"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString("x\n"); err != nil {
			t.Fatal(err)
		}
	}
	if Value() != 7 {
		t.Fatal("value")
	}
}
