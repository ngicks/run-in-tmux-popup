package runinpopup

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if code, ok := runFakePinentry(); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
