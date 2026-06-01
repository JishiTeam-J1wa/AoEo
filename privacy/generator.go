package privacy

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"unicode"
)

// FakeGenerator produces realistic fake data for each entity type.
// It guarantees that generated values look plausible while being entirely
// synthetic.
type FakeGenerator struct {
	rnd *rand.Rand
	mu  sync.Mutex
}

// NewFakeGenerator creates a new generator with the given seed.
// Use a fixed seed for deterministic tests, or time.Now().UnixNano() for
// production randomness.
func NewFakeGenerator(seed int64) *FakeGenerator {
	return &FakeGenerator{rnd: rand.New(rand.NewSource(seed))}
}

// Generate returns a fake value for the given entity type.
func (g *FakeGenerator) Generate(typ EntityType, original string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch typ {
	case EntityIP:
		return g.fakeIP()
	case EntityDomain:
		return g.fakeDomain(original)
	case EntityPerson:
		return g.fakeName(original)
	case EntityPhone:
		return g.fakePhone(original)
	case EntityIDCard:
		return g.fakeIDCard()
	case EntitySecret:
		return g.fakeSecret(original)
	case EntityAddress:
		return g.fakeAddress()
	case EntityEmail:
		return g.fakeEmail(original)
	case EntityURL:
		return g.fakeURL(original)
	case EntityDate:
		return g.fakeDate()
	default:
		return g.genericMask(original)
	}
}

func (g *FakeGenerator) fakeIP() string {
	// Generate RFC1918 private IP to avoid collision with real public IPs.
	switch g.rnd.Intn(3) {
	case 0:
		return fmt.Sprintf("10.%d.%d.%d", g.rnd.Intn(256), g.rnd.Intn(256), g.rnd.Intn(256))
	case 1:
		return fmt.Sprintf("172.%d.%d.%d", 16+g.rnd.Intn(16), g.rnd.Intn(256), g.rnd.Intn(256))
	default:
		return fmt.Sprintf("192.168.%d.%d", g.rnd.Intn(256), g.rnd.Intn(256))
	}
}

func (g *FakeGenerator) fakeDomain(original string) string {
	// Preserve subdomain depth, replace the meaningful parts.
	parts := strings.Split(original, ".")
	if len(parts) >= 2 {
		suffix := parts[len(parts)-1]
		if suffix == "com" || suffix == "cn" || suffix == "net" || suffix == "org" {
			return fmt.Sprintf("internal-%s.masked.%s", g.randomSuffix(6), suffix)
		}
	}
	return fmt.Sprintf("masked-%s.local", g.randomSuffix(6))
}

func (g *FakeGenerator) fakeName(original string) string {
	// Detect if the original looks Chinese (has CJK runes).
	hasCJK := false
	for _, r := range original {
		if unicode.Is(unicode.Han, r) {
			hasCJK = true
			break
		}
	}
	if hasCJK {
		return g.fakeChineseName()
	}
	return g.fakeEnglishName()
}

func (g *FakeGenerator) fakeChineseName() string {
	surnames := []string{"李", "王", "张", "刘", "陈", "杨", "赵", "黄", "周", "吴", "徐", "孙", "马", "朱", "胡"}
	names := []string{"伟", "芳", "娜", "敏", "静", "丽", "强", "磊", "军", "洋", "勇", "艳", "杰", "娟", "涛"}
	return surnames[g.rnd.Intn(len(surnames))] + names[g.rnd.Intn(len(names))]
}

func (g *FakeGenerator) fakeEnglishName() string {
	first := []string{"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda"}
	last := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis"}
	return first[g.rnd.Intn(len(first))] + " " + last[g.rnd.Intn(len(last))]
}

func (g *FakeGenerator) fakePhone(original string) string {
	// If it looks like a Chinese mainland mobile (11 digits starting with 1),
	// preserve that format.
	if len(original) == 11 && original[0] == '1' {
		prefixes := []string{"138", "139", "135", "136", "137", "150", "151", "152", "157", "158", "159", "186", "187", "188"}
		prefix := prefixes[g.rnd.Intn(len(prefixes))]
		return prefix + fmt.Sprintf("%08d", g.rnd.Intn(100000000))
	}
	// Generic: preserve length, replace all digits.
	var b strings.Builder
	for _, c := range original {
		if unicode.IsDigit(c) {
			b.WriteByte(byte('0' + g.rnd.Intn(10)))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (g *FakeGenerator) fakeIDCard() string {
	// Generate a valid 18-digit Chinese ID card number with correct check digit.
	areas := []string{"110101", "310101", "440106", "500101", "510107", "330106", "420106"}
	area := areas[g.rnd.Intn(len(areas))]
	year := 1970 + g.rnd.Intn(40)
	month := 1 + g.rnd.Intn(12)
	day := 1 + g.rnd.Intn(28)
	seq := g.rnd.Intn(1000)
	base := fmt.Sprintf("%s%04d%02d%02d%03d", area, year, month, day, seq)
	return base + computeIDCheckDigit(base)
}

func (g *FakeGenerator) fakeSecret(original string) string {
	// Preserve length and character class distribution.
	var b strings.Builder
	for _, c := range original {
		switch {
		case unicode.IsDigit(c):
			b.WriteByte(byte('0' + g.rnd.Intn(10)))
		case unicode.IsUpper(c):
			b.WriteByte(byte('A' + g.rnd.Intn(26)))
		case unicode.IsLower(c):
			b.WriteByte(byte('a' + g.rnd.Intn(26)))
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (g *FakeGenerator) fakeAddress() string {
	cities := []string{"北京市", "上海市", "广州市", "深圳市", "成都市", "杭州市"}
	districts := []string{"朝阳区", "海淀区", "天河区", "南山区", "武侯区", "西湖区"}
	streets := []string{"建国路", "人民大道", "解放街", "中山路", "建设大道", "和平路"}
	return fmt.Sprintf("%s%s%s%d号%d室",
		cities[g.rnd.Intn(len(cities))],
		districts[g.rnd.Intn(len(districts))],
		streets[g.rnd.Intn(len(streets))],
		100+g.rnd.Intn(900),
		1+g.rnd.Intn(30),
	)
}

func (g *FakeGenerator) fakeEmail(original string) string {
	// Preserve the @domain part if possible.
	parts := strings.Split(original, "@")
	if len(parts) == 2 {
		return fmt.Sprintf("user-%s@%s", g.randomSuffix(6), parts[1])
	}
	return fmt.Sprintf("user-%s@example.com", g.randomSuffix(6))
}

func (g *FakeGenerator) fakeURL(original string) string {
	// Replace path/query but keep scheme roughly similar.
	if strings.HasPrefix(original, "https://") {
		return fmt.Sprintf("https://internal-%s.masked.local/path", g.randomSuffix(6))
	}
	return fmt.Sprintf("http://internal-%s.masked.local/path", g.randomSuffix(6))
}

func (g *FakeGenerator) fakeDate() string {
	year := 1980 + g.rnd.Intn(40)
	month := 1 + g.rnd.Intn(12)
	day := 1 + g.rnd.Intn(28)
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func (g *FakeGenerator) genericMask(original string) string {
	return strings.Repeat("*", len(original))
}

func (g *FakeGenerator) randomSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[g.rnd.Intn(len(letters))]
	}
	return string(b)
}

// computeIDCheckDigit calculates the last check digit for an 18-digit
// Chinese ID card number (first 17 digits provided).
func computeIDCheckDigit(base17 string) string {
	if len(base17) != 17 {
		return "X"
	}
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checkCodes := []string{"1", "0", "X", "9", "8", "7", "6", "5", "4", "3", "2"}

	sum := 0
	for i := 0; i < 17; i++ {
		digit := int(base17[i] - '0')
		sum += digit * weights[i]
	}
	return string(checkCodes[sum%11])
}
