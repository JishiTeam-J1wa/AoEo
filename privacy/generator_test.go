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

// ---------------------------------------------------------------------------
// Generate: Email
// ---------------------------------------------------------------------------

func TestFakeGenerator_Email_DomainPreserved(t *testing.T) {
	g := NewFakeGenerator(42)
	email := g.Generate(EntityEmail, "alice@corp.example.com")
	if !strings.HasSuffix(email, "@corp.example.com") {
		t.Fatalf("expected domain preserved, got: %s", email)
	}
	if strings.HasPrefix(email, "alice") {
		t.Fatal("local part should be replaced")
	}
}

func TestFakeGenerator_Email_InvalidFormat(t *testing.T) {
	g := NewFakeGenerator(42)
	email := g.Generate(EntityEmail, "not-an-email")
	if !strings.Contains(email, "@") {
		t.Fatalf("expected fallback email with @, got: %s", email)
	}
	if !strings.HasSuffix(email, "@example.com") {
		t.Fatalf("expected example.com fallback, got: %s", email)
	}
}

// ---------------------------------------------------------------------------
// Generate: Phone (non-Chinese format)
// ---------------------------------------------------------------------------

func TestFakeGenerator_Phone_NonChineseFormat(t *testing.T) {
	g := NewFakeGenerator(42)
	// Non-Chinese phone: +1-555-123-4567
	phone := g.Generate(EntityPhone, "+1-555-123-4567")
	if len(phone) != len("+1-555-123-4567") {
		t.Fatalf("phone length should be preserved: got %d, want %d", len(phone), len("+1-555-123-4567"))
	}
	// Non-digit characters should be preserved.
	if phone[0] != '+' || phone[2] != '-' || phone[6] != '-' || phone[10] != '-' {
		t.Fatalf("non-digit separators should be preserved, got: %s", phone)
	}
}

func TestFakeGenerator_Phone_ChineseFormat(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 10; i++ {
		phone := g.Generate(EntityPhone, "13800138000")
		if len(phone) != 11 {
			t.Fatalf("expected 11 digits, got %d: %s", len(phone), phone)
		}
		if phone[0] != '1' {
			t.Fatalf("expected phone starting with 1, got: %s", phone)
		}
	}
}

// ---------------------------------------------------------------------------
// Generate: Address
// ---------------------------------------------------------------------------

func TestFakeGenerator_Address(t *testing.T) {
	g := NewFakeGenerator(42)
	addr := g.Generate(EntityAddress, "original address")
	if addr == "" {
		t.Fatal("address should not be empty")
	}
	if addr == "original address" {
		t.Fatal("address should be different from original")
	}
	// Should contain a city name.
	cities := []string{"北京市", "上海市", "广州市", "深圳市", "成都市", "杭州市"}
	found := false
	for _, c := range cities {
		if strings.Contains(addr, c) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("address should contain a city, got: %s", addr)
	}
}

// ---------------------------------------------------------------------------
// Generate: URL
// ---------------------------------------------------------------------------

func TestFakeGenerator_URL_HTTPS(t *testing.T) {
	g := NewFakeGenerator(42)
	url := g.Generate(EntityURL, "https://api.example.com/v1/data")
	if !strings.HasPrefix(url, "https://") {
		t.Fatalf("expected https scheme preserved, got: %s", url)
	}
}

func TestFakeGenerator_URL_HTTP(t *testing.T) {
	g := NewFakeGenerator(42)
	url := g.Generate(EntityURL, "http://internal.corp/path")
	if !strings.HasPrefix(url, "http://") {
		t.Fatalf("expected http scheme preserved, got: %s", url)
	}
	if strings.HasPrefix(url, "https://") {
		t.Fatal("should not upgrade http to https")
	}
}

// ---------------------------------------------------------------------------
// Generate: Domain
// ---------------------------------------------------------------------------

func TestFakeGenerator_Domain_WithKnownTLD(t *testing.T) {
	g := NewFakeGenerator(42)
	for _, tld := range []string{"com", "cn", "net", "org"} {
		domain := g.Generate(EntityDomain, "sub.example."+tld)
		if !strings.HasSuffix(domain, "."+tld) {
			t.Fatalf("expected .%s TLD preserved, got: %s", tld, domain)
		}
		if domain == "sub.example."+tld {
			t.Fatalf("domain should be replaced, got same: %s", domain)
		}
	}
}

func TestFakeGenerator_Domain_UnknownTLD(t *testing.T) {
	g := NewFakeGenerator(42)
	domain := g.Generate(EntityDomain, "myhost.internal")
	if !strings.HasSuffix(domain, ".local") {
		t.Fatalf("expected .local suffix for unknown TLD, got: %s", domain)
	}
}

func TestFakeGenerator_Domain_SingleLabel(t *testing.T) {
	g := NewFakeGenerator(42)
	domain := g.Generate(EntityDomain, "localhost")
	if !strings.HasSuffix(domain, ".local") {
		t.Fatalf("expected .local suffix for single label, got: %s", domain)
	}
}

// ---------------------------------------------------------------------------
// Generate: Date
// ---------------------------------------------------------------------------

func TestFakeGenerator_Date(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 20; i++ {
		date := g.Generate(EntityDate, "2024-01-15")
		if len(date) != 10 {
			t.Fatalf("date should be 10 chars (YYYY-MM-DD), got %d: %s", len(date), date)
		}
		parts := strings.Split(date, "-")
		if len(parts) != 3 {
			t.Fatalf("date should have 3 parts, got: %s", date)
		}
		if len(parts[0]) != 4 || len(parts[1]) != 2 || len(parts[2]) != 2 {
			t.Fatalf("date format incorrect: %s", date)
		}
	}
}

// ---------------------------------------------------------------------------
// Generate: Secret (length & character class preservation)
// ---------------------------------------------------------------------------

func TestFakeGenerator_Secret_CharClassPreservation(t *testing.T) {
	g := NewFakeGenerator(42)
	orig := "aA0!bB1@"
	fake := g.Generate(EntitySecret, orig)
	if len(fake) != len(orig) {
		t.Fatalf("length mismatch: got %d, want %d", len(fake), len(orig))
	}
	// Check character classes at each position.
	for i := range orig {
		o := orig[i]
		f := fake[i]
		switch {
		case o >= 'a' && o <= 'z':
			if f < 'a' || f > 'z' {
				t.Fatalf("position %d: expected lowercase, got %c", i, f)
			}
		case o >= 'A' && o <= 'Z':
			if f < 'A' || f > 'Z' {
				t.Fatalf("position %d: expected uppercase, got %c", i, f)
			}
		case o >= '0' && o <= '9':
			if f < '0' || f > '9' {
				t.Fatalf("position %d: expected digit, got %c", i, f)
			}
		default:
			if o != f {
				t.Fatalf("position %d: expected special char %c preserved, got %c", i, o, f)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Generate: Default (genericMask)
// ---------------------------------------------------------------------------

func TestFakeGenerator_Default_GenericMask(t *testing.T) {
	g := NewFakeGenerator(42)
	orig := "hello"
	fake := g.Generate(EntityType("unknown"), orig)
	if fake != "*****" {
		t.Fatalf("expected ***** mask, got: %s", fake)
	}
}

func TestFakeGenerator_Default_GenericMaskUnicode(t *testing.T) {
	g := NewFakeGenerator(42)
	orig := "abc"
	fake := g.Generate(EntityType(""), orig)
	if fake != "***" {
		t.Fatalf("expected *** mask, got: %s", fake)
	}
}

// ---------------------------------------------------------------------------
// NewFakeGenerator
// ---------------------------------------------------------------------------

func TestNewFakeGenerator_FixedSeedReproducible(t *testing.T) {
	g1 := NewFakeGenerator(12345)
	g2 := NewFakeGenerator(12345)

	for i := 0; i < 10; i++ {
		v1 := g1.Generate(EntityIP, "")
		v2 := g2.Generate(EntityIP, "")
		if v1 != v2 {
			t.Fatalf("same seed should produce same values: %s vs %s at iteration %d", v1, v2, i)
		}
	}
}

func TestNewFakeGenerator_DifferentSeedsDiffer(t *testing.T) {
	g1 := NewFakeGenerator(111)
	g2 := NewFakeGenerator(222)

	same := 0
	for i := 0; i < 10; i++ {
		v1 := g1.Generate(EntityIP, "")
		v2 := g2.Generate(EntityIP, "")
		if v1 == v2 {
			same++
		}
	}
	// With different seeds, at least some values should differ.
	if same == 10 {
		t.Fatal("different seeds should produce different values")
	}
}

// ---------------------------------------------------------------------------
// computeIDCheckDigit: correctness
// ---------------------------------------------------------------------------

func TestComputeIDCheckDigit_KnownValues(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		// Manually computed: base "11010119900101001"
		// weights: 7,9,10,5,8,4,2,1,6,3,7,9,10,5,8,4,2
		// sum = 1*7+1*9+0*10+1*5+0*8+1*4+1*2+9*1+9*6+0*3+0*7+1*9+0*10+1*5+0*8+0*4+1*2
		//     = 7+9+0+5+0+4+2+9+54+0+0+9+0+5+0+0+2 = 106
		// 106 % 11 = 7 => checkCodes[7] = "5"
		{"11010119900101001", "5"},
	}

	for _, tt := range tests {
		got := computeIDCheckDigit(tt.base)
		if got != tt.want {
			t.Fatalf("computeIDCheckDigit(%s) = %s, want %s", tt.base, got, tt.want)
		}
	}
}

func TestComputeIDCheckDigit_InvalidLength(t *testing.T) {
	got := computeIDCheckDigit("123")
	if got != "X" {
		t.Fatalf("expected X for invalid length, got %s", got)
	}
}

func TestComputeIDCheckDigit_EmptyString(t *testing.T) {
	got := computeIDCheckDigit("")
	if got != "X" {
		t.Fatalf("expected X for empty string, got %s", got)
	}
}

func TestComputeIDCheckDigit_AllCodes(t *testing.T) {
	// Verify that the function returns valid check codes for any 17-digit input.
	base := "11010119900101000"
	got := computeIDCheckDigit(base)
	validCodes := map[string]bool{"0": true, "1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true, "X": true}
	if !validCodes[got] {
		t.Fatalf("invalid check code: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Generate: IDCard full validation
// ---------------------------------------------------------------------------

func TestFakeGenerator_IDCard_MultipleValid(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 20; i++ {
		id := g.Generate(EntityIDCard, "")
		if len(id) != 18 {
			t.Fatalf("ID card should be 18 chars, got %d: %s", len(id), id)
		}
		// Verify check digit.
		base := id[:17]
		check := computeIDCheckDigit(base)
		if id[17:] != check {
			t.Fatalf("ID card check digit mismatch: id=%s, expected check=%s, got=%s", id, check, id[17:])
		}
		// Verify all first 17 chars are digits.
		for j, c := range base {
			if c < '0' || c > '9' {
				t.Fatalf("ID card base position %d not a digit: %c in %s", j, c, id)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Generate: Name
// ---------------------------------------------------------------------------

func TestFakeGenerator_Name_ChineseMultipleChars(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 5; i++ {
		name := g.Generate(EntityPerson, "李明")
		runeCount := len([]rune(name))
		if runeCount < 2 || runeCount > 3 {
			t.Fatalf("Chinese name should be 2-3 chars, got %d: %s", runeCount, name)
		}
	}
}

func TestFakeGenerator_Name_EnglishFormat(t *testing.T) {
	g := NewFakeGenerator(42)
	for i := 0; i < 5; i++ {
		name := g.Generate(EntityPerson, "Alice Smith")
		parts := strings.Split(name, " ")
		if len(parts) != 2 {
			t.Fatalf("English name should have first and last, got: %s", name)
		}
		if parts[0] == "" || parts[1] == "" {
			t.Fatalf("English name parts should not be empty: %s", name)
		}
	}
}
