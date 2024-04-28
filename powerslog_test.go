package powerslog

import "testing"

func TestNewHandler(t *testing.T) {
	h1 := NewHandler(nil, nil)
	h2 := NewHandler(nil, nil)
	if h1 == h2 {
		t.Errorf("NewHandler() = %v, want new instance", h1)
	}
}
