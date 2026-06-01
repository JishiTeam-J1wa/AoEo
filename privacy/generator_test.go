package privacy

import (
	"net"
	"strings"
	"testing"
	"unicode"
)

func TestFakeGenerator_IP(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 20; i++ {
		ip := g.Generate(EntityIP, "192.168.1.1")
		if net.ParseIP(ip) == nil {
			t.Fatalf("invalid IP generated: %s", ip)
		}
		// Must be RFC1918 private IP
		if !strings.HasPrefix(ip, "10.") && !strings.HasPrefix(ip, "172.") && !strings.HasPrefix(ip, "192.168.") {
			t.Fatalf("IP not private: %s", ip)
		}
	}
}

func TestFakeGenerator_Domain(t *testing.T) {
	g := NewFakeGenerator(42)
	domain := g.Generate(EntityDomain, "www.x1.com")
	if domain == "www.x1.com" {
		t.Fatal("domain was not replaced")
	}
	if !strings.Contains(domain, ".") {
		t.Fatalf("invalid domain generated: %s", domain)
	}
}

func TestFakeGenerator_Name(t *testing.T) {
	g := NewFakeGenerator(42)

	// Chinese name
	cn := g.Generate(EntityPerson, "张三")
	if cn == "张三" {
		t.Fatal("Chinese name was not replaced")
	}
	hasCJK := false
	for _, r := range cn {
		if unicode.Is(unicode.Han, r) {
			hasCJK = true
			break
		}
	}
	if !hasCJK {
		t.Fatalf("generated Chinese name has no CJK: %s", cn)
	}

	// English name
	en := g.Generate(EntityPerson, "John Doe")
	if en == "John Doe" {
		t.Fatal("English name was not replaced")
	}
}

func TestFakeGenerator_Phone(t *testing.T) {
	g := NewFakeGenerator(42)
	phone := g.Generate(EntityPhone, "13800138000")
	if len(phone) != 11 || phone[0] != '1' {
		t.Fatalf("invalid phone generated: %s", phone)
	}
}

func TestFakeGenerator_IDCard(t *testing.T) {
	g := NewFakeGenerator(42)
	id := g.Generate(EntityIDCard, "110101199001011234")
	if len(id) != 18 {
		t.Fatalf("ID card not 18 digits: %s (len=%d)", id, len(id))
	}
	// Verify check digit
	base := id[:17]
	expected := computeIDCheckDigit(base)
	if id[17:] != expected {
		t.Fatalf("ID card check digit mismatch: got %s, want %s", id[17:], expected)
	}
}

func TestFakeGenerator_Secret(t *testing.T) {
	g := NewFakeGenerator(42)
	orig := "sk-abc123DEF_!"
	fake := g.Generate(EntitySecret, orig)
	if len(fake) != len(orig) {
		t.Fatalf("secret length changed: %d vs %d", len(fake), len(orig))
	}
}

func TestComputeIDCheckDigit(t *testing.T) {
	// Known valid ID card prefix
	base := "11010119900101123"
	got := computeIDCheckDigit(base)
	// We just verify it doesn't panic and returns a single character
	if len(got) != 1 {
		t.Fatalf("check digit should be 1 char, got %q", got)
	}
}
