package powerslog

import "testing"

func TestNewHandler(t *testing.T) {
	_ = NewHandler(nil)
}
