package layout

import "testing"

func TestParseHexRGBAndNormalizeHex6(t *testing.T) {
	r, g, b, ok := ParseHexRGB("#f0a")
	if !ok || r != 0xff || g != 0x00 || b != 0xaa {
		t.Fatalf("ParseHexRGB(#f0a) = (%x,%x,%x,%v)", r, g, b, ok)
	}
	if h, ok := NormalizeHex6("#ABC"); !ok || h != "aabbcc" {
		t.Fatalf("NormalizeHex6(#ABC) = %q, %v", h, ok)
	}
	if h, ok := NormalizeHex6("282A37"); !ok || h != "282a37" {
		t.Fatalf("NormalizeHex6(282A37) = %q, %v", h, ok)
	}
	for _, bad := range []string{"", "#", "#12", "#1234", "#12345", "#1234567", "#gggggg", "blue"} {
		if _, _, _, ok := ParseHexRGB(bad); ok {
			t.Errorf("ParseHexRGB(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestUUIDHelpers(t *testing.T) {
	u1 := NewUUIDv4()
	u2 := NewUUIDv4()
	if !IsUUID(u1) || !IsUUID(u2) || u1 == u2 {
		t.Fatalf("NewUUIDv4 produced %q and %q", u1, u2)
	}
	if u1[14] != '4' {
		t.Errorf("UUID version nibble = %c, want '4' in %q", u1[14], u1)
	}
	if !IsUUID("550E8400-E29B-41D4-A716-446655440000") {
		t.Error("uppercase UUID rejected")
	}
	for _, bad := range []string{"", "not-a-uuid", "550e8400e29b41d4a716446655440000", "550e8400-e29b-41d4-a716-44665544000g"} {
		if IsUUID(bad) {
			t.Errorf("IsUUID(%q) = true, want false", bad)
		}
	}
}
