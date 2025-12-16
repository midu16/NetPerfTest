package ebpf

import (
	"testing"
)

func TestNewLoader(t *testing.T) {
	config := &Config{
		Hook:      HookXDP,
		Interface: "eth0",
		Loops:     1000,
	}

	loader := NewLoader(config)
	if loader == nil {
		t.Fatal("NewLoader returned nil")
	}

	if loader.config != config {
		t.Error("Loader config not set correctly")
	}
}

func TestHookTypeValidation(t *testing.T) {
	validHooks := []HookType{
		HookXDP,
		HookTCIngress,
		HookTCEgress,
		HookSocket,
	}

	for _, hook := range validHooks {
		if hook == "" {
			t.Errorf("Hook type %s is empty", hook)
		}
	}
}
